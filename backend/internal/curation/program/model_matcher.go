package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type ModelMatcher struct {
	db      *sql.DB
	writer  *curation.Writer
	sources *curation.Sources
}

func NewModelMatcher(db *sql.DB, writer *curation.Writer, sources *curation.Sources) *ModelMatcher {
	return &ModelMatcher{db: db, writer: writer, sources: sources}
}

type MatchChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}
type MatchPlan struct {
	Changes []MatchChange `json:"changes"`
	Target  string        `json:"target"`
	write   matchWritePlan
	match   MatchResult
	item    curation.PendingItem
}
type ApplyOptions struct {
	// Finish 在匹配结论的同一事务记录人工采纳及其操作编号。
	Finish                                                 func(context.Context, *sql.Tx, []string) error
	Source                                                 curation.Source
	Key, Batch, ConfirmedBy, PendingItemID, ConversationID string
}
type MatchApplied struct {
	OperationID  string   `json:"operation_id,omitempty"`
	OperationIDs []string `json:"operation_ids"`
}

func staleItem() error { return operations.Conflict("item_stale", "待定项证据已变更") }
func (m *ModelMatcher) evidence(ctx context.Context, q operations.Queryer, id string) (catalog.CurationEvidence, error) {
	entries, err := catalog.LoadCurationEvidence(ctx, q, []string{id}, m.writer.ArtworkExists)
	if err != nil {
		return catalog.CurationEvidence{}, err
	}
	e, ok := entries[id]
	if !ok {
		return e, staleItem()
	}
	return e, nil
}

