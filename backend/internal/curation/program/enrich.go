package program

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

var methodNames = map[string]string{
	"tag_id": "标签标识", "barcode": "条码", "catalog_number": "唱片编号", "isrc": "ISRC", "disc_id": "光盘目录", "acoustid": "声学指纹",
}

func hasMarker(markers []catalog.CurationSuppression, objectType, objectID, subject, raw string) bool {
	if len(markers) == 0 {
		return false
	}
	hash := sha256.Sum256([]byte(raw))
	valueHash := hex.EncodeToString(hash[:])
	for _, marker := range markers {
		if marker.TargetType == objectType && marker.TargetID == objectID && marker.Subject == subject && marker.ValueHash == valueHash {
			return true
		}
	}
	return false
}

// 操作记录的展示字段按 UTF-8 字节限制。待定项里的外部原文另存。
func summaryText(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	if len(value) <= 500 {
		return value
	}
	end := 500
	for !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func externalLabel(labels []sources.Label, number bool) string {
	var only string
	for _, label := range labels {
		value := strings.TrimSpace(label.Name)
		if number {
			value = strings.TrimSpace(label.CatalogNumber)
		}
		if value == "" {
			continue
		}
		if only != "" && only != value {
			return ""
		}
		only = value
	}
	if (!number && strings.EqualFold(only, "[no label]")) || (number && strings.EqualFold(only, "[none]")) {
		return ""
	}
	return only
}

func missingFields(e catalog.CurationEvidence, m sources.Release) map[string]catalog.CorrectionIntent {
	fields := map[string]catalog.CorrectionIntent{}
	vacant := func(name string) bool { return !e.Corrected[name] && !e.Automatic[name] }
	add := func(name, value string, gap bool) {
		value = metadata.NormalizeValue(value)
		if gap && vacant(name) && value != "" && len(value) <= metadata.MaxTextBytes && strings.IndexFunc(value, unicode.IsControl) < 0 {
			fields[name] = catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: value}
		}
	}
	add("title", m.Title, e.TitlePlaceholder)
	add("album_artist", credits(m.Artists), e.AlbumArtist == "")
	add("label", externalLabel(m.Labels, false), e.Label == "")
	add("catalog_number", externalLabel(m.Labels, true), e.CatalogNumber == "" && !e.CatalogNumberIsMCN)
	add("barcode", m.Barcode, len(e.Barcodes) == 0)
	if e.Year == 0 && vacant("year") && len(m.Date) >= 4 {
		y, err := strconv.ParseUint(m.Date[:4], 10, 16)
		if err == nil && y > 0 {
			fields["year"] = catalog.CorrectionIntent{Mode: catalog.CorrectionSet, Value: int(y)}
		}
	}
	for name, intent := range fields {
		// 写入口根据规范化后的意图做最终判断；已读到的标记无需打开批次。
		raw, _ := json.Marshal(intent)
		if hasMarker(e.Suppressions, "release", e.ID, name, string(raw)) {
			delete(fields, name)
		}
	}
	return fields
}

type groupPlan struct {
	result, joinID string
	link           bool
}

func planGroup(ctx context.Context, db operations.Queryer, e catalog.CurationEvidence, externalID string) (groupPlan, error) {
	if externalID == "" {
		return groupPlan{}, nil
	}
	if e.GroupChangeSource != "" && e.GroupChangeSource != "curation" {
		return groupPlan{result: "manual"}, nil
	}
	groups, err := curation.ObjectsLinkedTo(ctx, db, "musicbrainz", "release_group", externalID)
	if err != nil {
		return groupPlan{}, err
	}
	if slices.Contains(groups, e.GroupID) {
		return groupPlan{result: "linked"}, nil
	}
	visible, err := catalog.VisibleCurationGroups(ctx, db, groups)
	if err != nil {
		return groupPlan{}, err
	}
	switch len(visible) {
	case 1:
		if hasMarker(e.Suppressions, "release", e.ID, "group_join", visible[0]) {
			return groupPlan{result: "suppressed"}, nil
		}
		return groupPlan{joinID: visible[0]}, nil
	case 0:
		// 这一页前面的发行可能刚把本组对应到了另一个外部专辑。
		links, err := curation.LinksOf(ctx, db, "release_group", []string{e.GroupID})
		if err != nil {
			return groupPlan{}, err
		}
		for _, link := range links {
			if link.Source == "musicbrainz" {
				return groupPlan{result: "conflict"}, nil
			}
		}
		agree, err := catalog.CurationGroupMembersAgree(ctx, db, e.GroupID, e.ID, externalID)
		if err != nil {
			return groupPlan{}, err
		}
		if agree {
			if hasMarker(e.Suppressions, "release_group", e.GroupID, "external_link", "musicbrainz\x00"+externalID) {
				return groupPlan{result: "suppressed"}, nil
			}
			return groupPlan{link: true}, nil
		}
	}
	return groupPlan{result: "conflict"}, nil
}

