package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// LegacyField 是一次仅依赖当前扫描的唯一解码，字段独立幂等。
type LegacyField struct {
	Track, Name, Value string
	Scan               json.RawMessage
}

func (f LegacyField) Key() string { return operations.Fingerprint(f) }

func LegacyFields(e catalog.CurationEvidence) []LegacyField {
	var result []LegacyField
	for _, field := range []string{"title", "album_artist"} {
		if vals := e.Legacy[field]; len(vals) == 1 && !e.Corrected[field] && !e.Automatic[field] {
			result = append(result, LegacyField{Name: field, Value: vals[0], Scan: e.FieldInputs[field]})
		}
	}
	for _, t := range e.Tracks {
		for _, field := range []string{"title", "artist"} {
			vals := t.Legacy[field]
			if len(vals) != 1 {
				continue
			}
			basis, protected := t.Title, t.TitleCorrected || t.TitleAutomatic
			if field == "artist" {
				basis, protected = t.Artist, t.ArtistCorrected || t.ArtistAutomatic
			}
			if protected {
				continue
			}
			scan, _ := json.Marshal(basis)
			result = append(result, LegacyField{Track: t.ID, Name: field, Value: vals[0], Scan: scan})
		}
	}
	return result
}

// 写入锁内复核可整理字段与原字节的唯一解码，防止清理后的显示串相同但原输入已变。
func (f LegacyField) check(ctx context.Context, tx *sql.Tx, release string) error {
	var ready bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_stable_releases WHERE id=$1)
 AND NOT EXISTS(SELECT FROM scan_runs WHERE status='running')`, release).Scan(&ready); err != nil {
		return err
	}
	if ready {
		entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{release})
		if err != nil {
			return err
		}
		if slices.ContainsFunc(LegacyFields(entries[release]), func(v LegacyField) bool {
			return v.Track == f.Track && v.Name == f.Name && v.Value == f.Value
		}) {
			return nil
		}
	}
	return operations.Conflict("nothing_to_apply", "扫描依据已变化或发行尚未稳定")
}

func (w *Writer) ApplyLegacyField(ctx context.Context, release string, f LegacyField, batch, key string) error {
	_, err := w.applyFields(ctx, Request{legacy: &f, ReleaseID: release, BatchID: batch, Key: key,
		Source:       Source{Kind: "source", Provider: "legacy_encoding", Basis: "标签原字节只有一种可无损还原的本地读法", WriterLevel: catalog.LevelModelLocal},
		Fields:       map[string]catalog.CorrectionIntent{f.Name: {Mode: catalog.CorrectionSet, Value: f.Value}},
		ExpectedScan: map[string]json.RawMessage{f.Name: f.Scan},
	}, f.Track, f.Track != "")
	return err
}
