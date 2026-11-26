package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type ArtistIdentityPlan struct {
	Changes    []MatchChange
	preview    curation.ArtistIdentityPreview
	item       curation.PendingItem
	release    string
	result     MatchResult
	links      curation.LinkRequest
	fileDigest string
}

func (m *ModelMatcher) PlanArtistIdentity(ctx context.Context, item curation.PendingItem, source, externalID string) (ArtistIdentityPlan, error) {
	p, err := m.writer.PreviewArtistIdentity(ctx, m.sources, item, source, externalID)
	if err != nil {
		return ArtistIdentityPlan{}, err
	}
	plan := ArtistIdentityPlan{Changes: []MatchChange{{Field: "artist_link", After: p.Entry}}, preview: p, item: item}
	if item.ArtistWebGeneration == nil {
		return plan, nil
	}
	if err := curation.CheckArtistWebInput(ctx, m.db, item); err != nil {
		return plan, err
	}
	finder, ok := m.sources.ReleaseFinder("musicbrainz")
	if !ok {
		return plan, sources.ErrUnavailable
	}
	pages, ok := finder.(curation.ReleasePageFinder)
	if !ok {
		return plan, sources.ErrUnavailable
	}
	var facts struct {
		Albums []struct {
			ID string `json:"id"`
		} `json:"albums"`
	}
	if err = json.Unmarshal(item.Evidence, &facts); err != nil {
		return plan, err
	}
	for _, album := range facts.Albums {
		e, err := m.evidence(ctx, m.db, album.ID)
		if err != nil {
			return plan, err
		}
		page, err := found(pages.SearchReleasePage(ctx, curation.ReleaseSearch{Title: e.Title, ArtistID: externalID, TrackCount: len(e.Tracks)}))
		if err != nil {
			return plan, err
		}
		complete := page.Complete
		var editions []sources.Release
		for _, r := range uniqueReleases(page.Items) {
			if knownWrongShape(e, r) {
				continue
			}
			if !r.Complete {
				r, err = found(finder.ReleaseByID(ctx, r.ID))
				if err != nil {
					return plan, err
				}
			}
			if !r.Complete {
				complete = false
				continue
			}
			if slices.ContainsFunc(r.Artists, func(credit sources.Credit) bool { return credit.ArtistID == externalID }) {
				editions = append(editions, r)
			}
		}
		// 每版完整发行的署名均已核对候选 MBID，可用于短专辑的艺人判定。
		result := compareAlbumEditions(e, "search", editions, MatchSnapshot{}, true)
		if result.Status != "matched" && !slices.Contains(result.Conflicts, "different_release_groups") {
			continue
		}
		if !complete && result.Status == "matched" {
			result = uncertain(result, "list_truncated")
		}
		plan.release, plan.result, plan.fileDigest = e.ID, result, e.FileDigest
		if result.Status == "matched" {
			write, err := planMatch(ctx, m.db, e, result)
			if err != nil {
				return plan, err
			}
			plan.links = write.links
		}
		break
	}
	return plan, nil
}
func (p ArtistIdentityPlan) StructureMatched() bool { return p.release != "" }

func (m *ModelMatcher) ApplyArtistIdentity(ctx context.Context, plan ArtistIdentityPlan, opt ApplyOptions) (curation.ArtistLinkApplied, error) {
	finish := opt.Finish
	if plan.StructureMatched() {
		finish = func(ctx context.Context, tx *sql.Tx, ids []string) error {
			fresh, err := m.evidence(ctx, tx, plan.release)
			if err != nil {
				return err
			}
			if fresh.FileDigest != plan.fileDigest {
				return staleItem()
			}
			status := "conflict"
			if plan.result.Status == "matched" {
				links := plan.links
				links.Source, links.Method, links.ConversationID, links.ConfirmedBy = opt.Source, "album_structure", opt.ConversationID, opt.ConfirmedBy
				links.Key, links.BatchID = curation.DerivedKey(opt.Key, "release"), opt.Batch
				links.CheckEvidence = func(ctx context.Context, tx *sql.Tx) error {
					e, err := m.evidence(ctx, tx, plan.release)
					if err != nil {
						return err
					}
					if e.FileDigest != plan.fileDigest {
						return staleItem()
					}
					return nil
				}
				applied, err := m.writer.ApplyArtistReleaseLinks(ctx, tx, plan.item, links)
				if err != nil && failureCode(err) != "nothing_to_apply" {
					return err
				}
				if applied.OperationID != "" {
					ids = append(ids, applied.OperationID)
				}
				status = "matched"
				if err := curation.ResolvePendingItems(ctx, tx, plan.release, "release_match", "program_matched"); err != nil {
					return err
				}
			}
			now := time.Now()
			if err := curation.SaveReleaseMatchResult(ctx, tx, plan.release, curation.ReleaseMatchResult{Source: "musicbrainz", Status: status, CheckedAt: &now, Conflicts: plan.result.Conflicts}); err != nil {
				return err
			}
			if opt.Finish != nil {
				return opt.Finish(ctx, tx, ids)
			}
			return curation.ResolvePendingItem(ctx, tx, plan.item.ID, opt.ConversationID, "model_linked")
		}
	} else if opt.ConfirmedBy == "" && plan.item.ArtistWebGeneration != nil {
		return curation.ArtistLinkApplied{}, operations.Conflict("no_evidence", "艺人候选需要确认")
	}
	return m.writer.ApplyArtistIdentity(ctx, plan.preview, curation.ArtistLinkRequest{
		Source: opt.Source, Key: opt.Key, BatchID: opt.Batch, ConfirmedBy: opt.ConfirmedBy, ConversationID: opt.ConversationID,
	}, finish)
}