func linkNeeded(e catalog.CurationEvidence, links []curation.LinkIntent) bool {
	linkedTracks := make(map[string]string, len(e.Tracks))
	for _, track := range e.Tracks {
		linkedTracks[track.ID] = track.ExternalID
	}
	for _, link := range links {
		var occupied bool
		switch link.ObjectType {
		case "release":
			occupied = e.ExternalID != ""
		case "track":
			occupied = linkedTracks[link.ObjectID] != ""
		case "release_group":
			// planGroup 已经核对了本组的最新对应。
			return true
		}
		if !occupied && !hasMarker(e.Suppressions, link.ObjectType, link.ObjectID, "external_link", "musicbrainz\x00"+link.ExternalID) {
			return true
		}
	}
	return false
}

func (w *Worker) applyMatch(ctx context.Context, u *unit, e catalog.CurationEvidence, m MatchResult) (group, artistStatus string, err error) {
	p, err := planMatch(ctx, w.db, e, m)
	if err != nil {
		return "", "", err
	}
	p.tracks = planTrackFields(e, m)
	local, err := catalog.LoadCurationArtistCredits(ctx, w.db, e.ID)
	switch {
	case err == nil:
		p.artists = planArtistLinks(local, e, m)
		artistStatus = "done"
	case !errors.Is(err, catalog.ErrArtistCatalogPreparing):
		return "", "", err
	}
	group, err = writeMatch(ctx, w.db, w.writer, p, u.write, nil)
	return group, artistStatus, err
}

type matchWritePlan struct {
	artists  []curation.LinkIntent
	tracks   []curation.TrackRequest
	evidence catalog.CurationEvidence
	group    groupPlan
	links    curation.LinkRequest
	fields   map[string]catalog.CorrectionIntent
}

func planMatch(ctx context.Context, db operations.Queryer, e catalog.CurationEvidence, m MatchResult) (matchWritePlan, error) {
	var plan groupPlan
	request := curation.LinkRequest{ExternalSource: "musicbrainz"}
	var fields map[string]catalog.CorrectionIntent
	request.ReleaseID, request.Method, request.Confidence = e.ID, m.Method, "certain"
	var basis string
	if r := m.Release; r != nil {
		var err error
		plan, err = planGroup(ctx, db, e, r.Group.ID)
		if err != nil {
			return matchWritePlan{}, err
		}
		request.External = curation.ExternalSummary{Title: summaryText(r.Title), Artist: summaryText(credits(r.Artists)), Date: summaryText(r.Date), Country: summaryText(r.Country)}
		request.External.DiscCount = len(r.Media)
		for _, disc := range r.Media {
			request.External.TrackCount += max(disc.TrackCount, len(disc.Tracks))
		}
		basis = fmt.Sprintf("MusicBrainz 发行 %s，依据%s", r.ID, methodNames[m.Method])
		request.Links = append(request.Links, curation.LinkIntent{ObjectType: "release", ObjectID: e.ID, ExternalID: r.ID, ExternalGroupID: r.Group.ID})
		if plan.link {
			request.Links = append(request.Links, curation.LinkIntent{ObjectType: "release_group", ObjectID: e.GroupID, ExternalID: r.Group.ID})
		}
		fields = missingFields(e, *r)
	} else {
		r := m.Recording
		request.External = curation.ExternalSummary{Title: summaryText(r.Title), Artist: summaryText(credits(r.Artists))}
		basis = fmt.Sprintf("MusicBrainz 录音 %s，依据%s", r.ID, methodNames[m.Method])
	}
	request.Source = curation.Source{Kind: "source", Provider: "musicbrainz", Basis: basis}
	for _, pair := range m.Pairs {
		if pair.RecordingID != "" {
			request.Links = append(request.Links, curation.LinkIntent{ObjectType: "track", ObjectID: pair.TrackID, ExternalID: pair.RecordingID})
		}
	}
	return matchWritePlan{evidence: e, group: plan, links: request, fields: fields}, nil
}

