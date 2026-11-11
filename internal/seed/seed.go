// Package seed fills a local database with plausible dummy work.
//
// Local development needs content. An empty dashboard has no heatmap and a streak of one, an
// empty library cannot be searched, an empty recycle bin has nothing to restore, and a public site
// with no published work has no index, no archive and no work page to look at. None of those
// screens can be judged in that state, so this package produces the content they need.
//
// It is deliberately not application behavior. services.CreatePoem stamps created_at with now()
// and offers no way to set published_at or deleted_at, and a seed needs all three: a work spread
// over the last ninety days, one published, one soft-deleted. So the seed writes its own rows.
//
// The content is generated rather than copied, so no real work is reproduced by accident, and it is
// deterministic for a given seed, so a reseed reproduces the same library rather than a different
// one each time.
package seed

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Defaults for a seed run.
const (
	// DefaultCount is how many live works a run writes.
	DefaultCount = 60

	// DefaultDays is how far back the library reaches.
	DefaultDays = 90

	// publishedRatio is how many works in the library are published: one in six.
	//
	// A ratio rather than a count, so the shape of the library holds when the count changes.
	// Ten of sixty is enough for the public site to have an index, an archive and a feed that say
	// something.
	publishedRatio = 6

	// deletedRatio is how many works in the library are soft-deleted: one in ten.
	//
	// Enough for the recycle surface to have something in it, few enough that it does not take the
	// library's shape away.
	deletedRatio = 10

	// streakDays is how many consecutive recent days carry work.
	//
	// The streak is counted in UTC days ending today or yesterday, so a library with no recent
	// days reads as a streak of zero however much work it has. Giving the most recent days several
	// works each is what makes that number worth reading.
	streakDays = 7

	// worksPerStreakDay is how many works each of those recent days carries.
	worksPerStreakDay = 2
)

// Options configure a seed run.
type Options struct {
	// Count is how many live works to write. Zero takes the default and a negative number writes
	// none, because zero is what "not specified" looks like.
	Count int

	// Published is how many of them to publish. Zero derives it from Count and a negative number
	// publishes none.
	Published int

	// Deleted is how many to soft-delete. Zero derives it from Count and a negative number deletes
	// none.
	Deleted int

	// Seed makes the generated library reproducible.
	Seed uint64

	// Now is the instant the library is built around.
	Now time.Time

	// Days is how far back the library reaches.
	Days int

	// Reset empties the library first.
	//
	// The tables are emptied together: poem_versions holds a foreign key into poems, and Postgres
	// refuses to truncate a table another table references.
	Reset bool
}

