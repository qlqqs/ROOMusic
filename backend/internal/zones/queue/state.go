// Package queue 提供播放队列的纯状态转换，不依赖存储、目录投影或渲染器。
// 返回值可与输入共享未改变的切片；调用方应将状态视为只读，所有修改经本包函数完成。
package queue

import "slices"

// Mode 的字符串与客户端及持久化合同一致。
type Mode string

const (
	ModeSequential Mode = "sequential"
	ModeRepeatAll  Mode = "repeat_all"
	ModeRepeatOne  Mode = "repeat_one"
	ModeShuffle    Mode = "shuffle"

	MaxQueueEntries = 10_000
	MaxHistory      = 500
)

// PlayModes 按客户端的模式切换顺序返回四种模式。
func PlayModes() []Mode {
	return []Mode{ModeSequential, ModeRepeatAll, ModeRepeatOne, ModeShuffle}
}

// Entry 区分曲目身份与入队位置；同一曲目可以有多个独立条目。
type Entry struct {
	EntryID string
	TrackID string
}

// State 只保存队列语义；CurrentEntryID 为空表示没有当前条目。
type State struct {
	Entries        []Entry
	CurrentEntryID string
	Mode           Mode
	// History 最近播放过的条目在末尾，最多保留 MaxHistory 项。
	History []string
	// Bag 是随机模式本轮尚未播放的条目 ID。
	Bag []string
}

// Env 由调用方注入：NewEntryID 生成非空唯一 ID，Random 返回 [0, 1)。
// 只有创建条目的操作调用 NewEntryID，只有随机选曲调用 Random。
type Env struct {
	NewEntryID func() string
	Random     func() float64
}

// EmptyQueue 返回顺序模式的空队列，不保留可变的全局状态。
func EmptyQueue() State {
	return State{Mode: ModeSequential}
}

// CurrentEntry 返回当前条目的值；不存在时第二个返回值为 false。
func CurrentEntry(state State) (Entry, bool) {
	at := indexOfEntry(state.Entries, state.CurrentEntryID)
	if at < 0 {
		return Entry{}, false
	}
	return state.Entries[at], true
}

// HasNext 对应手动“下一首”：顺序和单曲循环的末项不可用。
func HasNext(state State) bool {
	if len(state.Entries) == 0 {
		return false
	}
	if state.Mode == ModeRepeatAll || state.Mode == ModeShuffle {
		return true
	}
	at := indexOfEntry(state.Entries, state.CurrentEntryID)
	return at < len(state.Entries)-1
}

// UpNext 返回“接下来”的条目；随机模式按袋子列出，不承诺实际播放顺序。
// limit 沿用源函数的 Array.slice 结束索引语义，通常由调用方传非负值。
func UpNext(state State, limit int) []Entry {
	if state.Mode == ModeShuffle {
		byID := make(map[string]Entry, len(state.Entries))
		for _, entry := range state.Entries {
			byID[entry.EntryID] = entry
		}
		entries := make([]Entry, 0, len(state.Bag))
		for _, id := range state.Bag {
			if entry, ok := byID[id]; ok {
				entries = append(entries, entry)
			}
		}
		return entries[:sliceEnd(limit, len(entries))]
	}
	start := indexOfEntry(state.Entries, state.CurrentEntryID) + 1
	end := sliceEnd(start+limit, len(state.Entries))
	if end < start {
		end = start
	}
	return slices.Clone(state.Entries[start:end])
}

func sliceEnd(end, length int) int {
	if end < 0 {
		return max(length+end, 0)
	}
	return min(end, length)
}

func entryOf(trackID string, env Env) Entry {
	return Entry{EntryID: env.NewEntryID(), TrackID: trackID}
}

func indexOfEntry(entries []Entry, entryID string) int {
	if entryID == "" {
		return -1
	}
	for i, entry := range entries {
		if entry.EntryID == entryID {
			return i
		}
	}
	return -1
}

func freshBag(entries []Entry, currentEntryID string, mode Mode, exclude []string) []string {
	if mode != ModeShuffle {
		return nil
	}
	excluded := make(map[string]bool, len(exclude))
	for _, id := range exclude {
		excluded[id] = true
	}
	bag := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.EntryID != currentEntryID && !excluded[entry.EntryID] {
			bag = append(bag, entry.EntryID)
		}
	}
	return bag
}

func pushHistory(history []string, entryID string) []string {
	if entryID == "" {
		return history
	}
	next := append(slices.Clone(history), entryID)
	if len(next) > MaxHistory {
		return next[len(next)-MaxHistory:]
	}
	return next
}

func filterIDs(ids []string, keep func(string) bool) []string {
	next := make([]string, 0, len(ids))
	for _, id := range ids {
		if keep(id) {
			next = append(next, id)
		}
	}
	return next
}

func skipped(entryID string, isSkipped func(string) bool) bool {
	return isSkipped != nil && isSkipped(entryID)
}
