package program

import (
	"context"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type primaryNameLookup struct {
	LinkID     string `json:"link_id"`
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
}

// 缺主名的旧对应按原始艺人条目补齐。查询期间对应可能被替换，落库时重新核对归属与编号。
func (w *Worker) fillArtistPrimaryName(ctx context.Context, artist string, link primaryNameLookup) error {
	var name, id string
	var err error
	switch link.Source {
	case "musicbrainz":
		finder, ok := w.sources.ArtistFinder(link.Source)
		if !ok {
			return nil
		}
		var entry sources.Artist
		entry, err = finder.ArtistByID(ctx, link.ExternalID)
		id, name = entry.ID, entry.Name
	case "wikidata":
		var entry sources.WikidataArtist
		entry, err = w.sources.WikidataArtist(ctx, link.ExternalID)
		id, name = entry.WikidataID, entry.Name
	}
	if errors.Is(err, sources.ErrNotFound) {
		return nil
	}
	if err != nil {
		return sourceFailure(ctx, link.Source, err)
	}
	name = strings.TrimSpace(name)
	if !strings.EqualFold(id, link.ExternalID) || name == "" {
		return nil
	}
	if err = w.ready(ctx); err != nil {
		return err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE external_links l SET artist_primary_name=$5
 FROM artist_merged_primary_links p WHERE p.artist_id=$1 AND p.link_id=$2 AND p.source=$3 AND p.external_id=$4
 AND p.artist_primary_name IS NULL AND l.id=p.link_id`, artist, link.LinkID, link.Source, link.ExternalID, name)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errReleaseChanged
	}
	if err = catalog.RefreshMergedArtistProfiles(ctx, tx, []string{artist}); err != nil {
		return err
	}
	return tx.Commit()
}