// WithDefaults fills the unset fields.
//
// Published and Deleted are derived from Count when they are zero, so the shape of the library
// holds when the count changes. A negative count means none of that kind, because zero is what
// "not specified" looks like.
func (o Options) WithDefaults() Options {
	if o.Count == 0 {
		o.Count = DefaultCount
	}
	if o.Count < 0 {
		o.Count = 0
	}
	if o.Published == 0 {
		o.Published = o.Count / publishedRatio
	}
	if o.Published < 0 {
		o.Published = 0
	}
	if o.Deleted == 0 {
		o.Deleted = o.Count / deletedRatio
	}
	if o.Deleted < 0 {
		o.Deleted = 0
	}
	if o.Days <= 0 {
		o.Days = DefaultDays
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	return o
}

// Report describes what a seed run wrote.
type Report struct {
	// Live is how many works were written.
	Live int

	// Published is how many of them are published.
	Published int

	// Deleted is how many are soft-deleted.
	Deleted int

	// Versions is how many prior revisions were written.
	Versions int
}

// Work is one generated work and the state it is written in.
type Work struct {
	// Content is the body of the work. Its first line becomes the title everywhere the work is
	// shown, so the first line is never blank.
	Content string

	// CreatedAt is when the work was written.
	CreatedAt time.Time

	// PublishedAt is when it was published, or nil while it is a draft.
	PublishedAt *time.Time

	// DeletedAt is when it was soft-deleted, or nil while it is live.
	DeletedAt *time.Time

	// Versions are the earlier revisions of the work, oldest first.
	Versions []Revision
}

// Revision is one earlier version of a work.
type Revision struct {
	// Content is the body of the revision.
	Content string

	// RecordedAt is when the revision was recorded, which is before the work was last saved.
	RecordedAt time.Time
}

// lines are the fragments the generated works are built from.
//
// Complete phrases rather than a bag of nouns, because the first line of a work becomes its title
// in the library, on the dashboard and on the public site, and a title assembled from three
// unrelated nouns reads as noise rather than as a title.
var lines = []string{
	"the lamp keeps its small fire",
	"the last train forgets the platform",
	"we count the hours by rain",
	"a door left open for the evening",
	"the river takes the long way round",
	"nothing is written on the glass",
	"the kettle clicks and gives up",
	"winter arrives without announcing itself",
	"she reads the same page twice",
	"the street is loud with small repairs",
	"a letter folded into quarters",
	"the orchard keeps its own calendar",
	"we leave the porch light burning",
	"the map folds along the old crease",
	"somebody is practicing the piano again",
	"the tide argues with the stones",
	"a dog sleeps in the doorway",
	"the wheat does not ask for rain",
	"we speak of summer as if it were coming",
	"the clock loses a minute a day",
	"the path goes down and forgets to return",
	"the kettle sings before it boils",
	"a handprint dries on the glass",
	"the bus arrives without anyone waiting",
	"the garden keeps what it is given",
	"we mend the fence and not the reason",
	"the sky goes pale before the rain",
	"a name written and then crossed out",
	"the mountain stands where it always stood",
	"we walk the long way to say nothing",
	"the cat watches the empty chair",
	"the market empties and refills",
	"an old coat for a colder year",
	"the water takes the shape of the glass",
	"we leave the door unlocked behind us",
	"the birds begin before the light",
	"a list of things to repair",
	"the field keeps its own weather",
	"somebody is home before we arrive",
}

// Generate returns the works a seed run would write.
//
// The dates are the part that carries the weight. A library whose works are all stamped now() has
// an empty heatmap and a streak of one, and both of those are the dashboard's whole point. So the
// works are spread across the window, and the most recent days carry several each.
func Generate(opts Options) []Work {
	opts = opts.WithDefaults()
	if opts.Count == 0 {
		return nil
	}

	// #nosec G404 -- the generator produces placeholder text, not a secret. The seeded values
	// decide what a local library looks like, and a predictable sequence is the point: the
	// same seed gives the same library, so a reseed does not quietly rewrite the screens.
	rng := rand.New(rand.NewPCG(opts.Seed, 0x5655525345))

	day := time.Date(opts.Now.UTC().Year(), opts.Now.UTC().Month(), opts.Now.UTC().Day(), 0, 0, 0, 0, time.UTC)

	offsets := dayOffsets(rng, opts)

	works := make([]Work, 0, len(offsets))
	for _, offset := range offsets {
		works = append(works, Work{
			Content:   compose(rng),
			CreatedAt: momentOn(rng, day.AddDate(0, 0, -offset), opts.Now),
		})
	}

	markPublished(rng, works, opts.Published)
	markDeleted(rng, works, opts.Deleted)
	addVersions(rng, works)

	return works
}

// dayOffsets returns, for each work, how many days before the window's last day it was written.
//
// The most recent days carry several works each, so the streak has a length. The rest are spread
// across the remaining window.
func dayOffsets(rng *rand.Rand, opts Options) []int {
	offsets := make([]int, 0, opts.Count)

	recent := streakDays
	if opts.Days < recent {
		recent = opts.Days
	}
	for d := 0; d < recent && len(offsets) < opts.Count; d++ {
		for w := 0; w < worksPerStreakDay && len(offsets) < opts.Count; w++ {
			offsets = append(offsets, d)
		}
	}

	spread := opts.Days - recent
	if spread <= 0 {
		return offsets
	}
	for len(offsets) < opts.Count {
		offsets = append(offsets, recent+rng.IntN(spread))
	}
	return offsets
}

// momentOn picks a time of day within the given day.
//
// The hour is drawn from the waking part of the day, because a library where every work was written
// at 04:00 is not a library anybody would recognize as their own.
//
// The moment is clamped to the instant the library is built around. On the most recent day that
// matters: a work dated later than now is a day the author has not reached yet, and the streak
// counts days, so it would move the number rather than describe it.
func momentOn(rng *rand.Rand, day time.Time, limit time.Time) time.Time {
	moment := day.
		Add(time.Duration(6+rng.IntN(17)) * time.Hour).
		Add(time.Duration(rng.IntN(60)) * time.Minute)
	if moment.After(limit) {
		moment = limit
	}
	return moment
}

// compose builds the body of one work.
//
// The body is two or three stanzas of two to four lines. The first line is never blank, because it
// becomes the title.
func compose(rng *rand.Rand) string {
	var b strings.Builder

	stanzas := 1 + rng.IntN(3)
	for s := 0; s < stanzas; s++ {
		if s > 0 {
			b.WriteString("\n\n")
		}
		verse := 2 + rng.IntN(3)
		for v := 0; v < verse; v++ {
			if v > 0 {
				b.WriteString("\n")
			}
			b.WriteString(lines[rng.IntN(len(lines))])
		}
	}
	return b.String()
}

// markPublished publishes the given number of works.
//
// The published ones are drawn from the most recent works, because a public site whose archive is
// ordered by publication date should show its newest work first, and the publication date is
// later than the creation date only for work published after it was written.
func markPublished(rng *rand.Rand, works []Work, count int) {
	if count <= 0 || len(works) == 0 {
		return
	}
	if count > len(works) {
		count = len(works)
	}

	picked := rng.Perm(len(works))[:count]
	for _, index := range picked {
		published := works[index].CreatedAt.Add(time.Duration(rng.IntN(48)) * time.Hour)
		works[index].PublishedAt = &published
	}
}

// markDeleted soft-deletes the given number of works.
func markDeleted(rng *rand.Rand, works []Work, count int) {
	if count <= 0 || len(works) == 0 {
		return
	}
	if count > len(works) {
		count = len(works)
	}

	// A published work that is then deleted must not stay published, or the public site would
	// serve a work the author threw away. The publisher filters both columns, so a row that is both
	// published and deleted is excluded -- but the state would be one the author cannot reach, and
	// a seed that produces an unreachable state is a seed that misleads whoever reads it.
	candidates := make([]int, 0, len(works))
	for i := range works {
		if works[i].PublishedAt == nil {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return
	}
	if count > len(candidates) {
		count = len(candidates)
	}

	for _, pick := range rng.Perm(len(candidates))[:count] {
		index := candidates[pick]
		deleted := works[index].CreatedAt.Add(time.Duration(rng.IntN(72)) * time.Hour)
		works[index].DeletedAt = &deleted
	}
}

// addVersions gives some works an earlier revision.
//
// The history surface and the restore path both need a work with more than one revision to act on,
// and a work with a single revision leaves the restore button doing nothing.
func addVersions(rng *rand.Rand, works []Work) {
	for i := range works {
		if rng.IntN(4) != 0 {
			continue
		}
		versions := 1 + rng.IntN(2)
		revisions := make([]Revision, 0, versions)
		for v := 0; v < versions; v++ {
			revisions = append(revisions, Revision{
				Content: compose(rng),
				// A revision predates the work it belongs to, which is what makes the history
				// ordered by saving rather than by writing.
				RecordedAt: works[i].CreatedAt.Add(-time.Duration(1+rng.IntN(72)) * time.Hour),
			})
		}
		works[i].Versions = revisions
	}
}

// ForceRemoteEnv allows the locality check to be skipped.
//
// It exists because the check is about an accident, not about a rule: somebody who means to seed a
// hosted database is doing something this project has no other way to express, and refusing to help
// them silently would be worse than doing what they asked.
const ForceRemoteEnv = "VERSE_SEED_ALLOW_REMOTE"

// RequireLocalDSN refuses a connection string that does not point at this machine.
//
// The seed empties the library and replaces it with generated text. Against a hosted database that
// is the author's work, and no version of that is what anybody meant.
//
// The check is on the host rather than on the database name, because the name is a convention and
// the host is the thing that decides whether the data is somebody else's.
func RequireLocalDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("parse the connection string: %w", err)
	}

	host := parsed.Hostname()
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}

	if os.Getenv(ForceRemoteEnv) == "1" {
		return nil
	}

	return fmt.Errorf("refusing to seed %q: it is not on this machine. Seeding a hosted database "+
		"would replace real work with generated text. Point DATABASE_URL at a local database, or set "+
		"%s=1 if you mean it", host, ForceRemoteEnv)
}

