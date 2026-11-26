package program

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 对位要求整组人数相等；同组中一项异名只影响它本身。
type creditPosition struct {
	local    catalog.CurationArtistCredit
	external sources.Credit
	tagged   bool
}

func creditPositions(local []catalog.CurationArtistCredit, external []sources.Credit, tags []string) []creditPosition {
	if len(local) == 0 || len(local) != len(external) {
		return nil
	}
	seenLocal, seenExternal := map[string]bool{}, map[string]bool{}
	pairs := make([]creditPosition, 0, len(local))
	for i, c := range local {
		x := external[i]
		if c.Position != i+1 || c.ArtistID == "" || x.ArtistID == "" || seenLocal[c.ArtistID] || seenExternal[x.ArtistID] || curation.MusicBrainzSpecialArtist(x.ArtistID) {
			return nil
		}
		seenLocal[c.ArtistID], seenExternal[x.ArtistID] = true, true
		pairs = append(pairs, creditPosition{c, x, len(tags) == len(local) && tags[i] == x.ArtistID})
	}
	return pairs
}

func (p creditPosition) link() curation.LinkIntent {
	return curation.LinkIntent{ObjectType: "artist", ObjectID: p.local.ArtistID, ExternalID: p.external.ArtistID}
}

func (p creditPosition) direct() bool { return p.tagged || sameText(p.local.Name, p.external.Name) }

