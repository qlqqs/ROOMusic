package stewardtools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (a adapter) preview(ctx context.Context, name string, raw json.RawMessage) (tool.Preview, error) {
	var p input
	_ = json.Unmarshal(raw, &p)
	p.ReleaseID = strings.ToLower(p.ReleaseID)
	p.GroupID = strings.ToLower(p.GroupID)
	p.ArtistID = strings.ToLower(p.ArtistID)
	result := tool.Preview{Summary: "修改曲库资料", Changes: []tool.Change{}}
	add := func(field string, before, after any) {
		result.Changes = append(result.Changes, tool.Change{Field: field, Before: before, After: after})
	}
	switch name {
	case "correct_track":
		state, err := a.reads.LoadTrackCorrectionState(ctx, p.TrackID)
		if errors.Is(err, catalog.ErrTrackNotFound) || err == nil && state.ReleaseID != p.ReleaseID {
			return result, operations.NotFound("not_found", "曲目不存在")
		}
		if err != nil {
			return result, err
		}
		changes, err := governance.ParseTrackCorrectionChanges(p.Changes)
		if err != nil {
			return result, err
		}
		for _, field := range state.Fields {
			if field.Field == "title" {
				result.Target = field.EffectiveValue.(string)
			}
			change, ok := changes[field.Field]
			if !ok {
				continue
			}
			var after any = change.Text
			if change.Mode == catalog.CorrectionClear {
				after = field.ValueAfterClear()
			}
			add(field.Field, field.EffectiveValue, after)
		}
	case "correct_release":
		state, err := a.reads.LoadCorrectionState(ctx, p.ReleaseID)
		if err != nil {
			return result, err
		}
		changes, err := governance.ParseCorrectionChanges(p.Changes)
		if err != nil {
			return result, err
		}
		for _, field := range state.Fields {
			if field.Field == "title" {
				result.Target, _ = field.EffectiveValue.(string)
			}
			c, ok := changes[field.Field]
			if !ok {
				continue
			}
			var after any
			switch c.Mode {
			case catalog.CorrectionClear:
				after = field.ValueAfterClear()
			case catalog.CorrectionSet:
				switch field.Field {
				case "year":
					after = c.Year
				case "genre":
					after = c.Genres
				default:
					after = c.Text
				}
			}
			add(field.Field, field.EffectiveValue, after)
		}
	case "set_default_release":
		group, err := a.catalog.LoadReleaseGroup(ctx, p.GroupID)
		if err != nil {
			return result, err
		}
		result.Target = group.Representative.Title
		var next any
		if p.ReleaseID != "" {
			next = p.ReleaseID
		}
		add("default_release_id", group.DefaultReleaseID, next)
	case "claim_release":
		claim, err := a.reads.PreviewClaim(ctx, strings.ToLower(p.SourceReleaseID), strings.ToLower(p.ReleaseID))
		if err != nil {
			return result, err
		}
		result.Target = claim.Release.Title
		for _, item := range claim.Items {
			field := item.Kind
			if item.Field != "" {
				field += "." + item.Field
			}
			if item.Issue != "" {
				field += "." + item.Issue
			}
			after := item.To
			if item.Action == governance.ClaimActionMove {
				after = item.From
			}
			if item.Action == governance.ClaimActionReset {
				after = nil
			}
			add(field, item.To, after)
		}
	case "merge_releases":
		merge, err := a.reads.PreviewReleaseMerge(ctx, strings.ToLower(p.TargetReleaseID), strings.ToLower(p.SourceReleaseID))
		if err != nil {
			return result, err
		}
		if merge.Token != p.Token {
			return result, operations.Conflict("merge_changed", "合并预览已变化，请重新预览")
		}
		result.Target = merge.Target.Title
		add("source", merge.Source, merge.Target)
		add("media", nil, merge.Media)
		add("hidden", nil, merge.Hidden)
		add("favorites_moved", 0, merge.FavoritesMoved)
	case "change_release_group", "review_release_issue":
		issues, err := a.reads.LoadReleaseIssues(ctx, p.ReleaseID)
		if err != nil {
			return result, err
		}
		result.Target = issues.Release.Title
		if name == "change_release_group" {
			var target any = "独立专辑"
			if p.Action == "join" {
				group, err := a.catalog.LoadReleaseGroup(ctx, p.GroupID)
				if err != nil {
					return result, err
				}
				target = map[string]any{"id": p.GroupID, "title": group.Representative.Title}
			}
			add("group", issues.GroupID, target)
		} else {
			before := "未核对"
			for _, field := range issues.Fields {
				if field.Field == p.Issue && field.Review != nil {
					before = "已核对"
				}
			}
			if p.Issue == governance.IssueGrouping && issues.Grouping != nil && issues.Grouping.Review != nil {
				before = "已核对"
			}
			if p.Issue == governance.IssueMissingTracks && issues.MissingTracks != nil && issues.MissingTracks.Review != nil {
				before = "已核对"
			}
			after := "未核对"
			if p.Action == "review" {
				after = "已核对"
			}
			add(p.Issue, before, after)
		}
	default:
		artist, err := a.catalog.LoadArtist(ctx, p.ArtistID)
		if err != nil {
			return result, err
		}
		result.Target = artist.Artist.Name
		switch name {
		case "rename_artist":
			var c governance.ArtistRenameRequest
			_ = json.Unmarshal(raw, &c)
			before, after, err := a.reads.ArtistNamingPreview(ctx, p.ArtistID)
			if err != nil {
				return result, err
			}
			if c.Name != nil {
				after, err = governance.NormalizeArtistName(*c.Name)
				if err != nil {
					return result, err
				}
			}
			add("name", before, after)
		case "merge_artists":
			var c governance.ArtistMergeRequest
			_ = json.Unmarshal(raw, &c)
			source, err := a.catalog.LoadArtist(ctx, strings.ToLower(c.SourceArtistID))
			if err != nil {
				return result, err
			}
			add("artist", source.Artist.Name, artist.Artist.Name)
		case "split_artist":
			var c governance.ArtistSplitRequest
			_ = json.Unmarshal(raw, &c)
			c.ArtistID = p.ArtistID
			c.Basis = p.BasisToken
			if err := c.Normalize(); err != nil {
				return result, err
			}
			split, err := governance.PreviewArtistSplit(ctx, a.db, p.ArtistID, c.ArtistSplitScope)
			if err != nil {
				return result, err
			}
			target := "新艺人"
			if c.Target.DisplayName != nil {
				target = *c.Target.DisplayName
			}
			if c.Target.Kind == "restore" {
				restored, err := a.catalog.LoadArtist(ctx, strings.ToLower(c.Target.ArtistID))
				if err != nil {
					return result, err
				}
				target = restored.Artist.Name
			}
			add("artist", map[string]any{"name": artist.Artist.Name, "scope": split.Scope}, target)
		case "ignore_artist_candidate":
			var c governance.ArtistCandidateIgnoreRequest
			_ = json.Unmarshal(raw, &c)
			c.ArtistID = p.ArtistID
			if err := c.Normalize(); err != nil {
				return result, err
			}
			before, err := a.reads.CandidateIgnorePreview(ctx, c.KeyPairs)
			if err != nil {
				return result, err
			}
			for i, pair := range c.KeyPairs {
				add("ignored:"+pair.LeftKeyID+":"+pair.RightKeyID, before[i], *c.Ignored)
			}
		}
	}
	result.Summary = map[string]string{"correct_track": "修正曲目信息", "correct_release": "修正元数据", "change_release_group": "调整版本归组", "set_default_release": "设置默认版本", "review_release_issue": "更新问题核对", "claim_release": "认领旧版本修改", "merge_releases": "合并发行", "merge_artists": "合并艺人", "split_artist": "拆分艺人", "rename_artist": "修改艺人主名", "ignore_artist_candidate": "更新候选提示"}[name] + "：" + result.Target
	return result, nil
}