// AnnotatePendingEvidence 读取尚无真实性标记的旧项；只给完整指纹吻合的发行附上当前来源。
func (m *ModelMatcher) AnnotatePendingEvidence(ctx context.Context, items []curation.PendingItem) error {
	var ids, artistIDs []string
	var artistIndices, indices []int
	for i, item := range items {
		if item.Kind != "release_match" && item.Kind != "recording_match" && item.Kind != "artist_identity" {
			continue
		}
		var stored struct{ Authenticity json.RawMessage }
		if err := json.Unmarshal(item.Evidence, &stored); err != nil {
			return err
		}
		if stored.Authenticity == nil {
			if item.Kind == "artist_identity" {
				artistIDs = append(artistIDs, item.ArtistID)
				artistIndices = append(artistIndices, i)
				continue
			}
			ids = append(ids, item.TargetID)
			indices = append(indices, i)
		}
	}
	if len(ids)+len(artistIDs) == 0 {
		return nil
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(artistIDs) > 0 {
		page, err := artistMediaPage(ctx, tx, `SELECT unnest($1::uuid[])`, artistIDs)
		if err != nil {
			return err
		}
		byID := map[string]artistEvidence{}
		for _, e := range page {
			byID[e.ID] = e
		}
		for _, i := range artistIndices {
			item := &items[i]
			e, ok := byID[item.ArtistID]
			if !ok {
				continue
			}
			var stored map[string]json.RawMessage
			if err := json.Unmarshal(item.Evidence, &stored); err != nil {
				return err
			}
			if !sameArtistSamples(stored, e) {
				continue
			}
			stored["authenticity"] = jsonValue(e.sampleAuthenticity())
			item.Evidence = jsonValue(stored)
		}
	}
	if len(ids) == 0 {
		return tx.Commit()
	}
	entries, err := catalog.LoadCurationEvidence(ctx, tx, ids, m.writer.ArtworkExists)
	if err != nil {
		return err
	}
	states, err := curation.LoadWorkStates(ctx, tx, "release", ids)
	if err != nil {
		return err
	}
	for _, i := range indices {
		item := &items[i]
		e, ok := entries[item.TargetID]
		if !ok || ReleaseDigest(e) != item.InputDigest {
			continue
		}
		var matched struct{ Method string }
		state := states[item.TargetID]
		if state.InputDigest == item.InputDigest && len(state.Result) > 0 {
			if err := json.Unmarshal(state.Result, &matched); err != nil {
				return err
			}
		}
		item.Evidence = jsonValue(pendingMatchEvidence(e, matched.Method))
	}
	return tx.Commit()
}

func sameArtistSamples(stored map[string]json.RawMessage, current artistEvidence) bool {
	// JSONB 的空白与键顺序不代表样本变化。
	for name, now := range map[string]json.RawMessage{"albums": current.Albums, "tracks": current.Tracks, "names": jsonValue(current.Names)} {
		var before, after any
		if json.Unmarshal(stored[name], &before) != nil || json.Unmarshal(now, &after) != nil || !reflect.DeepEqual(before, after) {
			return false
		}
	}
	return true
}

func (m *ModelMatcher) Plan(ctx context.Context, item curation.PendingItem, externalID string) (MatchPlan, error) {
	if !uuid.Valid(externalID) {
		return MatchPlan{}, operations.Invalid("invalid_input", "MusicBrainz 编号无效")
	}
	externalID = strings.ToLower(externalID)
	e, err := m.evidence(ctx, m.db, item.TargetID)
	if err != nil {
		return MatchPlan{}, err
	}
	if ReleaseDigest(e) != item.InputDigest {
		return MatchPlan{}, staleItem()
	}
	match, err := m.readMatch(ctx, item.Kind, e, externalID)
	if err != nil {
		return MatchPlan{}, err
	}
	p, err := planMatch(ctx, m.db, e, match)
	if err != nil {
		return MatchPlan{}, err
	}
	plan := MatchPlan{write: p, match: match, item: item, Target: e.Title}
	before := e.ExternalID
	if match.Mode == "single" {
		before = e.Tracks[0].ExternalID
	}
	if conflicts := linkConflicts(e, "release", e.ID, e.ExternalID, externalID); match.Mode == "album" && len(conflicts) > 0 {
		return MatchPlan{}, matchConflict(conflicts[0])
	}
	linked := 0
	trackLinks := map[string]string{}
	for _, t := range e.Tracks {
		trackLinks[t.ID] = t.ExternalID
	}
	for _, pair := range match.Pairs {
		if pair.RecordingID == "" {
			return MatchPlan{}, matchConflict("position_conflict")
		}
		existing := trackLinks[pair.TrackID]
		if conflicts := linkConflicts(e, "track", pair.TrackID, existing, pair.RecordingID); len(conflicts) > 0 {
			return MatchPlan{}, matchConflict(conflicts[0])
		}
		if existing != "" {
			linked++
		}
	}
	if p.group.result == "conflict" && match.Release != nil && e.GroupExternalID != "" && e.GroupExternalID != match.Release.Group.ID {
		return MatchPlan{}, matchConflict("existing_link")
	}
	if p.group.result == "suppressed" {
		return MatchPlan{}, matchConflict("suppressed")
	}
	if !linkNeeded(e, p.links.Links) {
		return MatchPlan{}, operations.Conflict("nothing_to_apply", "已有相同的外部对应")
	}
	plan.Changes = []MatchChange{{"external_link", optionalText(before), externalID}, {"track_links", linked, len(match.Pairs)}}
	for _, field := range catalog.CorrectableFields {
		if intent, ok := p.fields[field.Name]; ok {
			var old any
			switch field.Name {
			case "title":
				old = e.Title
			case "album_artist":
				old = albumArtist(e)
			}
			plan.Changes = append(plan.Changes, MatchChange{field.Name, old, intent.Value})
		}
	}
	if p.group.link {
		plan.Changes = append(plan.Changes, MatchChange{"group_external_link", nil, match.Release.Group.ID})
	}
	if p.group.joinID != "" {
		plan.Changes = append(plan.Changes, MatchChange{"group", e.GroupID, p.group.joinID})
	}
	return plan, nil
}
func optionalText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func matchConflict(code string) error {
	return operations.Conflict(code, "所选外部资料与本地曲目或已有对应冲突")
}

func (m *ModelMatcher) readMatch(ctx context.Context, kind string, e catalog.CurationEvidence, id string) (MatchResult, error) {
	switch kind {
	case "recording_match":
		r := newMatch("single", "model")
		if len(e.Tracks) != 1 {
			return r, matchConflict("track_count")
		}
		finder, ok := m.sources.RecordingFinder("musicbrainz")
		if !ok {
			return r, operations.Conflict("source_disabled", "录音查询未启用")
		}
		rec, err := finder.RecordingByID(ctx, id)
		if err != nil {
			return r, err
		}
		if rec.ID != id {
			return r, matchConflict("external_id_mismatch")
		}
		r.Status, r.Recording, r.Pairs = "matched", &rec, []TrackPair{{e.Tracks[0].ID, rec.ID}}
		return r, nil
	case "release_match":
		r := newMatch("album", "model")
		finder, ok := m.sources.ReleaseFinder("musicbrainz")
		if !ok {
			return r, operations.Conflict("source_disabled", "发行查询未启用")
		}
		release, err := finder.ReleaseByID(ctx, id)
		if err != nil {
			return r, err
		}
		if release.ID != id {
			return r, matchConflict("external_id_mismatch")
		}
		if len(e.DiscPositions()) != len(release.Media) {
			return r, matchConflict("media_count_mismatch")
		}
		var c comparison
		c.compareTracks(e, "model", release)
		if slices.Contains(c.conflicts, "position_conflict") {
			return r, matchConflict("position_conflict")
		}
		if !c.countEqual || len(c.pairs) == 0 {
			return r, matchConflict("track_count")
		}
		r.Status, r.Release, r.Pairs = "matched", &release, c.pairs
		return r, nil
	default:
		return MatchResult{}, operations.Invalid("invalid_input", "待定项种类无效")
	}
}
func (m *ModelMatcher) Apply(ctx context.Context, plan MatchPlan, opt ApplyOptions) (MatchApplied, error) {
	p := plan.write
	if !uuid.Valid(opt.Key) || opt.Source.Kind != "model" || p.evidence.ID == "" || (opt.PendingItemID != "" && opt.PendingItemID != plan.item.ID) {
		return MatchApplied{}, operations.Invalid("invalid_input", "匹配写入参数无效")
	}
	conversation := opt.ConversationID
	if conversation == "" {
		conversation = plan.item.ConversationID
	}
	p.links.Source, p.links.ConversationID, p.links.ConfirmedBy = opt.Source, conversation, opt.ConfirmedBy
	// 目录锁内重读摘要；同键回放由操作日志先行处理。
	p.links.CheckEvidence = func(ctx context.Context, tx *sql.Tx) error {
		if err := curation.CheckMatchItem(ctx, tx, plan.item, conversation, opt.ConfirmedBy != ""); err != nil {
			return err
		}
		e, err := m.evidence(ctx, tx, p.evidence.ID)
		if err != nil {
			return err
		}
		if ReleaseDigest(e) != plan.item.InputDigest {
			if !allMatchLinks(e, p.links.Links) {
				return staleItem()
			}
			expected := p.evidence
			expected.ExternalID, expected.ExternalGroupID = e.ExternalID, e.ExternalGroupID
			if p.group.link {
				expected.GroupExternalID = e.GroupExternalID
			}
			expected.Tracks = slices.Clone(expected.Tracks)
			if len(expected.Tracks) != len(e.Tracks) {
				return staleItem()
			}
			for i := range expected.Tracks {
				expected.Tracks[i].ExternalID = e.Tracks[i].ExternalID
			}
			if ReleaseDigest(expected) != ReleaseDigest(e) {
				return staleItem()
			}
		}
		return nil
	}
	out := MatchApplied{OperationIDs: []string{}}
	write := func(ctx context.Context, step string, fn func(batch, key string) error) error {
		key := opt.Key
		if step != "links" {
			key = curation.DerivedKey(opt.Key, step)
		}
		err := fn(opt.Batch, key)
		if failureCode(err) == "nothing_to_apply" {
			return nil
		}
		return err
	}
	group, err := writeMatch(ctx, m.db, m.writer, p, write, &out.OperationIDs)
	if err != nil {
		return MatchApplied{}, err
	}
	if len(out.OperationIDs) > 0 {
		out.OperationID = out.OperationIDs[0]
	}
	tx, err := m.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return MatchApplied{}, err
	}
	defer tx.Rollback()
	fresh, err := m.evidence(ctx, tx, p.evidence.ID)
	if err != nil {
		return MatchApplied{}, err
	}
	conclusion := outcome{status: "matched", group: group, match: plan.match}
	digest := ReleaseDigest(fresh)
	if !allMatchLinks(fresh, p.links.Links) || ReleaseDigest(afterMatch(p.evidence, fresh, conclusion)) != digest {
		return MatchApplied{}, staleItem()
	}
	states, err := curation.LoadWorkStates(ctx, tx, "release", []string{fresh.ID})
	if err != nil {
		return MatchApplied{}, err
	}
	state := states[fresh.ID]
	if state.Status != "matched" || state.InputDigest != digest {
		state.Generation++
		state.Attempts++
	}
	state.Subject, state.TargetID, state.RuleVersion = "release", fresh.ID, MatchRuleVersion
	releaseInputs(fresh).record(&state)
	state.MatchRuleVersion, state.RequeryMatchVersion = MatchRuleVersion, 0
	state.Status, state.InputDigest, state.Result = "matched", digest, releaseResult(conclusion)
	state.RetryCount, state.NextAttemptAt = 0, nil
	if err := curation.SaveWorkState(ctx, tx, state); err != nil {
		return MatchApplied{}, err
	}
	if opt.ConfirmedBy == "" {
		if err := curation.ResolvePendingItem(ctx, tx, plan.item.ID, conversation, "model_matched"); err != nil {
			return MatchApplied{}, err
		}
	}
	if opt.Finish != nil {
		if err := opt.Finish(ctx, tx, out.OperationIDs); err != nil {
			return MatchApplied{}, err
		}
	}
	return out, tx.Commit()
}

