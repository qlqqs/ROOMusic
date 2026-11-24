package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// ReleaseDigest 与发行匹配工作状态共用同一份目录证据。
func ReleaseDigest(e catalog.CurationEvidence) string { return operations.Fingerprint(e) }

type MergeCandidate struct {
	ReleaseID  string `json:"release_id"`
	Title      string `json:"title"`
	DiscCount  int    `json:"disc_count"`
	TrackCount int    `json:"track_count"`
}

func ReleaseMergeCandidate(item PendingItem) (MergeCandidate, error) {
	var candidates []MergeCandidate
	if item.Kind != "release_merge" || json.Unmarshal(item.Candidates, &candidates) != nil || len(candidates) != 1 || !uuid.Valid(candidates[0].ReleaseID) || candidates[0].ReleaseID == item.TargetID {
		return MergeCandidate{}, operations.Conflict("item_stale", "待定项没有唯一的来源发行")
	}
	return candidates[0], nil
}

type MergeReleaseEvidence struct {
	MergeCandidate
	ExternalID         string `json:"external_id"`
	OperationID        string `json:"operation_id,omitempty"`
	ExternalDiscCount  any    `json:"external_disc_count"`
	ExternalTrackCount any    `json:"external_track_count"`
	RecordingCount     int    `json:"recording_count"`
	recordings         map[string]bool
	digest, group      string
}

const mergeLayoutTrackLimit = 300

type mergeLayoutTrack struct {
	Position   int    `json:"position"`
	Title      string `json:"title"`
	ExternalID string `json:"external_id"`
}

type mergeLayoutMedium struct {
	Position       int                `json:"position"`
	From           string             `json:"from"`
	FromReleaseID  string             `json:"from_release_id"`
	OriginPosition int                `json:"origin_position"`
	Title          string             `json:"title"`
	TrackCount     int                `json:"track_count"`
	Tracks         []mergeLayoutTrack `json:"tracks,omitzero"`
}

type mergeLayoutEvidence struct {
	Layout           []mergeLayoutMedium `json:"layout"`
	LayoutTrackTotal int                 `json:"layout_track_total"`
	TracksOmitted    bool                `json:"tracks_omitted,omitempty"`
}

type ReleaseMergeEvidence struct {
	*mergeLayoutEvidence
	Target            MergeReleaseEvidence `json:"target"`
	Source            MergeReleaseEvidence `json:"source"`
	RecordingsOverlap bool                 `json:"recordings_overlap"`
}

