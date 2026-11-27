package program

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

var errJudgeBudget = errors.New("识别线索已用完六次查询")
var errIncompleteHint = errors.New("查询结果未列全")

func hasLocalIdentifier(e catalog.CurationEvidence, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	contains := func(s string) bool { return strings.Contains(s, value) }
	for _, s := range []string{e.Title, e.Artist, e.AlbumArtist, e.RawCatalogNumber, e.Barcode, e.CatalogNumber, e.Label, e.Edition, e.MediaType} {
		if contains(s) {
			return true
		}
	}
	if e.Year > 0 && contains(strconv.Itoa(e.Year)) {
		return true
	}
	if slices.ContainsFunc(e.Barcodes, contains) {
		return true
	}
	for _, t := range e.Tracks {
		for _, v := range t.LocalTags {
			if contains(v) {
				return true
			}
		}
		if contains(t.Title) || contains(t.Artist) || contains(t.WorkTitle) || slices.ContainsFunc(strings.Split(path.Clean(t.RelativePath), "/"), contains) {
			return true
		}
		for _, values := range t.Identifiers {
			if slices.ContainsFunc(values, contains) {
				return true
			}
		}
		for _, v := range t.Comments {
			if contains(v.Value) {
				return true
			}
		}
	}
	return false
}

// 文件与规则仍属于当前代才允许消费请求；已缓存的读取不进入这个回调。
func (w *Worker) runJudge(ctx context.Context, e catalog.CurationEvidence, c *curation.JudgeCursor) (MatchResult, error) {
	p, err := w.writer.PendingItem(ctx, c.ItemID)
	if err != nil {
		return MatchResult{}, err
	}
	var saved struct {
		Judge    curation.JudgeAnswer
		Snapshot MatchSnapshot `json:"match_snapshot"`
	}
	if err = json.Unmarshal(p.Evidence, &saved); err != nil {
		return MatchResult{}, err
	}
	var candidates []Candidate
	if err = json.Unmarshal(p.Candidates, &candidates); err != nil {
		return MatchResult{}, err
	}
	ctx = sources.WithRequestPermit(ctx, func(ctx context.Context) error {
		if c.Used >= 6 {
			return errJudgeBudget
		}
		var used int
		err := w.db.QueryRowContext(ctx, `UPDATE curation_work_states SET result=jsonb_set(result,'{judge,used}',to_jsonb(coalesce((result->'judge'->>'used')::int,0)+1))
 WHERE subject='release' AND target_id=$1 AND result->'judge'->>'item_id'=$2 AND file_digest=curation_release_file_digest(target_id)
 AND generation=$3 AND match_rule_version=$4 AND (result->'judge'->>'used')::int < 6 RETURNING (result->'judge'->>'used')::int`, e.ID, c.ItemID, c.Generation, c.Version).Scan(&used)
		if errors.Is(err, sql.ErrNoRows) {
			return errReleaseChanged
		}
		if err == nil {
			c.Used = used
		}
		return err
	})
	f, ok := w.sources.ReleaseFinder("musicbrainz")
	if !ok {
		return MatchResult{}, sources.ErrUnavailable
	}
	rf, _ := w.sources.RecordingFinder("musicbrainz")
	mode := "album"
	if p.Kind == "recording_match" {
		mode = "single"
	}
	undecided := newMatch(mode, "model")
	if c.Status == "conflict" {
		undecided.Status = "uncertain"
		undecided.Candidates = candidates
		_ = json.Unmarshal(p.Conflicts, &undecided.Conflicts)
	}
	for index := c.Next; index < len(saved.Judge.Groups); index++ {
		var m MatchResult
		if index == -1 {
			for _, candidate := range candidates {
				if candidate.ExternalID == saved.Judge.SelectedID {
					m, err = checkHintCandidates(ctx, e, mode, []Candidate{candidate}, candidate.Method, curation.JudgeGroup{}, saved.Snapshot, f, rf)
					break
				}
			}
		} else {
			m, err = w.lookupHint(ctx, e, mode, saved.Judge.Groups[index], saved.Snapshot, candidates, f, rf)
		}
		if errors.Is(err, errIncompleteHint) {
			undecided.Conflicts = append(undecided.Conflicts, "list_truncated")
			return undecided, nil
		}
		if errors.Is(err, errJudgeBudget) {
			undecided.Conflicts = append(undecided.Conflicts, "request_limit")
			return undecided, nil
		}
		if err != nil {
			return m, err
		}
		if m.Status == "matched" || slices.Contains(m.Conflicts, "ambiguous_editions") {
			m.Method = "model"
			return m, nil
		}
		if index >= 0 {
			kind := saved.Judge.Groups[index].AlbumType
			if kind == "podcast" || kind == "audiobook" {
				break
			}
		}
		var next int
		err = w.db.QueryRowContext(ctx, `UPDATE curation_work_states SET result=jsonb_set(result,'{judge,next}',to_jsonb($3::int))
 WHERE subject='release' AND target_id=$1 AND result->'judge'->>'item_id'=$2 AND generation=$4 AND match_rule_version=$5
 AND file_digest=curation_release_file_digest(target_id) RETURNING (result->'judge'->>'next')::int`, e.ID, c.ItemID, index+1, c.Generation, c.Version).Scan(&next)
		if errors.Is(err, sql.ErrNoRows) {
			return m, errReleaseChanged
		}
		if err != nil {
			return m, err
		}
		c.Next = next
	}
	return undecided, nil
}