func allMatchLinks(e catalog.CurationEvidence, links []curation.LinkIntent) bool {
	tracks := map[string]string{}
	for _, t := range e.Tracks {
		tracks[t.ID] = t.ExternalID
	}
	for _, l := range links {
		var current string
		switch l.ObjectType {
		case "release":
			current = e.ExternalID
		case "release_group":
			current = e.GroupExternalID
		case "track":
			current = tracks[l.ObjectID]
		}
		if current != l.ExternalID {
			return false
		}
	}
	return true
}

// 已采纳的匹配分多条受治理操作写入。快照只保存重放所需的候选、对应与可变证据，
// 曲库路径等未改动证据重读后用原 input_digest 核对。
type confirmedMatch struct {
	Match                                                                    MatchResult
	Links                                                                    []curation.LinkIntent
	External                                                                 curation.ExternalSummary
	GroupResult, JoinID                                                      string
	LinkGroup                                                                bool
	GroupID, GroupExternalID, GroupChangeSource, ExternalID, ExternalGroupID string
	TrackExternalIDs                                                         []string
	Automatic                                                                map[string]bool
}

func (p MatchPlan) ConfirmationSnapshot() ([]byte, error) {
	e := p.write.evidence
	s := confirmedMatch{Match: p.match, Links: p.write.links.Links, External: p.write.links.External,
		GroupResult: p.write.group.result, JoinID: p.write.group.joinID, LinkGroup: p.write.group.link,
		GroupID: e.GroupID, GroupExternalID: e.GroupExternalID, GroupChangeSource: e.GroupChangeSource,
		ExternalID: e.ExternalID, ExternalGroupID: e.ExternalGroupID, Automatic: e.Automatic}
	for _, track := range e.Tracks {
		s.TrackExternalIDs = append(s.TrackExternalIDs, track.ExternalID)
	}
	return json.Marshal(s)
}

