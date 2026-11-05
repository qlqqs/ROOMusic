package governance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type artistChangeName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type artistChangedDisplay struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}
type artistChangeDescription struct {
	Kind         string                `json:"kind"`
	Source       *artistChangeName     `json:"source,omitempty"`
	Target       *artistChangeName     `json:"target,omitempty"`
	OtherArtist  *artistChangeName     `json:"other_artist,omitempty"`
	Mode         string                `json:"mode,omitempty"`
	NameCount    *int                  `json:"name_count,omitempty"`
	ReleaseCount *int                  `json:"release_count,omitempty"`
	TrackCount   *int                  `json:"track_count,omitempty"`
	From         *artistChangedDisplay `json:"from,omitempty"`
	To           *artistChangedDisplay `json:"to,omitempty"`
	Ignored      *bool                 `json:"ignored,omitempty"`
}

func validArtistChangeName(n *artistChangeName) bool {
	if n == nil || !uuid.Valid(n.ID) {
		return false
	}
	v, e := NormalizeArtistName(n.Name)
	return e == nil && v == n.Name
}
func validArtistDisplay(n *artistChangedDisplay) bool {
	if n == nil || (n.Source != "auto" && n.Source != "manual") {
		return false
	}
	v, e := NormalizeArtistName(n.Name)
	return e == nil && v == n.Name
}
func validArtistCount(n *int, max int) bool { return n != nil && *n >= 0 && *n <= max }
func decodeArtistChanges(record operations.Record) (*artistChangeDescription, string) {
	var before, after struct {
		SchemaVersion int              `json:"schema_version"`
		Artists       []artistIdentity `json:"artists"`
		Changes       json.RawMessage  `json:"changes"`
	}
	if len(record.Before) > 8*1024*1024 || len(record.After) > 8*1024*1024 || json.Unmarshal(record.Before, &before) != nil || json.Unmarshal(record.After, &after) != nil || before.SchemaVersion != 1 || after.SchemaVersion != 1 {
		return nil, ""
	}
	var changes artistChangeDescription
	decoder := json.NewDecoder(bytes.NewReader(after.Changes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&changes) != nil || changes.Kind != record.ChangeType() {
		return nil, ""
	}
	valid := false
	switch changes.Kind {
	case operations.TypeArtistMerge, operations.TypeArtistSplit:
		valid = validArtistChangeName(changes.Source) && validArtistChangeName(changes.Target) && validArtistCount(changes.NameCount, maxArtistBindings) && validArtistCount(changes.ReleaseCount, maxArtistTracks) && validArtistCount(changes.TrackCount, maxArtistTracks) && changes.From == nil && changes.To == nil && changes.OtherArtist == nil && changes.Ignored == nil
		if changes.Kind == operations.TypeArtistSplit {
			valid = valid && (changes.Mode == "aliases" || changes.Mode == "albums") && changes.Source.ID == record.TargetID
		} else {
			valid = valid && changes.Mode == "" && changes.Target.ID == record.TargetID
		}
	case operations.TypeArtistRename:
		valid = validArtistDisplay(changes.From) && validArtistDisplay(changes.To) && changes.Source == nil && changes.Target == nil && changes.OtherArtist == nil && changes.Ignored == nil && changes.NameCount == nil && changes.ReleaseCount == nil && changes.TrackCount == nil && changes.Mode == ""
	case operations.TypeArtistCandidateIgnore:
		valid = validArtistChangeName(changes.OtherArtist) && changes.Ignored != nil && changes.Source == nil && changes.Target == nil && changes.From == nil && changes.To == nil && changes.NameCount == nil && changes.ReleaseCount == nil && changes.TrackCount == nil && changes.Mode == ""
	}
	if !valid {
		return nil, ""
	}
	for _, a := range after.Artists {
		if a.ID == record.TargetID {
			if _, err := NormalizeArtistName(a.Name); err == nil {
				return &changes, a.Name
			}
		}
	}
	return nil, ""
}

// describeArtistRecords 用同一 SQL 快照判定发布状态和读取当前摘要，整理期间只使用历史名称。
func describeArtistRecords(ctx context.Context, q operations.Queryer, records []operations.Record) (map[string]operations.Description, error) {
	out := map[string]operations.Description{}
	if len(records) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(records))
	historical := map[string]string{}
	for _, r := range records {
		ids = append(ids, r.TargetID)
		change, name := decodeArtistChanges(r)
		d := operations.Description{}
		if change != nil {
			d.Changes = change
		}
		out[r.ID] = d
		historical[r.ID] = name
	}
	rows, err := q.QueryContext(ctx, `WITH readiness AS (
 SELECT COALESCE((SELECT status='ready' AND manifest_sealed AND target_generation=published_generation
 AND target_generation>0 AND textnorm_version=published_textnorm_version AND search_projection_version=published_search_projection_version
 AND textnorm_version=$2 AND search_projection_version=$3 FROM catalog_projection_state WHERE capability='artists'),false) AS ready)
 SELECT a.id::text,CASE WHEN readiness.ready THEN COALESCE(p.effective_display_name,a.initial_name) ELSE NULL END,
 readiness.ready AND o.redirect_artist_id IS NULL AND EXISTS(SELECT 1 FROM artist_effective_credits e
 LEFT JOIN tracks t ON t.id=e.track_id WHERE e.artist_id=a.id AND (t.source_status='present' OR (e.release_id IS NOT NULL AND EXISTS(
 SELECT 1 FROM tracks rt JOIN media rm ON rm.id=rt.medium_id WHERE rm.release_id=e.release_id AND rt.source_status='present')))),
 CASE WHEN readiness.ready THEN o.redirect_artist_id::text ELSE NULL END,a.initial_name
 FROM artists a CROSS JOIN readiness LEFT JOIN artist_profiles p ON p.artist_id=a.id AND readiness.ready
 LEFT JOIN artist_overrides o ON o.artist_id=a.id WHERE a.id=ANY($1::text[]::uuid[])`, ids, textnorm.Version, artistgraph.Version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	infos := map[string]operations.ArtistInfo{}
	initials := map[string]string{}
	for rows.Next() {
		var info operations.ArtistInfo
		var name sql.NullString
		var initial string
		if err := rows.Scan(&info.ID, &name, &info.Visible, &info.RedirectArtistID, &initial); err != nil {
			return nil, err
		}
		info.Name = name.String
		infos[info.ID] = info
		initials[info.ID] = initial
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, record := range records {
		d := out[record.ID]
		if info, ok := infos[strings.ToLower(record.TargetID)]; ok {
			if info.Name == "" {
				info.Name = historical[record.ID]
				if info.Name == "" {
					info.Name = initials[info.ID]
				}
			}
			d.Artist = &info
		}
		out[record.ID] = d
	}
	return out, nil
}
func artistNotRevertible() (operations.Outcome, error) {
	return operations.Outcome{}, operations.Conflict("not_revertible", "请通过拆开、合并或再次改名恢复")
}

// ArtistMergeKind 不提供独立撤销；恢复通过正常的反向命令完成。
type ArtistMergeKind struct{}

func (ArtistMergeKind) Type() string { return operations.TypeArtistMerge }
func (ArtistMergeKind) Revert(context.Context, *sql.Tx, string, operations.Record) (operations.Outcome, error) {
	return artistNotRevertible()
}
func (ArtistMergeKind) Describe(ctx context.Context, q operations.Queryer, r []operations.Record) (map[string]operations.Description, error) {
	return describeArtistRecords(ctx, q, r)
}

// ArtistSplitKind 不提供独立撤销；恢复通过正常的反向命令完成。
type ArtistSplitKind struct{}

func (ArtistSplitKind) Type() string { return operations.TypeArtistSplit }
func (ArtistSplitKind) Revert(context.Context, *sql.Tx, string, operations.Record) (operations.Outcome, error) {
	return artistNotRevertible()
}
func (ArtistSplitKind) Describe(ctx context.Context, q operations.Queryer, r []operations.Record) (map[string]operations.Description, error) {
	return describeArtistRecords(ctx, q, r)
}

// ArtistRenameKind 不提供独立撤销；恢复通过正常的反向命令完成。
type ArtistRenameKind struct{}

func (ArtistRenameKind) Type() string { return operations.TypeArtistRename }
func (ArtistRenameKind) Revert(context.Context, *sql.Tx, string, operations.Record) (operations.Outcome, error) {
	return artistNotRevertible()
}
func (ArtistRenameKind) Describe(ctx context.Context, q operations.Queryer, r []operations.Record) (map[string]operations.Description, error) {
	return describeArtistRecords(ctx, q, r)
}

// ArtistCandidateIgnoreKind 不提供独立撤销；恢复通过正常的反向命令完成。
type ArtistCandidateIgnoreKind struct{}

func (ArtistCandidateIgnoreKind) Type() string { return operations.TypeArtistCandidateIgnore }
func (ArtistCandidateIgnoreKind) Revert(context.Context, *sql.Tx, string, operations.Record) (operations.Outcome, error) {
	return artistNotRevertible()
}
func (ArtistCandidateIgnoreKind) Describe(ctx context.Context, q operations.Queryer, r []operations.Record) (map[string]operations.Description, error) {
	return describeArtistRecords(ctx, q, r)
}
