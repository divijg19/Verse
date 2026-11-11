package publish

import (
	"context"
	"fmt"
	"path/filepath"
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
	works, err := Load(ctx)
	if err != nil {
		return Report{}, err
	}

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
