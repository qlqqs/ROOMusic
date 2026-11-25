package program

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type comparison struct {
	pairs                                                        []TrackPair
	conflicts                                                    []string
	countEqual, durationsOK, titleMatch, artistMatch, labelMatch bool
	isrcAgree, acoustidAgree                                     int
}

func (c *comparison) conflict(code string) {
	if code != "" && !slices.Contains(c.conflicts, code) {
		c.conflicts = append(c.conflicts, code)
	}
}

func (c comparison) passes(method string) bool {
	if len(c.pairs) == 0 || len(c.conflicts) > 0 {
		return false
	}
	switch method {
	case "tag_id":
		return c.countEqual || c.durationsOK
	case "barcode":
		return c.countEqual && (c.durationsOK || c.titleMatch || c.artistMatch)
	case "catalog_number":
		return c.countEqual && (c.labelMatch || c.durationsOK)
	case "isrc":
		return c.countEqual && c.isrcAgree > 0
	case "acoustid":
		return c.countEqual && c.durationsOK && c.acoustidAgree > 0
	case "disc_id":
		return c.countEqual && (c.titleMatch || c.artistMatch)
	}
	return false
}

func barcodeKey(s string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if len(digits) == 13 && digits[0] == '0' {
		return digits[1:]
	}
	return digits
}

func (c *comparison) compareRelease(e catalog.CurationEvidence, method string, r sources.Release) {
	localTitle, title := looseKey(e.Title), looseKey(r.Title)
	localArtist, artist := looseKey(albumArtist(e)), looseKey(credits(r.Artists))
	compareTitle := !e.TitlePlaceholder && localTitle != "" && title != ""
	compareArtist := !e.ArtistPlaceholder && localArtist != "" && artist != ""
	c.titleMatch = compareTitle && localTitle == title
	c.artistMatch = compareArtist && localArtist == artist
	if (method == "barcode" || method == "catalog_number" || method == "isrc" || method == "acoustid") && compareTitle && compareArtist && !c.titleMatch && !c.artistMatch {
		c.conflict("title_artist_mismatch")
	}
	if remote := barcodeKey(r.Barcode); remote != "" {
		hasLocal, matched := false, false
		for _, b := range e.Barcodes {
			key := barcodeKey(b)
			hasLocal = hasLocal || key != ""
			matched = matched || key == remote
		}
		if hasLocal && !matched {
			c.conflict("barcode_mismatch")
		}
	}
	cat := textnorm.CatalogNumber(e.CatalogNumber)
	hasNumber, matchedNumber := false, false
	for _, l := range r.Labels {
		c.labelMatch = c.labelMatch || sameText(e.Label, l.Name)
		n := textnorm.CatalogNumber(l.CatalogNumber)
		hasNumber = hasNumber || n != ""
		matchedNumber = matchedNumber || cat == n
	}
	if cat != "" && hasNumber && !matchedNumber {
		c.conflict("catalog_mismatch")
	}
	if ids := tags(e.Tracks, "mb_release"); method != "tag_id" && len(ids) > 0 && !slices.Contains(ids, r.ID) {
		c.conflict("release_tag_mismatch")
	}
	if ids := tags(e.Tracks, "mb_release_group"); len(ids) == 1 && ids[0] != r.Group.ID {
		c.conflict("group_tag_mismatch")
	}
	artists := tags(e.Tracks, "mb_album_artist")
	if len(artists) == 0 {
		artists = tags(e.Tracks, "mb_artist")
	}
	if len(artists) == 1 && !slices.ContainsFunc(r.Artists, func(a sources.Credit) bool { return a.ArtistID == artists[0] }) {
		c.conflict("artist_tag_mismatch")
	}
	for _, id := range tags(e.Tracks, "mb_disc") {
		if !slices.ContainsFunc(r.Media, func(m sources.Medium) bool { return slices.Contains(m.DiscIDs, id) }) {
			c.conflict("disc_tag_mismatch")
			break
		}
	}
	if strings.EqualFold(e.MediaType, "CD") && !slices.ContainsFunc(r.Media, func(m sources.Medium) bool { return strings.Contains(strings.ToUpper(m.Format), "CD") }) {
		c.conflict("media_format_mismatch")
	}
	if strings.EqualFold(r.Status, "Pseudo-Release") {
		c.conflict("pseudo_release")
	}
	for _, code := range linkConflicts(e, "release", e.ID, e.ExternalID, r.ID) {
		c.conflict(code)
	}
}

type trackPosition struct{ disc, position int }

