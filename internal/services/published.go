package services

import (
	"context"

	"github.com/divijg19/Verse/internal/database"
	"github.com/divijg19/Verse/internal/models"
)

// ListPublishedPoems returns every published work, in publication order.
//
// This is the publisher's read, and it is the first query in the repository whose purpose is to
// decide what leaves the private side of the split. It is therefore deliberately its own function
// rather than a flag on listLivePoems or on export.Build, and the reason is a mistake that has to be
// impossible to make by accident:
//
// A boolean reaches a query that already filters deleted_at, and the combination "a draft leaks into
// the public site" is one word of difference. With a separate function there is no parameter that
// could carry it -- a caller wanting published works calls this, and a caller not wanting them cannot
// call this at all. The authoring app does not call it, so no status badge, filter, or count in that
// app is affected by the split, which is why the split needed no UI work at all.
//
// No limit, and no paging. The publisher reads the whole set at build time, and the failure this
// avoids is a real one: an archive that silently stops at the end of a page is a data-loss bug that
// looks like success. listLivePoems defaults to 100 rows and is correct to, because a page of a
// library is what its caller asked for -- but a caller here has asked for everything, and returning
// a page would report a partial library as the whole of it. CountPublishedPoems exists so a caller
// can detect that case if a future version of this query ever gains a bound.
//
// Order is published_at DESC, id DESC. For the publisher this is the order a reader met the work in,
// which is what a listing wants; for the author it is invisible, since they never call this. The id
// tiebreak is the same total-order requirement as listLivePoems: published_at can tie, and an archive
// whose order is not total produces different output for the same input across builds, which defeats
// the byte-identical export check this project relies on.
func ListPublishedPoems(ctx context.Context) ([]models.Poem, error) {
	if err := database.Require(); err != nil {
		return nil, err
	}

	rows, err := database.Pool.Query(ctx, `
        SELECT `+models.PublishedPoemColumns+`
        FROM poems
        WHERE `+models.PublishedFilter+`
        ORDER BY published_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.Poem
	for rows.Next() {
		var p models.Poem
		if err := rows.Scan(&p.ID, &p.Content, &p.CreatedAt, &p.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CountPublishedPoems returns how many works the publisher would read.
//
// Exists because ListPublishedPoems returns a slice, and a slice length is the one thing a caller
// cannot get wrong and be right: an archive built from a truncated set is not a smaller archive, it
// is a lie about the size of the library. The dry-run enumerates by querying, and comparing that
// count against len(ListPublishedPoems) is how a future change that adds a bound gets caught -- by
// TestTheDryRunCountMatchesThePublishedSet rather than by a reader noticing a missing work years
// later.
func CountPublishedPoems(ctx context.Context) (int, error) {
	if err := database.Require(); err != nil {
		return 0, err
	}

	// int64 rather than int, because COUNT returns bigint and a narrowing conversion here would be a
	// silent overflow on a library large enough to matter.
	var count int64
	if err := database.Pool.QueryRow(ctx, `
        SELECT COUNT(*)
        FROM poems
        WHERE `+models.PublishedFilter).Scan(&count); err != nil {
		return 0, err
	}
	return int(count), nil
}
