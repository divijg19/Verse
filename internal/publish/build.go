package publish

import (
	"context"
	"fmt"
	"path/filepath"

	publictempl "github.com/divijg19/Verse/templ"
)

// Report describes a completed build.
type Report struct {
	Works int
	Files int
	Bytes int64
	Out   string
}

// Build reads the published set, renders it, validates the result, and only then replaces the output
// directory.
//
// The order is the safety property: validation runs against bytes in memory, so a build that would
// leak an authoring route or produce a broken internal link fails without touching whatever is
// currently deployed.
func Build(ctx context.Context, out string, opts Options) (Report, error) {
	works, digest, err := Load(ctx)
	if err != nil {
		return Report{}, err
	}
	opts.Digest = digest

	return build(ctx, out, opts, works)
}

// BuildFile is Build, reading the published set from an export file rather than from the database.
//
// The two differ in where the works come from and in nothing else. The rendering, the validation and
// the write are the same code, so a build from a file and a build from the database produce the same
// bytes -- which is what makes the file a usable artifact rather than a second publisher with its own
// idea of what is public.
func BuildFile(ctx context.Context, path, out string, opts Options) (Report, error) {
	works, digest, err := LoadFile(path)
	if err != nil {
		return Report{}, err
	}

	// The digest in the file is the published set's own, and it is what the site records. It is
	// not taken from the options, because the options do not know it: the file is the authority on
	// what it contains.
	opts.Digest = digest

	return build(ctx, out, opts, works)
}

// build renders, validates and writes a set of works that has already been read.
func build(ctx context.Context, out string, opts Options, works []publictempl.PublicWork) (Report, error) {
	files, err := Render(works, opts)
	if err != nil {
		return Report{}, fmt.Errorf("render the public site: %w", err)
	}

	if err := Validate(files, opts.BaseURL); err != nil {
		return Report{}, fmt.Errorf("the public build failed validation: %w", err)
	}

	if err := WriteDir(out, files); err != nil {
		return Report{}, err
	}

	report := Report{Works: len(works), Files: len(files), Out: filepath.Clean(out)}
	for _, body := range files {
		report.Bytes += int64(len(body))
	}

	return report, nil
}