func alignCredits(local []catalog.CurationArtistCredit, external []sources.Credit, tags []string) []curation.LinkIntent {
	var links []curation.LinkIntent
	for _, p := range creditPositions(local, external, tags) {
		if p.direct() {
			links = append(links, p.link())
		}
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

type creditGroup struct {
	role, trackID string
	local         []catalog.CurationArtistCredit
	external      []sources.Credit
	tags          []string
}

func artistCreditGroups(local catalog.CurationArtistCredits, e catalog.CurationEvidence, m MatchResult) []creditGroup {
	var groups []creditGroup
	recordings := map[string][]sources.Credit{}
	album := m.Credits
	if r := m.Release; r != nil {
		album = r.Artists
		for _, medium := range r.Media {
			for _, t := range medium.Tracks {
				if _, dup := recordings[t.RecordingID]; dup {
					recordings[t.RecordingID] = nil
				} else {
					recordings[t.RecordingID] = t.Artists
				}
			}
		}
	} else if r := m.Recording; r != nil {
		recordings[r.ID] = r.Artists
	}
	if m.Level == "artist" && len(e.Tracks) == 1 {
		t := e.Tracks[0]
		if !t.ArtistPlaceholder {
			groups = append(groups, creditGroup{"track_artist", t.ID, local.Tracks[t.ID], m.Credits, t.Identifiers["mb_artist"]})
		}
		return groups
	}
	tags := albumArtistTags(e.Tracks)
	if len(album) > 0 {
		if e.Authenticity["artist"] != metadata.Placeholder {
			groups = append(groups, creditGroup{"release_artist", "", local.ReleaseArtists, album, tags})
		}
		if e.Authenticity["album_artist"] != metadata.Placeholder {
			groups = append(groups, creditGroup{"album_artist", "", local.AlbumArtists, album, tags})
		}
	}
	pairs := map[string]string{}
	for _, p := range m.Pairs {
		pairs[p.TrackID] = p.RecordingID
	}
	for _, t := range e.Tracks {
		if id := pairs[t.ID]; id != "" && !t.ArtistPlaceholder {
			groups = append(groups, creditGroup{"track_artist", t.ID, local.Tracks[t.ID], recordings[id], t.Identifiers["mb_artist"]})
		}
	}
	return groups
}

func consistentArtistLinks(links []curation.LinkIntent) []curation.LinkIntent {
	byArtist := map[string]string{}
	for _, l := range links {
		if old, ok := byArtist[l.ObjectID]; ok && old != l.ExternalID {
			byArtist[l.ObjectID] = ""
		} else {
			byArtist[l.ObjectID] = l.ExternalID
		}
	}
	var result []curation.LinkIntent
	for _, id := range slices.Sorted(maps.Keys(byArtist)) {
		if x := byArtist[id]; x != "" {
			result = append(result, curation.LinkIntent{ObjectType: "artist", ObjectID: id, ExternalID: x})
		}
	}
	return result
}

func planArtistLinks(local catalog.CurationArtistCredits, e catalog.CurationEvidence, m MatchResult) []curation.LinkIntent {
	return consistentArtistLinks(directArtistLinks(artistCreditGroups(local, e, m)))
}

func directArtistLinks(groups []creditGroup) []curation.LinkIntent {
	var links []curation.LinkIntent
	for _, g := range groups {
		links = append(links, alignCredits(g.local, g.external, g.tags)...)
	}
	return links
}

func creditRoles(local catalog.CurationArtistCredits, g creditGroup, p creditPosition, a sources.Artist) (roles []catalog.CurationCreditRole, swapped bool) {
	roles = []catalog.CurationCreditRole{}
	var localComposer, localPlayer, remoteComposer, remotePlayer bool
	for _, r := range local.Roles {
		if g.trackID != "" && g.trackID != r.TrackID {
			continue
		}
		mine := p.local.ArtistID == r.ArtistID
		theirs := sameText(r.Name, p.external.Name) || artistNamed(a, r.Name)
		if !mine && !theirs {
			continue
		}
		roles = append(roles, r)
		if r.Role == "composer" {
			localComposer = localComposer || mine
			remoteComposer = remoteComposer || theirs
		} else {
			localPlayer = localPlayer || mine
			remotePlayer = remotePlayer || theirs
		}
	}
	return roles, (localComposer && !localPlayer && remotePlayer && !remoteComposer) || (localPlayer && !localComposer && remoteComposer && !remotePlayer)
}

func (w *Worker) identifyArtists(ctx context.Context, local catalog.CurationArtistCredits, e catalog.CurationEvidence, m MatchResult, offline bool) ([]curation.LinkIntent, []curation.ArtistCreditProposal, error) {
	artists := m.ArtistEvidence
	if artists == nil {
		artists = map[string]sources.Artist{}
	}
	groups := artistCreditGroups(local, e, m)
	links := directArtistLinks(groups)
	var proposals []curation.ArtistCreditProposal
	for _, g := range groups {
		for _, p := range creditPositions(g.local, g.external, g.tags) {
			if p.direct() {
				continue
			}
			a, ok := artists[p.external.ArtistID]
			if !ok && !offline {
				finder, available := w.sources.ArtistFinder("musicbrainz")
				if available {
					var err error
					a, err = finder.ArtistByID(ctx, p.external.ArtistID)
					if err != nil && !errors.Is(err, sources.ErrNotFound) {
						return nil, nil, err
					}
					artists[p.external.ArtistID] = a
				}
			}
			if artistNamed(a, p.local.Name) {
				links = append(links, p.link())
				continue
			}
			roles, swapped := creditRoles(local, g, p, a)
			if (e.Classical || e.Compilation) && swapped {
				continue
			}
			level, id := m.Level, m.GroupID
			if m.Release != nil {
				level, id = "release", m.Release.ID
			}
			if m.Recording != nil {
				level, id = "recording", m.Recording.ID
			}
			if id == "" {
				continue
			}
			aliases := make([]string, 0, len(a.Aliases))
			for _, alias := range a.Aliases {
				if alias.Name != "" {
					aliases = append(aliases, alias.Name)
				}
			}
			proposals = append(proposals, curation.ArtistCreditProposal{
				Local:    curation.ProposalLocalCredit{ArtistID: p.local.ArtistID, Position: p.local.Position, Name: p.local.Name, Role: g.role, TrackID: g.trackID},
				External: curation.ProposalExternalCredit{Provider: "musicbrainz", ArtistID: p.external.ArtistID, CreditName: p.external.Name, PrimaryName: a.Name, Aliases: aliases},
				Basis:    curation.ProposalCreditBasis{Level: level, Rule: m.Rule, ExternalID: id, Roles: roles},
			})
		}
	}
	links = consistentArtistLinks(links)
	for i, l := range links {
		if a, ok := artists[l.ExternalID]; ok && a.ID == l.ExternalID {
			links[i].ArtistPrimaryName = a.Name
		}
	}
	return links, proposals, nil
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