func releaseMergeMembers(ctx context.Context, q operations.Queryer, ids []string, entries map[string]catalog.CurationEvidence) (map[string]MergeReleaseEvidence, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	members := make(map[string]MergeReleaseEvidence, len(entries))
	for id, e := range entries {
		m := MergeReleaseEvidence{MergeCandidate: MergeCandidate{id, e.Title, len(e.Media), len(e.Tracks)}, ExternalID: e.ExternalID, ExternalDiscCount: "不可用", ExternalTrackCount: "不可用", digest: ReleaseDigest(e), group: e.GroupID, recordings: map[string]bool{}}
		for _, t := range e.Tracks {
			if t.ExternalID != "" {
				m.recordings[t.ExternalID] = true
			}
		}
		m.RecordingCount = len(m.recordings)
		members[id] = m
	}
	rows, err := q.QueryContext(ctx, `SELECT l.object_id,o.id,o.after_state->'external'->>'disc_count',o.after_state->'external'->>'track_count'
 FROM external_links l JOIN operations o ON o.id=l.operation_id AND o.operation_type='external_link'
 AND o.target_type='release' AND o.target_id=l.object_id AND o.after_state->>'source'='musicbrainz'
 WHERE l.source='musicbrainz' AND l.object_type='release' AND l.object_id=ANY($1::uuid[])
 AND o.after_state->'links' @> jsonb_build_array(jsonb_build_object('object_id',l.object_id,'object_type','release','external_id',l.external_id))
 AND NOT EXISTS(SELECT 1 FROM operations r WHERE r.reverts_operation_id=o.id)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, op string
		var discs, tracks *int
		if err := rows.Scan(&id, &op, &discs, &tracks); err != nil {
			return nil, err
		}
		m, visible := members[id]
		if !visible {
			continue
		}
		m.OperationID = op
		if discs != nil {
			m.ExternalDiscCount = *discs
		}
		if tracks != nil {
			m.ExternalTrackCount = *tracks
		}
		members[id] = m
	}
	return members, rows.Err()
}

func mergePair(a, b MergeReleaseEvidence) ReleaseMergeEvidence {
	p := ReleaseMergeEvidence{Target: a, Source: b}
	for id := range a.recordings {
		if b.recordings[id] {
			p.RecordingsOverlap = true
			break
		}
	}
	return p
}

func (p ReleaseMergeEvidence) inputDigest() string {
	return operations.Fingerprint([]string{p.Target.digest, p.Source.digest, p.Target.OperationID, p.Source.OperationID})
}

func (p ReleaseMergeEvidence) check(ctx context.Context, q operations.Queryer) error {
	a, b := p.Target, p.Source
	if a.ExternalID == "" || a.ExternalID != b.ExternalID || a.OperationID == "" || b.OperationID == "" || a.RecordingCount == 0 || b.RecordingCount == 0 || p.RecordingsOverlap {
		return operations.Conflict("no_strong_evidence", "双方需要有佐证的同一发行对应，且已对应的录音不相交")
	}
	var blocked bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_suppressions WHERE target_type='release' AND target_id=$1 AND subject='release_merge' AND value_hash=$2)`, min(a.ReleaseID, b.ReleaseID), hashExternalValue(max(a.ReleaseID, b.ReleaseID))).Scan(&blocked)
	if err != nil {
		return err
	}
	if blocked {
		return operations.Conflict("nothing_to_apply", "这两个发行已标记不再并入")
	}
	if a.group != b.group {
		var manual bool
		err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM (
			SELECT DISTINCT ON (target_id) origin FROM operations o
			WHERE operation_type='release_group_change' AND target_type='release' AND target_id IN ($1,$2)
			AND NOT EXISTS(SELECT 1 FROM operations undo WHERE undo.reverts_operation_id=o.id)
			ORDER BY target_id,created_at DESC,id DESC) latest
			WHERE (origin->>'source') IS DISTINCT FROM 'curation' OR origin->>'review_decision'='human')`, a.ReleaseID, b.ReleaseID).Scan(&manual)
		if err != nil {
			return err
		}
		if manual {
			return operations.Conflict("manual_structure", "最近的人工归组将两个发行分开")
		}
	}
	return nil
}

func skipMergePair(err error) bool {
	var f *operations.Failure
	if errors.As(err, &f) {
		switch f.Code {
		case "nothing_to_apply", "manual_structure", "no_strong_evidence", "release_merged", "merge_chain", "release_too_large":
			return true
		}
	}
	return false
}

func ReadReleaseMergeEvidence(ctx context.Context, q operations.Queryer, target, source string, exists func(string) bool, includeLayout bool) (ReleaseMergeEvidence, error) {
	if db, ok := q.(catalog.ReadTransactionBeginner); ok {
		return catalog.ReadWithLocalJITOff(ctx, db, func(tx *sql.Tx) (ReleaseMergeEvidence, error) {
			return ReadReleaseMergeEvidence(ctx, tx, target, source, exists, includeLayout)
		})
	}
	ids := []string{target, source}
	entries, err := catalog.LoadCurationEvidence(ctx, q, ids, exists)
	if err != nil {
		return ReleaseMergeEvidence{}, err
	}
	m, err := releaseMergeMembers(ctx, q, ids, entries)
	if err != nil {
		return ReleaseMergeEvidence{}, err
	}
	if _, ok := m[target]; !ok {
		return ReleaseMergeEvidence{}, operations.NotFound("not_found", "目标发行不可见")
	}
	if _, ok := m[source]; !ok {
		return ReleaseMergeEvidence{}, operations.NotFound("not_found", "来源发行不可见")
	}
	pair := mergePair(m[target], m[source])
	if includeLayout {
		plan, err := governance.NewStore(q).PreviewReleaseMerge(ctx, target, source)
		if err != nil {
			return ReleaseMergeEvidence{}, err
		}
		pair.mergeLayoutEvidence = buildMergeLayout(plan, entries)
	}
	return pair, nil
}

func buildMergeLayout(plan governance.ReleaseMergePreview, entries map[string]catalog.CurationEvidence) *mergeLayoutEvidence {
	total := len(entries[plan.Target.ID].Tracks) + len(entries[plan.Source.ID].Tracks)
	layout := &mergeLayoutEvidence{Layout: make([]mergeLayoutMedium, 0, len(plan.Media)), LayoutTrackTotal: total, TracksOmitted: total > mergeLayoutTrackLimit}
	type discKey struct {
		releaseID string
		position  int
	}
	tracks := make(map[discKey][]mergeLayoutTrack)
	if !layout.TracksOmitted {
		// 目录查询已按显示碟号、碟内曲序、曲目 ID 排序。
		for id, entry := range entries {
			for _, t := range entry.Tracks {
				key := discKey{id, t.Disc}
				tracks[key] = append(tracks[key], mergeLayoutTrack{t.Position, t.EffectiveTitle, t.ExternalID})
			}
		}
	}
	for _, m := range plan.Media {
		owner, from := plan.Target.ID, "target"
		if m.FromRelease == plan.Source.ID {
			owner, from = plan.Source.ID, "source"
		}
		disc := mergeLayoutMedium{Position: m.PositionAfter, From: from, FromReleaseID: m.FromRelease,
			OriginPosition: m.OriginPosition, Title: m.Title, TrackCount: m.TrackCount}
		if !layout.TracksOmitted {
			disc.Tracks = tracks[discKey{owner, m.PositionBefore}]
			if disc.Tracks == nil {
				disc.Tracks = []mergeLayoutTrack{}
			}
		}
		layout.Layout = append(layout.Layout, disc)
	}
	return layout
}