func (w *Worker) lookupHint(ctx context.Context, e catalog.CurationEvidence, mode string, g curation.JudgeGroup, snap MatchSnapshot, previous []Candidate, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, error) {
	empty := newMatch(mode, "model")
	if g.AlbumType == "podcast" || g.AlbumType == "audiobook" {
		return empty, nil
	}
	if g.ArtistID != "" {
		if _, ok := snap.Artists[g.ArtistID]; !ok {
			g.ArtistID = ""
		}
	}
	if g.AlbumType == "compilation" {
		e.Compilation = true
		g.ArtistID = variousArtistsID
	}
	method := "search"
	var candidates []Candidate
	switch {
	case hasLocalIdentifier(e, g.Identifier.Value) && (g.Identifier.Kind == "barcode" || g.Identifier.Kind == "catalog_number"):
		finder, ok := f.(curation.ReleasePageFinder)
		if !ok || mode != "album" {
			return empty, nil
		}
		method = g.Identifier.Kind
		q := curation.ReleaseSearch{}
		if method == "barcode" {
			q.Barcodes = []string{g.Identifier.Value}
			if len(e.Barcodes) == 0 {
				e.Barcodes = q.Barcodes
			}
		} else {
			q.CatalogNumber = g.Identifier.Value
			q.Label = e.Label
			if e.CatalogNumber == "" {
				e.CatalogNumber = q.CatalogNumber
			}
		}
		page, err := queryResult(finder.SearchReleasePage(ctx, q))
		if err != nil {
			return empty, err
		}
		if !page.Complete {
			return empty, errIncompleteHint
		}
		hits, _ := identifierHits(q, page.Items)
		candidates = releaseCandidates(method, hits)
	case g.Name != (curation.JudgeName{}) || g.ArtistID != "":
		n := localNames(curation.LocalSearchEvidence(e), mode)
		if mode == "single" {
			n.Title = cmp.Or(g.Name.Track, g.Name.Work, n.Title)
		} else {
			n.Title = cmp.Or(g.Name.Release, g.Name.Work, n.Title)
		}
		n.Artist = cmp.Or(g.Name.Artist, g.Name.Composer, n.Artist)
		n.ArtistID = g.ArtistID
		count := len(e.Tracks)
		if mode == "single" {
			count = 0
		}
		qry := func(names SearchNames, count int) (MatchResult, error) {
			m, err := queryText(ctx, mode, e, names, count, false, f, rf)
			if err == nil && len(m.Snapshot.Pages) > 0 && !m.Snapshot.Pages[len(m.Snapshot.Pages)-1].Complete {
				return m, errIncompleteHint
			}
			return m, err
		}
		m, err := qry(n, count)
		if err != nil {
			return m, err
		}
		if len(m.Candidates) == 0 && count > 0 {
			n.Title = relaxedTitle(n.Title)
			m, err = qry(n, 0)
			if err != nil {
				return m, err
			}
		}
		if len(m.Candidates) == 0 && n.ArtistID == "" && n.Artist != "" {
			if af, ok := w.sources.ArtistFinder("musicbrainz"); ok {
				artists, _, incomplete, err := lookupArtist(ctx, af, n.Artist)
				if err != nil {
					return m, err
				}
				if incomplete {
					return m, errIncompleteHint
				}
				if len(artists) == 1 {
					for id := range artists {
						m, err = qry(SearchNames{ArtistID: id}, count)
					}
					if err != nil {
						return m, err
					}
				}
			}
		}
		if len(m.Snapshot.Pages) == 0 {
			return empty, nil
		}
		candidates = m.Candidates
	default:
		if g.Version.Year == "" && g.Version.Country == "" && g.Version.Edition == "" && g.Version.Medium == "" && g.AlbumType == "" {
			return empty, nil
		}
		candidates = previous
	}
	return checkHintCandidates(ctx, e, mode, candidates, method, g, snap, f, rf)
}

func checkHintCandidates(ctx context.Context, e catalog.CurationEvidence, mode string, rows []Candidate, method string, g curation.JudgeGroup, snap MatchSnapshot, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, error) {
	result := newMatch(mode, "model")
	var passed []Candidate
	// 版本线索只区分同一发行组内的版次。
	sameGroup := len(rows) > 1 && rows[0].ReleaseGroupID != "" && !slices.ContainsFunc(rows[1:], func(c Candidate) bool {
		return c.ReleaseGroupID != rows[0].ReleaseGroupID
	})
	for _, c := range rows {
		checked, err := checkHintCandidate(ctx, e, mode, &c, method, g, snap, f, rf, sameGroup, len(rows) == 1)
		if err != nil {
			return result, err
		}
		if checked.Status != "matched" {
			continue
		}
		result = checked
		if len(checked.Candidates) == 1 {
			c.Verification = checked.Candidates[0].Verification
			c.TextEvidence = checked.Candidates[0].TextEvidence
			c.Conflicts = checked.Candidates[0].Conflicts
		}
		passed = append(passed, c)
	}
	if len(passed) > 1 {
		result = uncertain(newMatch(mode, "model"), "ambiguous_editions")
		result.Candidates = passed
		result.Route = ""
	}
	return result, nil
}