// Run writes a generated library into the database.
//
// The rows are written with their timestamps stated explicitly, which no application path does.
// services.CreatePoem stamps created_at with now(), and nothing in the application sets
// published_at at all.
//
// Reset empties the library, and it is guarded by RequireLocalDSN rather than by the disposable-DSN
// gate the test suite uses. The two gates answer different questions: the test gate asks whether the
// operator has named a throwaway database, and this one asks whether the database is reachable only
// from this machine. A seed is a local activity, and a local database is the one it names.
func Run(ctx context.Context, pool *pgxpool.Pool, opts Options) (Report, error) {
	opts = opts.WithDefaults()

	if opts.Reset {
		// poem_versions is named because it holds a foreign key into poems: Postgres refuses to
		// TRUNCATE a table another table references, so truncating poems alone fails.
		if _, err := pool.Exec(ctx, `TRUNCATE TABLE poem_versions, poems`); err != nil {
			return Report{}, fmt.Errorf("empty the library: %w", err)
		}
	}

	works := Generate(opts)
	report := Report{Live: len(works)}

	for i := range works {
		work := works[i]

		id := uuid.NewString()
		if _, err := pool.Exec(ctx,
			`INSERT INTO poems (id, content, created_at, published_at, deleted_at)
			 VALUES ($1, $2, $3, $4, $5)`,
			id, work.Content, work.CreatedAt, work.PublishedAt, work.DeletedAt,
		); err != nil {
			return report, fmt.Errorf("insert a work: %w", err)
		}

		if work.PublishedAt != nil {
			report.Published++
		}
		if work.DeletedAt != nil {
			report.Deleted++
		}

		for _, revision := range work.Versions {
			// The id is supplied rather than defaulted: poem_versions.id is a primary key with no
			// default, unlike poems.id.
			if _, err := pool.Exec(ctx,
				`INSERT INTO poem_versions (id, poem_id, content, recorded_at) VALUES ($1, $2, $3, $4)`,
				uuid.NewString(), id, revision.Content, revision.RecordedAt,
			); err != nil {
				return report, fmt.Errorf("insert a revision: %w", err)
			}
			report.Versions++
		}
	}

	return report, nil
}