type matchStep func(context.Context, string, func(batch, key string) error) error

func writeMatch(ctx context.Context, db operations.Queryer, writer *curation.Writer, p matchWritePlan, write matchStep, opIDs *[]string) (string, error) {
	e, plan, request, fields := p.evidence, p.group, p.links, p.fields
	record := func(id string) {
		if opIDs != nil && id != "" {
			*opIDs = append(*opIDs, id)
		}
	}
	var applied curation.LinkApplied
	if linkNeeded(e, request.Links) {
		err := write(ctx, "links", func(batch, key string) error {
			request.BatchID, request.Key = batch, key
			var err error
			applied, err = writer.ApplyLinks(ctx, request)
			record(applied.OperationID)
			return err
		})
		if err != nil {
			return "", err
		}
	}
	if plan.link {
		switch applied.Skipped["release_group:"+e.GroupID] {
		case "suppressed":
			plan.result = "suppressed"
		case "occupied":
			plan.result = "conflict"
		default:
			plan.result = "linked"
		}
	}
	if err := writeArtistLinks(ctx, db, writer, request, p.artists, write); err != nil {
		return "", err
	}
	if len(fields) > 0 {
		err := write(ctx, "fields", func(batch, key string) error {
			result, err := writer.Apply(ctx, curation.Request{OnlyEmpty: true, ConfirmedBy: request.ConfirmedBy, ConversationID: request.ConversationID, ReleaseID: e.ID, Fields: fields, Source: request.Source, BatchID: batch, Key: key})
			record(result.OperationID)
			return err
		})
		if err != nil {
			return "", err
		}
	}
	if err := writeTrackFields(ctx, writer, p.tracks, request.Source, write); err != nil {
		return "", err
	}
	group, err := writeMatchGroup(ctx, writer, e, plan, request, write, record)
	if err != nil {
		return "", err
	}
	for _, link := range request.Links {
		if link.ObjectType == "release" {
			return group, writer.DiscoverReleaseMerges(ctx, link.ExternalID, StoredRuleVersion)
		}
	}
	return group, nil
}

func writeMatchGroup(ctx context.Context, writer *curation.Writer, e catalog.CurationEvidence, plan groupPlan, request curation.LinkRequest, write matchStep, record func(string)) (string, error) {
	if plan.joinID == "" {
		return plan.result, nil
	}
	group := "joined"
	err := write(ctx, "group", func(batch, key string) error {
		result, err := writer.JoinGroup(ctx, curation.JoinRequest{ExternalSource: request.ExternalSource, ConfirmedBy: request.ConfirmedBy, ConversationID: request.ConversationID, ReleaseID: e.ID, GroupID: plan.joinID, Source: request.Source, BatchID: batch, Key: key})
		record(result.OperationID)
		var failure *operations.Failure
		if errors.As(err, &failure) && failure.Code == "nothing_to_apply" {
			group = "suppressed"
		}
		return err
	})
	var failure *operations.Failure
	if errors.As(err, &failure) {
		switch failure.Code {
		case "manual_structure":
			return "manual", nil
		case "same_group":
			return "linked", nil
		case "no_strong_evidence", "group_not_found":
			return "conflict", nil
		}
	}
	return group, err
}