func checkHintCandidate(ctx context.Context, e catalog.CurationEvidence, mode string, c *Candidate, method string, g curation.JudgeGroup, snap MatchSnapshot, f sources.ReleaseFinder, rf sources.RecordingFinder, sameGroup, unique bool) (MatchResult, error) {
	if method == "" || method == "model" {
		method = "search"
	}
	var checked MatchResult
	if mode == "single" {
		if rf == nil {
			return MatchResult{}, sources.ErrUnavailable
		}
		rec, err := queryResult(rf.RecordingByID(ctx, c.ExternalID))
		if err != nil {
			return MatchResult{}, err
		}
		if rec.ID != c.ExternalID {
			return uncertain(MatchResult{}, "external_id_mismatch"), nil
		}
		checked = DecideSingle(e, method, []sources.Recording{rec})
		c.Recording = &rec
	} else {
		r := c.Release
		if !completeRelease(r, c.ExternalID) || !r.Complete {
			detail, err := queryResult(f.ReleaseByID(ctx, c.ExternalID))
			if err != nil {
				return MatchResult{}, err
			}
			if detail.ID != c.ExternalID {
				return uncertain(MatchResult{}, "external_id_mismatch"), nil
			}
			r = &detail
		}
		if !matchesHint(e, g, *r, sameGroup) {
			return MatchResult{}, nil
		}
		checked = decideAlbum(e, method, []sources.Release{*r}, snap)
		if checked.Status == "uncertain" {
			checked = partialAlbumMatch(e, checked)
		}
		if method == "search" && checked.Status != "matched" && shortHintMatches(e, g, *r, unique) {
			// 这里只补足唯一反证 title_mismatch，保留原结构比较得到的曲目配对。
			if len(checked.Candidates) == 1 && slices.Equal(checked.Candidates[0].Conflicts, []string{"title_mismatch"}) {
				var comp comparison
				comp.compareRelease(e, "search", *r)
				comp.compareTracks(e, "model", *r)
				checked.Status, checked.Level, checked.Rule, checked.Release, checked.Pairs = "matched", "release", "model_title_tracks", r, comp.pairs
				checked.Conflicts = nil
				checked.Candidates[0].Conflicts = nil
				checked.Candidates[0].Verification = verifyAlbum(e, *r, comp)
				checked.Candidates[0].Verification.Direct = true
				checked.Candidates[0].Verification.Method = "model_title_tracks"
			}
		}
		c.Release = r
	}
	return checked, nil
}

func shortHintMatches(e catalog.CurationEvidence, g curation.JudgeGroup, r sources.Release, unique bool) bool {
	n := len(e.Tracks)
	if !unique || n == 0 || n > 4 || !sameText(g.Name.Release, r.Title) || !sameArtist(albumArtist(e), credits(r.Artists)) {
		return false
	}
	if slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool { return t.PositionInferred }) {
		return false
	}
	var c comparison
	c.compareRelease(e, "search", r)
	c.compareTracks(e, "model", r)
	return c.countEqual && c.tagText.Matches == n && c.within == n && len(c.conflicts) == 0
}

func matchesHint(e catalog.CurationEvidence, g curation.JudgeGroup, r sources.Release, sameGroup bool) bool {
	if g.ArtistID != "" && !creditID(r.Artists, g.ArtistID) {
		return false
	}
	if g.AlbumType == "soundtrack" || g.AlbumType == "live" || g.AlbumType == "compilation" {
		if !(g.AlbumType == "compilation" && creditID(r.Artists, variousArtistsID)) && !slices.ContainsFunc(r.Group.SecondaryTypes, func(s string) bool { return strings.EqualFold(s, g.AlbumType) }) {
			return false
		}
	}
	if !sameGroup {
		return true
	}
	v := g.Version
	for _, x := range []string{v.Year, v.Country, v.Edition, v.Medium} {
		if x != "" && !hasLocalIdentifier(e, x) {
			return false
		}
	}
	if v.Year != "" && !strings.HasPrefix(r.Date, v.Year) || v.Country != "" && !strings.EqualFold(r.Country, v.Country) {
		return false
	}
	if v.Edition != "" && !strings.Contains(strings.ToLower(r.Title+" "+r.Disambiguation), strings.ToLower(v.Edition)) {
		return false
	}
	if v.Medium != "" && !slices.ContainsFunc(r.Media, func(m sources.Medium) bool { return strings.EqualFold(m.Format, v.Medium) }) {
		return false
	}
	return true
}