// ResumeConfirmation 只用于首条操作已经按原采纳键落库之后；按原方案重放余下步骤。
func (m *ModelMatcher) ResumeConfirmation(ctx context.Context, item curation.PendingItem, raw []byte) (MatchPlan, error) {
	var s confirmedMatch
	if err := json.Unmarshal(raw, &s); err != nil {
		return MatchPlan{}, err
	}
	if err := m.sources.Check(ctx, "musicbrainz"); err != nil {
		return MatchPlan{}, err
	}
	fresh, err := m.evidence(ctx, m.db, item.TargetID)
	if err != nil {
		return MatchPlan{}, err
	}
	if len(fresh.Tracks) != len(s.TrackExternalIDs) || !allMatchLinks(fresh, s.Links) {
		return MatchPlan{}, staleItem()
	}
	before := fresh
	before.GroupID, before.GroupExternalID, before.GroupChangeSource = s.GroupID, s.GroupExternalID, s.GroupChangeSource
	before.ExternalID, before.ExternalGroupID, before.Automatic = s.ExternalID, s.ExternalGroupID, s.Automatic
	before.Tracks = slices.Clone(fresh.Tracks)
	for i, id := range s.TrackExternalIDs {
		before.Tracks[i].ExternalID = id
	}
	if ReleaseDigest(before) != item.InputDigest {
		return MatchPlan{}, staleItem()
	}
	group := s.GroupResult
	if s.LinkGroup {
		group = "linked"
	}
	if s.JoinID != "" && fresh.GroupID == s.JoinID {
		group = "joined"
	}
	if ReleaseDigest(afterMatch(before, fresh, outcome{match: s.Match, group: group})) != ReleaseDigest(fresh) {
		return MatchPlan{}, staleItem()
	}
	write := matchWritePlan{evidence: before, group: groupPlan{result: s.GroupResult, joinID: s.JoinID, link: s.LinkGroup},
		links: curation.LinkRequest{ReleaseID: item.TargetID, ExternalSource: "musicbrainz", Method: s.Match.Method, Confidence: "certain", External: s.External, Links: s.Links}}
	if s.Match.Release != nil {
		write.fields = missingFields(before, *s.Match.Release)
	}
	return MatchPlan{match: s.Match, write: write, item: item}, nil
}
