package program

// ReleaseMergeChanges 同时用于后台预览和例外采纳，令牌绑定双方组、碟序及曲目成员。
// 参数取自治理的并入预览（来源、目标、令牌、隐藏内容、收藏转移数）；program 不依赖治理包。
func ReleaseMergeChanges(source, target any, token string, hidden any, favoritesMoved int) []MatchChange {
	return []MatchChange{
		{Field: "release_merge", Before: source, After: target},
		{Field: "merge_token", After: token},
		{Field: "hidden", After: hidden},
		{Field: "favorites_moved", After: favoritesMoved},
	}
}
