package catalog

import (
	"cmp"
	"math"
	"slices"
)

func (e CurationEvidence) SortedTracks() []CurationTrack {
	rows := slices.Clone(e.Tracks)
	slices.SortFunc(rows, func(a, b CurationTrack) int {
		return cmp.Or(cmp.Compare(a.Disc, b.Disc), cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})
	return rows
}

func (e CurationEvidence) DiscPositions() []int {
	discs := slices.Clone(e.Media)
	for _, t := range e.Tracks {
		discs = append(discs, t.Disc)
	}
	slices.Sort(discs)
	return slices.Compact(discs)
}

// DiscTOC 以 CD 帧记录每首曲目的起点和整碟的结束位置。
type DiscTOC struct {
	Offsets []int
	Leadout int
}

// DiscTOCs 只有所有介质都能从同一镜像的连续 CUE 索引重建时才返回目录。
func (e CurationEvidence) DiscTOCs() []DiscTOC {
	byDisc := map[int][]CurationTrack{}
	for _, t := range e.SortedTracks() {
		byDisc[t.Disc] = append(byDisc[t.Disc], t)
	}
	var out []DiscTOC
	for _, d := range e.DiscPositions() {
		rows := byDisc[d]
		if len(rows) == 0 || len(rows) > 99 {
			return nil
		}
		first, last := rows[0], rows[len(rows)-1]
		if first.ParentFile == "" || first.CueSheetPath == "" || last.CueEndFrames == nil {
			return nil
		}
		var toc DiscTOC
		prev := -1
		for i, t := range rows {
			if t.SourceKind != "cue_virtual" || t.SourceRootID != first.SourceRootID || t.CueSheetPath != first.CueSheetPath || t.ParentFile != first.ParentFile || t.Position != i+1 || t.CueIndexFrames == nil || *t.CueIndexFrames <= prev {
				return nil
			}
			prev = *t.CueIndexFrames
			toc.Offsets = append(toc.Offsets, 150+prev)
		}
		if *last.CueEndFrames <= prev || int64(*last.CueEndFrames) > math.MaxUint32-150 {
			return nil
		}
		toc.Leadout = 150 + *last.CueEndFrames
		out = append(out, toc)
	}
	return out
}
