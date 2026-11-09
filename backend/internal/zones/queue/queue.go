package queue

import (
	"errors"
	"slices"
)

// ErrQueueFull 表示加入操作超过容量，整批拒绝且不生成条目 ID。
var ErrQueueFull = errors.New("queue_full")

// ReplaceQueue 替换整个队列，保留模式；起点越界取首项，空输入清空。
// 超过容量时原样返回，沿用客户端 replaceQueue 的行为。
func ReplaceQueue(state State, trackIDs []string, startIndex int, env Env) State {
	if len(trackIDs) > MaxQueueEntries {
		return state
	}
	entries := make([]Entry, len(trackIDs))
	for i, trackID := range trackIDs {
		entries[i] = entryOf(trackID, env)
	}
	current := ""
	if len(entries) > 0 {
		if startIndex < 0 || startIndex >= len(entries) {
			startIndex = 0
		}
		current = entries[startIndex].EntryID
	}
	return State{
		Entries: entries, CurrentEntryID: current, Mode: state.Mode,
		Bag: freshBag(entries, current, state.Mode, nil),
	}
}

// InsertAfterCurrent 插在当前项之后，无当前项则插在队首；超限返回原状态与 ErrQueueFull。
func InsertAfterCurrent(state State, trackIDs []string, env Env) (State, error) {
	if len(trackIDs) == 0 {
		return state, nil
	}
	if len(state.Entries)+len(trackIDs) > MaxQueueEntries {
		return state, ErrQueueFull
	}
	added := make([]Entry, len(trackIDs))
	for i, trackID := range trackIDs {
		added[i] = entryOf(trackID, env)
	}
	at := indexOfEntry(state.Entries, state.CurrentEntryID) + 1
	entries := make([]Entry, 0, len(state.Entries)+len(added))
	entries = append(entries, state.Entries[:at]...)
	entries = append(entries, added...)
	entries = append(entries, state.Entries[at:]...)
	return withAdded(state, entries, added), nil
}

// AppendEntries 追加到队尾，不打断当前播放；超限返回原状态与 ErrQueueFull。
func AppendEntries(state State, trackIDs []string, env Env) (State, error) {
	if len(trackIDs) == 0 {
		return state, nil
	}
	if len(state.Entries)+len(trackIDs) > MaxQueueEntries {
		return state, ErrQueueFull
	}
	added := make([]Entry, len(trackIDs))
	for i, trackID := range trackIDs {
		added[i] = entryOf(trackID, env)
	}
	entries := make([]Entry, 0, len(state.Entries)+len(added))
	entries = append(entries, state.Entries...)
	entries = append(entries, added...)
	return withAdded(state, entries, added), nil
}

func withAdded(state State, entries, added []Entry) State {
	if len(state.Entries) == 0 && len(added) > 0 {
		state.CurrentEntryID = added[0].EntryID
	}
	state.Entries = entries
	if state.Mode == ModeShuffle {
		bag := slices.Clone(state.Bag)
		for _, entry := range added {
			if entry.EntryID != state.CurrentEntryID {
				bag = append(bag, entry.EntryID)
			}
		}
		state.Bag = bag
	}
	return state
}

// RemoveResult 的 RemovedCurrent 提醒调用方停止旧条目，在新的当前项继续或停在空闲。
type RemoveResult struct {
	State          State
	RemovedCurrent bool
}

// RemoveEntry 按条目 ID 移除，同一曲目的其他条目不受影响。
func RemoveEntry(state State, entryID string, env Env) RemoveResult {
	index := indexOfEntry(state.Entries, entryID)
	if index < 0 {
		return RemoveResult{State: state}
	}
	entries := make([]Entry, 0, len(state.Entries)-1)
	for _, entry := range state.Entries {
		if entry.EntryID != entryID {
			entries = append(entries, entry)
		}
	}
	next := state
	next.Entries = entries
	next.History = filterIDs(state.History, func(id string) bool { return id != entryID })
	next.Bag = filterIDs(state.Bag, func(id string) bool { return id != entryID })
	removedCurrent := state.CurrentEntryID == entryID
	if removedCurrent {
		next.CurrentEntryID = successorAfterRemoval(state, index, entries, env)
		if next.CurrentEntryID != "" {
			next.Bag = filterIDs(next.Bag, func(id string) bool { return id != next.CurrentEntryID })
		}
	}
	return RemoveResult{State: next, RemovedCurrent: removedCurrent}
}

func successorAfterRemoval(before State, removedIndex int, entries []Entry, env Env) string {
	if len(entries) == 0 {
		return ""
	}
	if before.Mode == ModeShuffle {
		ids := make(map[string]bool, len(entries))
		for _, entry := range entries {
			ids[entry.EntryID] = true
		}
		pool := filterIDs(before.Bag, func(id string) bool { return id != before.CurrentEntryID && ids[id] })
		if len(pool) == 0 {
			for _, entry := range entries {
				pool = append(pool, entry.EntryID)
			}
		}
		return pool[int(env.Random()*float64(len(pool)))]
	}
	if removedIndex < len(entries) {
		return entries[removedIndex].EntryID
	}
	if before.Mode == ModeRepeatAll {
		return entries[0].EntryID
	}
	return ""
}

// MoveEntry 交换相邻条目；delta 由调用方校验为 -1 或 1，两端不移动。
func MoveEntry(state State, entryID string, delta int) State {
	index := indexOfEntry(state.Entries, entryID)
	target := index + delta
	if index < 0 || target < 0 || target >= len(state.Entries) {
		return state
	}
	entries := slices.Clone(state.Entries)
	entries[index], entries[target] = entries[target], entries[index]
	state.Entries = entries
	return state
}

// ClearQueue 清空条目、当前项、历史与袋子，保留模式。
func ClearQueue(state State) State {
	return State{Mode: state.Mode}
}

// SetMode 进入随机模式时重新装袋，排除当前项与历史；离开时清袋。
func SetMode(state State, mode Mode) State {
	if state.Mode == mode {
		return state
	}
	state.Mode = mode
	state.Bag = freshBag(state.Entries, state.CurrentEntryID, mode, state.History)
	return state
}

// JumpTo 明确选中一个条目，原当前项进入历史；不存在或已是当前项时不变。
func JumpTo(state State, entryID string) State {
	if indexOfEntry(state.Entries, entryID) < 0 || state.CurrentEntryID == entryID {
		return state
	}
	state.History = pushHistory(state.History, state.CurrentEntryID)
	state.CurrentEntryID = entryID
	state.Bag = filterIDs(state.Bag, func(id string) bool { return id != entryID })
	return state
}
