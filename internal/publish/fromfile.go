package publish

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/divijg19/Verse/internal/export"
	publictempl "github.com/divijg19/Verse/templ"
)

// LoadFile reads the published works out of an export file.
//
// This is the path the public build takes when it is given an artifact rather than a database. The
// artifact is written by one component and read by another -- the authoring side produces it, and
// the build consumes it -- so the two are separated by a trust boundary, and the file is checked
// rather than believed.
//
// Three things are checked.
//
// The format has to be one this build understands. A file that says it is something else is not an
// export, and reading it as one would produce a site that is missing whatever it does not have.
//
// The version has to be one this build understands, and it has to carry the publication state. A
// version 1 export predates the publication column, so it cannot say which works are public, and a
// build from it would publish either everything or nothing. Neither is a site anybody meant.
//
// The digest has to match the contents. The digest is over the published set, so it changes exactly
// when the site would change, and a file whose digest does not match its contents has been
// mangled between being written and being read.
func LoadFile(path string) ([]publictempl.PublicWork, string, error) {
	// #nosec G304 -- the path is the one the operator named on the command line, not request
	// input. The build reads the artifact it was told to read.
	body, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, "", fmt.Errorf("read the export file: %w", err)
	}

	var doc export.Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, "", fmt.Errorf("parse the export file: %w", err)
	}

	if doc.Format != export.Format {
		return nil, "", fmt.Errorf("the export file is in the %q format, not %q", doc.Format, export.Format)
	}
	if doc.Version != export.FormatVersion {
		return nil, "", fmt.Errorf("the export file is version %d and this build reads version %d",
			doc.Version, export.FormatVersion)
	}

	if digest := export.PublishedDigest(doc.Poems); doc.Digest != "" && digest != doc.Digest {
		return nil, "", fmt.Errorf("the export file's digest does not match its contents: " +
			"the file was changed after it was written")
	}

	sources := make([]source, 0, len(doc.Poems))
	for _, poem := range doc.Poems {
		if poem.PublishedAt == nil || poem.DeletedAt != nil {
			continue
		}
		sources = append(sources, source{
			ID:          poem.ID,
			Content:     poem.Content,
			PublishedAt: poem.PublishedAt.UTC(),
		})
	}

	// The order is applied by publicWorks, which is where the database path gets it too.
	return publicWorks(sources), doc.Digest, nil
}