func (c *comparison) compareTracks(e catalog.CurationEvidence, method string, release sources.Release) {
	local := e.SortedTracks()
	discs := e.DiscPositions()
	media := slices.Clone(release.Media)
	slices.SortFunc(media, func(a, b sources.Medium) int { return cmp.Compare(a.Position, b.Position) })
	if (len(discs) > 1 || method == "acoustid") && len(discs) != len(media) {
		c.conflict("media_count_mismatch")
	}
	localCounts := map[int]int{}
	inferred := false
	for i, t := range local {
		if i > 0 && t.Disc == local[i-1].Disc && t.Position == local[i-1].Position {
			c.conflict("position_conflict")
			return
		}
		inferred = inferred || t.PositionInferred
		localCounts[t.Disc]++
	}
	var remote []sources.Track
	at := map[trackPosition]sources.Track{}
	shape := len(discs) <= 1 || len(discs) == len(media)
	for i, m := range media {
		tracks := slices.Clone(m.Tracks)
		slices.SortFunc(tracks, func(a, b sources.Track) int { return cmp.Compare(a.Position, b.Position) })
		if m.TrackCount > 0 && m.TrackCount != len(tracks) {
			shape = false
		}
		if len(discs) > 1 && (i >= len(discs) || localCounts[discs[i]] != len(tracks)) {
			shape = false
		}
		for _, t := range tracks {
			key := trackPosition{m.Position, t.Position}
			if _, exists := at[key]; exists {
				c.conflict("position_conflict")
				return
			}
			at[key] = t
		}
		remote = append(remote, tracks...)
	}
	c.countEqual = shape && len(local) == len(remote)
	subset := method == "tag_id" && !inferred
	if !subset && !c.countEqual {
		c.conflict("track_count")
		return
	}

	comparable, within := 0, 0
	for i, t := range local {
		var other sources.Track
		if subset {
			var found bool
			other, found = at[trackPosition{t.Disc, t.Position}]
			if !found {
				c.conflict("position_conflict")
				continue
			}
		} else {
			other = remote[i]
		}
		c.pairs = append(c.pairs, TrackPair{t.ID, other.RecordingID})
		if ok, match := compareLength(t.DurationSeconds, other.LengthMS); ok {
			comparable++
			if match {
				within++
			} else {
				c.conflict("duration_mismatch")
			}
		}
		for _, id := range t.Identifiers["mb_recording"] {
			if id != other.RecordingID {
				c.conflict("recording_tag_mismatch")
			}
		}
		for _, id := range t.Identifiers["mb_track"] {
			if id != other.ID {
				c.conflict("track_tag_mismatch")
			}
		}
		if ids := trackISRCs(t); len(ids) > 0 && len(other.ISRCs) > 0 {
			agree, mismatch := compareISRCs(ids, other.ISRCs)
			if agree {
				c.isrcAgree++
			}
			if mismatch {
				c.conflict("isrc_mismatch")
			}
		}
	}
	c.durationsOK = comparable > 0 && comparable*2 >= len(local) && within == comparable
	if inferred && within != len(local) {
		c.conflict("inferred_positions")
	}
}

func compareISRCs(local, remote []string) (agree, mismatch bool) {
	remoteIDs := map[string]bool{}
	for _, raw := range remote {
		if key := isrcKey(raw); key != "" {
			remoteIDs[key] = true
		}
	}
	if len(remoteIDs) == 0 {
		return false, false
	}
	for _, id := range local {
		if remoteIDs[id] {
			agree = true
		} else {
			mismatch = true
		}
	}
	return agree, mismatch
}

// DecideAlbum 对本步骤的完整发行逐一核对；search 只保留候选。
func DecideAlbum(e catalog.CurationEvidence, method string, releases []sources.Release) MatchResult {
	return decideAlbum(e, method, releases, nil)
}

func decideAlbum(e catalog.CurationEvidence, method string, releases []sources.Release, recordingIDs map[string]map[string]bool) MatchResult {
	result := newMatch("album", method)
	result.Candidates = releaseCandidates(method, releases)
	if method == "tag_id" && len(tags(e.Tracks, "mb_release")) > 1 {
		return uncertain(result, "mixed_release_tags")
	}
	if len(releases) == 0 {
		return result
	}
	if method == "search" {
		return uncertain(result, "search_only")
	}
	if len(releases) > 3 {
		return uncertain(result, "too_many")
	}
	frequencies := map[string]int{}
	passed := 0
	var winner *sources.Release
	var pairs []TrackPair
	for i := range releases {
		r := &releases[i]
		var c comparison
		c.compareRelease(e, method, *r)
		c.compareTracks(e, method, *r)
		if method == "acoustid" {
			for _, pair := range c.pairs {
				if ids := recordingIDs[pair.TrackID]; ids != nil {
					if ids[pair.RecordingID] {
						c.acoustidAgree++
					} else {
						c.conflict("acoustid_mismatch")
					}
				}
			}
		}
		if c.passes(method) {
			passed++
			winner, pairs = r, c.pairs
		}
		for _, code := range c.conflicts {
			frequencies[code]++
		}
	}
	switch passed {
	case 1:
		result.Status, result.Release, result.Pairs = "matched", winner, pairs
	case 0:
		// 多个候选均不通过时，保留最常见的相反证据（并列均保留）。
		top := 0
		for _, n := range frequencies {
			top = max(top, n)
		}
		for _, code := range slices.Sorted(maps.Keys(frequencies)) {
			if frequencies[code] == top {
				result.Conflicts = append(result.Conflicts, code)
			}
		}
		result.Status = "uncertain"
	default:
		return uncertain(result, "ambiguous")
	}
	return result
}
