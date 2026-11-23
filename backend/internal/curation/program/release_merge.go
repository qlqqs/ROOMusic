package program

import "github.com/qlqq/roomusic/backend/internal/catalog/governance"

// ReleaseMergeChanges 同时用于后台预览和例外采纳，令牌绑定双方组、碟序及曲目成员。
func ReleaseMergeChanges(p governance.ReleaseMergePreview) []MatchChange {
	return []MatchChange{
		{Field: "release_merge", Before: p.Source, After: p.Target},
		{Field: "merge_token", After: p.Token},
		{Field: "hidden", After: p.Hidden},
		{Field: "favorites_moved", After: p.FavoritesMoved},
	}
}
