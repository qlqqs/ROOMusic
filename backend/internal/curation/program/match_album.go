package program

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type TagTextEvidence struct {
	TitleMatch  bool `json:"title_match"`
	ArtistMatch bool `json:"artist_match"`
	Comparable  int  `json:"comparable"`
	Matches     int  `json:"matches"`
}

type comparison struct {
	tagText                                                      TagTextEvidence
	pairedTitles                                                 map[string]string
	pairs                                                        []TrackPair
	conflicts                                                    []string
	countEqual, durationsOK, titleMatch, artistMatch, labelMatch bool
	isrcAgree, acoustidAgree                                     int
	comparable, within                                           int
	differences                                                  []DurationDifference
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
	case "search":
		return c.countEqual && c.durationsOK
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
	localArtist, artist := artistKey(albumArtist(e)), artistKey(credits(r.Artists))
	compareTitle := !e.TitlePlaceholder && localTitle != "" && title != ""
	compareArtist := !e.ArtistPlaceholder && localArtist != "" && artist != ""
	c.titleMatch = compareTitle && localTitle == title
	c.artistMatch = compareArtist && localArtist == artist || creditID(r.Artists, e.ArtistMBID) || (e.Compilation && creditID(r.Artists, variousArtistsID))
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
	if method == "catalog_number" && cat != "" && e.Label != "" && len(r.Labels) > 0 &&
		!slices.ContainsFunc(r.Labels, func(l sources.Label) bool {
			return textnorm.CatalogNumber(l.CatalogNumber) == cat && sameText(e.Label, l.Name)
		}) {
		c.conflict("catalog_mismatch")
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
	for _, code := range releaseBlocks(e, r.ID, r.Group.ID) {
		c.conflict(code)
	}
}

type trackPosition struct{ disc, position int }

func (c *comparison) compareTracks(e catalog.CurationEvidence, method string, release sources.Release) {
	local := e.SortedTracks()
	discs := e.DiscPositions()
	media := slices.Clone(release.Media)
	slices.SortFunc(media, func(a, b sources.Medium) int { return cmp.Compare(a.Position, b.Position) })
	if (len(discs) > 1 || method == "acoustid" || method == "search") && len(discs) != len(media) {
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
		if (len(discs) > 1 || method == "search") && (i >= len(discs) || localCounts[discs[i]] != len(tracks)) {
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
			if method == "model" && !t.PositionInferred {
				positioned, ok := at[trackPosition{t.Disc, t.Position}]
				if !ok || positioned.RecordingID != other.RecordingID {
					c.conflict("position_conflict")
				}
			}
		}
		c.pairs = append(c.pairs, TrackPair{t.ID, other.RecordingID})
		title := other.Title
		if title == "" {
			title = other.RecordingTitle
		}
		if t.Authenticity["title"] == metadata.Real && looseKey(t.Title) != "" && looseKey(title) != "" {
			c.tagText.Comparable++
			if sameText(t.Title, title) {
				c.tagText.Matches++
			}
		}
		if (t.TitlePlaceholder || t.TitleInferred) && title != "" {
			if c.pairedTitles == nil {
				c.pairedTitles = map[string]string{}
			}
			c.pairedTitles[t.ID] = title
		}
		for _, code := range linkConflicts(e, "track", t.ID, t.ExternalID, other.RecordingID) {
			c.conflict(code)
		}
		if ok, match := compareLength(t.DurationSeconds, other.LengthMS); ok {
			comparable++
			if match {
				within++
			} else {
				c.conflict("duration_mismatch")
				if method == "model" {
					c.differences = append(c.differences, DurationDifference{t.Disc, t.Position, int64(t.DurationSeconds)*1000 - *other.LengthMS})
				}
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
	c.comparable, c.within = comparable, within
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

// DecideAlbum 核对已读出的完整发行集合。
func DecideAlbum(e catalog.CurationEvidence, method string, releases []sources.Release) MatchResult {
	result := decideAlbum(e, method, releases, MatchSnapshot{})
	if result.Status == "uncertain" {
		return partialAlbumMatch(e, result)
	}
	return result
}

func decideAlbum(e catalog.CurationEvidence, method string, releases []sources.Release, snapshot MatchSnapshot) MatchResult {
	return compareAlbumEditions(e, method, releases, snapshot, false)
}

func compareAlbumEditions(e catalog.CurationEvidence, method string, releases []sources.Release, snapshot MatchSnapshot, artistCreditVerified bool) MatchResult {
	result := newMatch("album", method)
	result.Candidates = releaseCandidates(method, releases)
	result.Snapshot = snapshot
	result.Snapshot.Version, result.Snapshot.Method = 1, method
	if method == "tag_id" && len(tags(e.Tracks, "mb_release")) > 1 {
		return uncertain(result, "mixed_release_tags")
	}
	if len(releases) == 0 {
		return result
	}
	if len(releases) > 100 {
		markBlocked(e, result.Mode, result.Candidates)
		return uncertain(result, "list_truncated")
	}
	result.Snapshot.CandidateSetComplete = true
	var passed []verifiedEdition
	freq := map[string]int{}
	blocked := 0
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		r := candidate.Release
		var c comparison
		c.compareRelease(e, method, *r)
		c.artistMatch = artistCreditVerified || c.artistMatch
		c.compareTracks(e, method, *r)
		if method == "acoustid" {
			for _, pair := range c.pairs {
				if ids := snapshot.RecordingIDs[pair.TrackID]; ids != nil {
					if ids[pair.RecordingID] {
						c.acoustidAgree++
					} else {
						c.conflict("acoustid_mismatch")
					}
				}
			}
		}
		if method == "tag_id" {
			c.artistMatch = c.artistMatch || snapshot.TagArtist != "" && albumArtist(e) == snapshot.TagArtist && r.ID == snapshot.TagReleaseID
			c.tagText.TitleMatch, c.tagText.ArtistMatch = c.titleMatch, c.artistMatch
			candidate.TextEvidence = &c.tagText
			if e.Authenticity["title"] == metadata.Real && e.SelectedArtistAuth() == metadata.Real && looseKey(r.Title) != "" && looseKey(credits(r.Artists)) != "" && !c.titleMatch && !c.artistMatch && c.tagText.Matches == 0 {
				if c.tagText.Comparable > 0 {
					c.conflict("tag_text_mismatch")
				} else {
					c.conflict("title_artist_mismatch")
				}
			}
		}
		if method == "search" {
			if len(e.Tracks) <= 4 {
				if !c.titleMatch {
					c.conflict("title_mismatch")
				}
				if !c.artistMatch {
					c.conflict("artist_mismatch")
				}
			}
			if !c.durationsOK && len(c.conflicts) == 0 {
				c.conflict("duration_missing")
			}
		}
		if (method == "catalog_number" && (e.Authenticity["label"] == metadata.Inferred || e.Authenticity["catalog_number"] == metadata.Inferred) || method == "barcode" && inferredBarcode(e, r.Barcode)) && !c.durationsOK {
			c.conflict("duration_missing")
		}
		if c.passes(method) {
			passed = append(passed, verifiedEdition{r, c})
		}
		candidate.Verification = verifyAlbum(e, *r, c)
		candidate.Verification.Method = method
		candidate.Verification.Direct = c.passes(method)
		candidate.Conflicts = c.conflicts
		if blockedCodes(c.conflicts) {
			blocked++
		}
		for _, code := range c.conflicts {
			freq[code]++
		}
	}
	switch len(passed) {
	case 0:
		result = uncertain(result, commonConflicts(freq)...)
		if blocked == len(releases) {
			result.Status = "no_usable_candidates"
			result.Route = ""
		}
	case 1:
		result.Status, result.Level, result.Rule = "matched", "release", "identifier"
		if method == "search" {
			result.Rule = "structure"
		}
		result.Release, result.Pairs = passed[0].release, passed[0].comparison.pairs
	default:
		for _, v := range passed {
			if !v.comparison.countEqual || !v.comparison.durationsOK || len(e.Tracks) <= 4 && (!v.comparison.titleMatch || !v.comparison.artistMatch) {
				return uncertain(result, "ambiguous")
			}
		}
		result = matchedEditions(result, passed)
	}
	return result
}

type verifiedEdition struct {
	release    *sources.Release
	comparison comparison
}

func matchedEditions(result MatchResult, passed []verifiedEdition) MatchResult {
	rows := make([]sources.Release, 0, len(passed))
	representative := passed[0]
	for _, v := range passed {
		rows = append(rows, *v.release)
		if earlierEdition(*v.release, *representative.release) {
			representative = v
		}
	}
	if sharedGroup(rows) == "" {
		return uncertain(result, "different_release_groups")
	}
	result = groupMatch(result, rows, "structure")
	result.Release, result.Pairs = nil, nil
	result.TrackTitles = representative.comparison.pairedTitles
	result.RepresentativeID = representative.release.ID
	return result
}

func inferredBarcode(e catalog.CurationEvidence, barcode string) bool {
	inferred := false
	for i, raw := range e.Barcodes {
		if barcodeKey(raw) != barcodeKey(barcode) {
			continue
		}
		if i >= len(e.BarcodeAuthenticity) || e.BarcodeAuthenticity[i] == metadata.Real {
			return false
		}
		inferred = inferred || e.BarcodeAuthenticity[i] == metadata.Inferred
	}
	return inferred
}

func commonConflicts(frequencies map[string]int) []string {
	top := 0
	for _, n := range frequencies {
		top = max(top, n)
	}
	var codes []string
	for _, code := range slices.Sorted(maps.Keys(frequencies)) {
		if frequencies[code] == top {
			codes = append(codes, code)
		}
	}
	return codes
}

func blockingCode(code string) bool { return code == "existing_link" || code == "suppressed" }

func blockedCodes(codes []string) bool { return slices.ContainsFunc(codes, blockingCode) }

func releaseBlocks(e catalog.CurationEvidence, id, group string) []string {
	codes := linkConflicts(e, "release", e.ID, e.ExternalID, id)
	if group != "" {
		for _, c := range linkConflicts(e, "release_group_match", e.ID, e.ReleaseGroupMatchID, group) {
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
		if e.ExternalGroupID != "" && group != e.ExternalGroupID && !slices.Contains(codes, "existing_link") {
			codes = append(codes, "existing_link")
		}
	}
	return codes
}

func sharedGroup(rows []sources.Release) string {
	if len(rows) == 0 {
		return ""
	}
	id := rows[0].Group.ID
	for _, r := range rows[1:] {
		if r.Group.ID != id {
			return ""
		}
	}
	return id
}

func sameCredits(a, b []sources.Credit) bool {
	return len(a) > 0 && slices.EqualFunc(a, b, func(x, y sources.Credit) bool { return x.ArtistID != "" && x.ArtistID == y.ArtistID })
}

func groupMatch(result MatchResult, rows []sources.Release, rule string) MatchResult {
	result.Status, result.Level, result.Rule, result.GroupID = "matched", "release_group", rule, rows[0].Group.ID
	result.GroupTitle = rows[0].Group.Title
	for _, r := range rows {
		if r.Group.FirstReleaseDate != "" {
			result.GroupFirstReleaseDate = r.Group.FirstReleaseDate
			break
		}
	}
	result.Credits = rows[0].Artists
	for _, r := range rows {
		if !sameCredits(result.Credits, r.Artists) || credits(result.Credits) != credits(r.Artists) {
			result.Credits = nil
			break
		}
	}
	return result
}

func earlierEdition(a, b sources.Release) bool {
	if a.Date == b.Date {
		return a.ID < b.ID
	}
	return a.Date != "" && (b.Date == "" || a.Date < b.Date)
}

const variousArtistsID = "89ad4ac3-39f7-470e-963a-56509c546377"

func creditID(cs []sources.Credit, id string) bool {
	return id != "" && len(cs) == 1 && cs[0].ArtistID == id
}

func sameAlbumNames(e catalog.CurationEvidence, r sources.Release) bool {
	title, artist := textnorm.MergeKey(e.Title), textnorm.MergeKey(albumArtist(e))
	return !e.TitlePlaceholder && !e.ArtistPlaceholder && title != "" && artist != "" &&
		title == textnorm.MergeKey(r.Title) && artist == textnorm.MergeKey(credits(r.Artists))
}
