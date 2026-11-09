package queue

import "slices"

// AdvanceReason 区分自然结束与手动切歌，单曲循环只影响前者。
type AdvanceReason string

const (
	ReasonNatural AdvanceReason = "natural"
	ReasonManual  AdvanceReason = "manual"
)

// AdvanceOptions 的 IsSkipped 用于跳过已失败或不应自动选择的条目；nil 表示不跳过。
type AdvanceOptions struct {
	Reason    AdvanceReason
	IsSkipped func(string) bool
}

// Step 的 EntryID 为空表示没有可播放的结果；此时前进操作保持原状态。
type Step struct {
	State   State
	EntryID string
}

// Advance 按模式前进；随机模式耗尽袋子后重新装袋，不连续重复可用的不同条目。
func Advance(state State, env Env, options AdvanceOptions) Step {
	return advance(state, env, options, "")
}

// PeekNatural 只预选自然结束后的下一项，不修改当前项、历史或袋子；空串表示没有下一项。
// 调用方在播放区运行时缓存返回的 ID，确认开始播放时交给 AdvanceTo；重复调用会重新抽签。
func PeekNatural(state State, env Env, isSkipped func(string) bool) string {
	return Advance(state, env, AdvanceOptions{Reason: ReasonNatural, IsSkipped: isSkipped}).EntryID
}

// AdvanceTo 消费自然前进的预选 ID，不调用随机数；历史与袋子的更新复用 Advance。
// 预选被删除、被跳过或已不符合当前模式的候选规则时，返回空 EntryID 和原状态。
// 调用方应丢弃失效预选，再调用 PeekNatural；不可把旧渲染事件静默替换为另一首。
// 当前项或模式改变时，调用方应主动丢弃缓存；同一 ID 不携带会话或访问代次。
func AdvanceTo(state State, entryID string, isSkipped func(string) bool) Step {
	if entryID == "" {
		return Step{State: state}
	}
	return advance(state, Env{}, AdvanceOptions{Reason: ReasonNatural, IsSkipped: isSkipped}, entryID)
}

func advance(state State, env Env, options AdvanceOptions, selected string) Step {
	current := state.CurrentEntryID
	if len(state.Entries) == 0 {
		return Step{State: state}
	}
	if state.Mode == ModeRepeatOne && options.Reason == ReasonNatural && current != "" && !skipped(current, options.IsSkipped) {
		if selected != "" && selected != current {
			return Step{State: state}
		}
		return Step{State: state, EntryID: current}
	}
	var picked Step
	if state.Mode == ModeShuffle {
		picked = pickShuffled(state, env, options.IsSkipped, selected)
	} else {
		picked = pickInOrder(state, options.IsSkipped)
		if selected != "" && picked.EntryID != selected {
			return Step{State: state}
		}
	}
	if picked.EntryID == "" {
		return Step{State: state}
	}
	picked.State.CurrentEntryID = picked.EntryID
	picked.State.History = pushHistory(state.History, current)
	return picked
}

func pickInOrder(state State, isSkipped func(string) bool) Step {
	entries := state.Entries
	at := indexOfEntry(entries, state.CurrentEntryID)
	wraps := state.Mode == ModeRepeatAll
	for step := 1; step <= len(entries); step++ {
		index := at + step
		if index >= len(entries) && !wraps {
			return Step{State: state}
		}
		candidate := entries[index%len(entries)]
		if candidate.EntryID == state.CurrentEntryID && len(entries) > 1 {
			continue
		}
		if !skipped(candidate.EntryID, isSkipped) {
			return Step{State: state, EntryID: candidate.EntryID}
		}
	}
	return Step{State: state}
}

// selected 非空时只消费该候选；空串时按注入的随机数抽签。两条路径共用过滤与重装袋规则。
func pickShuffled(state State, env Env, isSkipped func(string) bool, selected string) Step {
	ids := make(map[string]bool, len(state.Entries))
	for _, entry := range state.Entries {
		ids[entry.EntryID] = true
	}
	usable := func(id string) bool { return !skipped(id, isSkipped) && ids[id] }
	bag := filterIDs(state.Bag, func(id string) bool { return id != state.CurrentEntryID && usable(id) })
	if len(bag) == 0 {
		for _, entry := range state.Entries {
			if entry.EntryID != state.CurrentEntryID && usable(entry.EntryID) {
				bag = append(bag, entry.EntryID)
			}
		}
		if len(bag) == 0 {
			current := state.CurrentEntryID
			if !ids[current] || !usable(current) || (selected != "" && selected != current) {
				return Step{State: state}
			}
			state.Bag = nil
			return Step{State: state, EntryID: current}
		}
	}
	entryID := selected
	if entryID == "" {
		entryID = bag[min(len(bag)-1, int(env.Random()*float64(len(bag))))]
	} else if !slices.Contains(bag, entryID) {
		return Step{State: state}
	}
	state.Bag = filterIDs(bag, func(id string) bool { return id != entryID })
	return Step{State: state, EntryID: entryID}
}

// Retreat 随机模式沿实际历史后退，并将离开的当前项放回袋子；其他模式取列表上一项。
// 没有上一项时返回当前项供调用方从头播放；没有当前项则返回空 EntryID。
func Retreat(state State, isSkipped func(string) bool) Step {
	current := state.CurrentEntryID
	if current == "" {
		return Step{State: state}
	}
	if state.Mode == ModeShuffle {
		history := slices.Clone(state.History)
		for len(history) > 0 {
			previous := history[len(history)-1]
			history = history[:len(history)-1]
			if previous != current && indexOfEntry(state.Entries, previous) >= 0 && !skipped(previous, isSkipped) {
				bag := filterIDs(state.Bag, func(id string) bool { return id != previous && id != current })
				state.CurrentEntryID = previous
				state.History = history
				state.Bag = append(bag, current)
				return Step{State: state, EntryID: previous}
			}
		}
		state.History = nil
		return Step{State: state, EntryID: current}
	}
	entries := state.Entries
	at := indexOfEntry(entries, current)
	for step := 1; step < len(entries); step++ {
		index := at - step
		if index < 0 && state.Mode != ModeRepeatAll {
			break
		}
		candidate := entries[(index+len(entries))%len(entries)]
		if !skipped(candidate.EntryID, isSkipped) {
			state.CurrentEntryID = candidate.EntryID
			state.History = pushHistory(state.History, current)
			return Step{State: state, EntryID: candidate.EntryID}
		}
	}
	return Step{State: state, EntryID: current}
}
