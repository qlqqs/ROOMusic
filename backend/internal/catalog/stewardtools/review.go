package stewardtools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
)

// ReviewTarget 是独立审查允许读取的目标与安全摘要，所有文本均置于来源明确的数据字段。
type ReviewTarget struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Source   string `json:"source"`
	Data     any    `json:"data"`
}
type reviewField struct {
	Field          string `json:"field"`
	Source         string `json:"source"`
	Confidence     string `json:"confidence"`
	CandidateCount int    `json:"candidate_count"`
	Consistent     bool   `json:"consistent"`
}
type reviewRelease struct {
	Current    []reviewCurrentField `json:"current"`
	GroupID    string               `json:"group_id,omitempty"`
	Fields     []reviewField        `json:"fields"`
	OpenIssues map[string]int64     `json:"open_issues"`
	Truncated  bool                 `json:"truncated"`
}
type reviewCurrentField struct {
	Field  string `json:"field"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

// SupportsReviewEvidence 只登记已有明确目标读取合同的工具。
func SupportsReviewEvidence(name string) bool {
	switch name {
	case "correct_track", "correct_release", "change_release_group", "set_default_release", "review_release_issue", "claim_release":
		return true
	default:
		return false
	}
}

// ReadReviewTargets 只调用现有公开读取方法；不返回原始标签候选、路径、歌词或核对人。
func ReadReviewTargets(ctx context.Context, store *catalog.Store, issues *governance.Store, raw json.RawMessage) ([]ReviewTarget, error) {
	var p struct {
		TrackID         string `json:"track_id"`
		ReleaseID       string `json:"release_id"`
		SourceReleaseID string `json:"source_release_id"`
		GroupID         string `json:"group_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	targets := []ReviewTarget{}
	seen := map[string]bool{}
	for _, id := range []string{p.ReleaseID, p.SourceReleaseID} {
		id = strings.ToLower(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if id == strings.ToLower(p.SourceReleaseID) {
			claim, err := issues.LoadClaim(ctx, id)
			if err != nil {
				return nil, err
			}
			fields := []reviewCurrentField{{Field: "title", Value: claim.Release.Title, Source: "catalog_effective_untrusted"}}
			for _, correction := range claim.Corrections {
				fields = append(fields, reviewCurrentField{Field: correction.Field, Value: correction.Correction, Source: "catalog_correction_untrusted"})
			}
			groupID := ""
			if claim.Album != nil {
				groupID = claim.Album.ID
			}
			data := reviewRelease{Current: fields, GroupID: groupID, Fields: []reviewField{}, OpenIssues: map[string]int64{"unavailable": 1}}
			targets = append(targets, ReviewTarget{Type: "release", ID: id, Revision: claim.Revision, Source: "catalog_read_summary", Data: data})
			continue
		}
		state, err := issues.LoadCorrectionState(ctx, id)
		if err != nil {
			return nil, err
		}
		evidence, err := store.LoadReleaseEvidence(ctx, id)
		if err != nil {
			return nil, err
		}
		data := reviewRelease{Current: []reviewCurrentField{}, Fields: []reviewField{}, OpenIssues: map[string]int64{}, Truncated: evidence.Truncated}
		for _, field := range state.Fields {
			data.Current = append(data.Current, reviewCurrentField{field.Field, field.EffectiveValue, "catalog_effective_untrusted"})
		}
		for _, field := range evidence.Fields {
			candidates := map[string]bool{}
			for _, value := range field.Candidates {
				candidates[value] = true
			}
			data.Fields = append(data.Fields, reviewField{field.Field, field.Source, field.Confidence, len(candidates), len(candidates) == 1})
		}
		current, err := issues.LoadReleaseIssues(ctx, id)
		if err != nil {
			return nil, err
		}
		data.GroupID = current.GroupID
		for _, field := range current.Fields {
			if field.Status == governance.IssueStatusOpen {
				data.OpenIssues["fields"]++
			}
		}
		if current.Grouping != nil && current.Grouping.Status == governance.IssueStatusOpen {
			data.OpenIssues["grouping"]++
		}
		if current.MissingTracks != nil && current.MissingTracks.Status == governance.IssueStatusOpen {
			data.OpenIssues["missing_tracks"] = current.MissingTracks.Count
		}
		targets = append(targets, ReviewTarget{Type: "release", ID: id, Revision: state.Revision, Source: "catalog_read_summary", Data: data})
	}
	if p.TrackID != "" {
		state, err := issues.LoadTrackCorrectionState(ctx, p.TrackID)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(p.ReleaseID, state.ReleaseID) {
			return nil, catalog.ErrTrackNotFound
		}
		targets = append(targets, ReviewTarget{Type: "track", ID: state.TrackID, Revision: state.Revision, Source: "catalog_read_summary", Data: map[string]any{"release_id": state.ReleaseID, "fields": state.Fields, "source": "catalog_effective_untrusted"}})
	}
	if p.GroupID != "" {
		group, err := store.LoadReleaseGroup(ctx, strings.ToLower(p.GroupID))
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(group.Releases))
		for _, release := range group.Releases {
			ids = append(ids, release.ID)
		}
		targets = append(targets, ReviewTarget{Type: "release_group", ID: group.ID, Revision: group.Revision, Source: "catalog_read_summary", Data: struct {
			DefaultReleaseID *string  `json:"default_release_id"`
			ReleaseIDs       []string `json:"release_ids"`
			ReleaseCount     int64    `json:"release_count"`
			AttentionCount   int64    `json:"attention_count"`
		}{group.DefaultReleaseID, ids, group.ReleaseCount, group.AttentionCount}})
	}
	return targets, nil
}
