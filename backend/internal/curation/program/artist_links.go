package program

import (
	"context"
	"maps"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// alignCredits 以整组为单位核对按位置排列的署名。只有等长的标签组可为异名提供佐证。
func alignCredits(local []catalog.CurationArtistCredit, external []sources.Credit, tags []string) []curation.LinkIntent {
	if len(local) == 0 || len(local) != len(external) {
		return nil
	}
	seenLocal, seenExternal := map[string]bool{}, map[string]bool{}
	links := make([]curation.LinkIntent, 0, len(local))
	for i, c := range local {
		x := external[i]
		if c.Position != i+1 || c.ArtistID == "" || x.ArtistID == "" || seenLocal[c.ArtistID] || seenExternal[x.ArtistID] || curation.MusicBrainzSpecialArtist(x.ArtistID) {
			return nil
		}
		name := textnorm.Text(c.Name)
		if (name == "" || name != textnorm.Text(x.Name)) && (len(tags) != len(external) || tags[i] != x.ArtistID) {
			return nil
		}
		seenLocal[c.ArtistID], seenExternal[x.ArtistID] = true, true
		links = append(links, curation.LinkIntent{ObjectType: "artist", ObjectID: c.ArtistID, ExternalID: x.ArtistID})
	}
	return links
}

func albumArtistTags(tracks []catalog.CurationTrack) []string {
	if len(tracks) == 0 {
		return nil
	}
	tags := tracks[0].Identifiers["mb_album_artist"]
	for _, t := range tracks[1:] {
		if !slices.Equal(tags, t.Identifiers["mb_album_artist"]) {
			return nil
		}
	}
	return tags
}

func planArtistLinks(local catalog.CurationArtistCredits, e catalog.CurationEvidence, m MatchResult) []curation.LinkIntent {
	byArtist := map[string]string{}
	add := func(group []catalog.CurationArtistCredit, ext []sources.Credit, tags []string) {
		for _, l := range alignCredits(group, ext, tags) {
			if prev, ok := byArtist[l.ObjectID]; ok && prev != l.ExternalID {
				byArtist[l.ObjectID] = ""
			} else {
				byArtist[l.ObjectID] = l.ExternalID
			}
		}
	}
	recordings := map[string][]sources.Credit{}
	if r := m.Release; r != nil {
		tags := albumArtistTags(e.Tracks)
		add(local.ReleaseArtists, r.Artists, tags)
		add(local.AlbumArtists, r.Artists, tags)
		for _, medium := range r.Media {
			for _, t := range medium.Tracks {
				if _, duplicate := recordings[t.RecordingID]; duplicate {
					recordings[t.RecordingID] = nil
				} else {
					recordings[t.RecordingID] = t.Artists
				}
			}
		}
	} else if r := m.Recording; r != nil {
		recordings[r.ID] = r.Artists
	}
	pairs := map[string]string{}
	for _, p := range m.Pairs {
		pairs[p.TrackID] = p.RecordingID
	}
	for _, t := range e.Tracks {
		if id := pairs[t.ID]; id != "" {
			add(local.Tracks[t.ID], recordings[id], t.Identifiers["mb_artist"])
		}
	}
	var links []curation.LinkIntent
	for _, id := range slices.Sorted(maps.Keys(byArtist)) {
		if x := byArtist[id]; x != "" {
			links = append(links, curation.LinkIntent{ObjectType: "artist", ObjectID: id, ExternalID: x})
		}
	}
	return links
}

func writeArtistLinks(ctx context.Context, db operations.Queryer, writer *curation.Writer, request curation.LinkRequest, links []curation.LinkIntent, write matchStep) error {
	if len(links) == 0 || request.Source.Kind != "source" {
		return nil
	}
	request.Links, request.Method, request.Confidence = links, "artist_credit", "certain"
	err := write(ctx, "artist_links", func(batch, key string) error {
		request.BatchID, request.Key = batch, key
		_, err := writer.ApplyLinks(ctx, request)
		return err
	})
	if err != nil {
		return err
	}
	externals := map[string]bool{}
	for _, link := range links {
		externals[link.ExternalID] = true
	}
	for _, x := range slices.Sorted(maps.Keys(externals)) {
		ids, err := curation.ObjectsLinkedTo(ctx, db, "musicbrainz", "artist", x)
		if err != nil {
			return err
		}
		if len(ids) < 2 {
			continue
		}
		artists, err := catalog.CurationCanonicalArtists(ctx, db, ids)
		if err != nil {
			return err
		}
		if err := mergeLinkedArtists(ctx, writer, request.Source, x, artists, write); err != nil {
			return err
		}
	}
	return nil
}

func mergeLinkedArtists(ctx context.Context, writer *curation.Writer, source curation.Source, x string, artists []string, write matchStep) error {
	// 保留尚未合并的各方。某一对已有分开决定时，仍可尝试其他两位。
	var remaining []string
	for _, id := range artists {
		merged := false
		for i, other := range remaining {
			a, b := min(id, other), max(id, other)
			var result curation.ArtistMergeApplied
			err := write(ctx, "artist_merge/"+x+"/"+a+"/"+b, func(batch, key string) error {
				var err error
				result, err = writer.MergeArtists(ctx, curation.ArtistAutoMerge{A: a, B: b, ExternalID: x, Source: source, BatchID: batch, Key: key})
				return err
			})
			switch failureCode(err) {
			case "nothing_to_apply", "manual_structure", "no_strong_evidence", "artist_redirected", "revision_conflict", "artist_catalog_not_ready", "artist_scope_too_large":
				continue
			}
			if err != nil {
				return err
			}
			if result.ArtistID != "" {
				remaining[i] = result.ArtistID
				merged = true
				break
			}
		}
		if !merged {
			remaining = append(remaining, id)
		}
	}
	return nil
}
