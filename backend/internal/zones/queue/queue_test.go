package queue

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// 条目 ID 依次为 e1、e2……；随机数按给定序列循环。
func makeEnv(randoms ...float64) Env {
	if len(randoms) == 0 {
		randoms = []float64{0}
	}
	counter, calls := 0, 0
	return Env{
		NewEntryID: func() string { counter++; return fmt.Sprintf("e%d", counter) },
		Random:     func() float64 { value := randoms[calls%len(randoms)]; calls++; return value },
	}
}

func queueOf(trackIDs []string, current int, mode Mode, env Env) State {
	return ReplaceQueue(State{Mode: mode}, trackIDs, current, env)
}

func entryIDs(entries []Entry) []string {
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.EntryID
	}
	return ids
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("实际值 = %v，期望 %v", got, want)
	}
}

func assertIDs(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("实际列表 = %v，期望 %v", got, want)
	}
}

func assertState(t *testing.T, got, want State) {
	t.Helper()
	if got.Mode != want.Mode || got.CurrentEntryID != want.CurrentEntryID ||
		!slices.Equal(got.Entries, want.Entries) || !slices.Equal(got.History, want.History) || !slices.Equal(got.Bag, want.Bag) {
		t.Fatalf("实际状态 = %+v，期望 %+v", got, want)
	}
}

func assertTracks(t *testing.T, state State, want ...string) {
	t.Helper()
	got := make([]string, len(state.Entries))
	for i, entry := range state.Entries {
		got[i] = entry.TrackID
	}
	assertIDs(t, got, want)
}

func mustAdd(t *testing.T, state State, err error) State {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// 逐条对应 frontend/src/features/player/engine/queue.test.ts；标题断言改为 TrackID。
func TestZonesQueue(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"替换/用新曲目替换整个队列，从选中的一项开始", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 1, ModeSequential, makeEnv())
			assertTracks(t, state, "a", "b", "c")
			assertEqual(t, state.CurrentEntryID, "e2")
			assertIDs(t, state.History, nil)
		}},
		{"替换/保留播放模式，旧队列与历史被丢弃", func(t *testing.T) {
			env := makeEnv()
			first := queueOf([]string{"a", "b"}, 0, ModeRepeatAll, env)
			first.History = []string{"e1"}
			second := ReplaceQueue(first, []string{"x"}, 0, env)
			assertEqual(t, second.Mode, ModeRepeatAll)
			assertTracks(t, second, "x")
			assertIDs(t, second.History, nil)
		}},
		{"替换/起点越界时取第一项；没有曲目得到空队列", func(t *testing.T) {
			assertEqual(t, queueOf([]string{"a", "b"}, 9, ModeSequential, makeEnv()).CurrentEntryID, "e1")
			assertState(t, queueOf(nil, 0, ModeSequential, makeEnv()), EmptyQueue())
		}},
		{"替换/同一首曲目多次出现时，每个条目有自己的ID", func(t *testing.T) {
			state := ReplaceQueue(EmptyQueue(), []string{"a", "a"}, 0, makeEnv())
			assertEqual(t, len(state.Entries), 2)
			if state.Entries[0].EntryID == state.Entries[1].EntryID {
				t.Fatal("重复曲目必须有独立条目ID")
			}
			assertEqual(t, state.Entries[0].TrackID, state.Entries[1].TrackID)
		}},
		{"加入/下一首播放插在当前条目之后，不改变当前条目", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b", "c"}, 1, ModeSequential, env)
			next, err := InsertAfterCurrent(state, []string{"x", "y"}, env)
			next = mustAdd(t, next, err)
			assertTracks(t, next, "a", "b", "x", "y", "c")
			assertEqual(t, next.CurrentEntryID, "e2")
		}},
		{"加入/没有当前条目时下一首播放放在队首", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 0, ModeSequential, env)
			state.CurrentEntryID = ""
			next, err := InsertAfterCurrent(state, []string{"x"}, env)
			next = mustAdd(t, next, err)
			assertEqual(t, next.Entries[0].TrackID, "x")
			assertEqual(t, next.CurrentEntryID, "")
		}},
		{"加入/加入队列追加到队尾，不打断当前播放", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 1, ModeSequential, env)
			next, err := AppendEntries(state, []string{"x"}, env)
			next = mustAdd(t, next, err)
			assertTracks(t, next, "a", "b", "x")
			assertEqual(t, next.CurrentEntryID, "e2")
		}},
		{"加入/队列为空时，第一个加入的条目成为当前条目", func(t *testing.T) {
			env := makeEnv()
			for _, add := range []func(State, []string, Env) (State, error){InsertAfterCurrent, AppendEntries} {
				next, err := add(EmptyQueue(), []string{"x", "y"}, env)
				next = mustAdd(t, next, err)
				assertEqual(t, next.CurrentEntryID, next.Entries[0].EntryID)
			}
		}},
		{"加入/加入空列表不改变队列", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a"}, 0, ModeSequential, env)
			for _, add := range []func(State, []string, Env) (State, error){AppendEntries, InsertAfterCurrent} {
				next, err := add(state, nil, env)
				assertState(t, mustAdd(t, next, err), state)
			}
		}},
		{"加入/容量上限10000：刚好装满可以，再多一首整批拒绝", func(t *testing.T) {
			env := makeEnv()
			tracks := make([]string, MaxQueueEntries-2)
			for i := range tracks {
				tracks[i] = fmt.Sprint(i)
			}
			full := ReplaceQueue(EmptyQueue(), tracks, 0, env)
			fits, err := AppendEntries(full, []string{"x", "y"}, env)
			fits = mustAdd(t, fits, err)
			assertEqual(t, len(fits.Entries), MaxQueueEntries)
			for _, add := range []func(State, []string, Env) (State, error){AppendEntries, InsertAfterCurrent} {
				next, err := add(full, []string{"x", "y", "z"}, env)
				if !errors.Is(err, ErrQueueFull) {
					t.Fatalf("错误 = %v，期望 ErrQueueFull", err)
				}
				assertState(t, next, full)
			}
			next, err := AppendEntries(fits, []string{"w"}, env)
			if !errors.Is(err, ErrQueueFull) {
				t.Fatalf("错误 = %v，期望 ErrQueueFull", err)
			}
			assertState(t, next, fits)
		}},
		{"加入/随机模式下新加入的条目进入袋子，当前条目除外", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 0, ModeShuffle, env)
			next, err := AppendEntries(state, []string{"x"}, env)
			assertIDs(t, mustAdd(t, next, err).Bag, []string{"e2", "e3"})
		}},
		{"移除/按条目ID移除：同一首曲目的其他条目留在队列里", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "a", "b"}, 2, ModeSequential, env)
			result := RemoveEntry(state, "e1", env)
			assertEqual(t, result.RemovedCurrent, false)
			assertIDs(t, entryIDs(result.State.Entries), []string{"e2", "e3"})
			assertEqual(t, result.State.CurrentEntryID, "e3")
		}},
		{"移除/移除当前条目：后继是原来的下一项", func(t *testing.T) {
			env := makeEnv()
			result := RemoveEntry(queueOf([]string{"a", "b", "c"}, 1, ModeSequential, env), "e2", env)
			assertEqual(t, result.RemovedCurrent, true)
			assertIDs(t, entryIDs(result.State.Entries), []string{"e1", "e3"})
			assertEqual(t, result.State.CurrentEntryID, "e3")
		}},
		{"移除/移除末项的当前条目：顺序模式停在空闲，列表循环回到首项", func(t *testing.T) {
			env := makeEnv()
			stopped := RemoveEntry(queueOf([]string{"a", "b"}, 1, ModeSequential, env), "e2", env)
			assertEqual(t, stopped.State.CurrentEntryID, "")
			assertIDs(t, entryIDs(stopped.State.Entries), []string{"e1"})
			env = makeEnv()
			looping := RemoveEntry(queueOf([]string{"a", "b"}, 1, ModeRepeatAll, env), "e2", env)
			assertEqual(t, looping.State.CurrentEntryID, "e1")
		}},
		{"移除/移除唯一的条目得到空队列", func(t *testing.T) {
			env := makeEnv()
			result := RemoveEntry(queueOf([]string{"a"}, 0, ModeSequential, env), "e1", env)
			assertState(t, result.State, EmptyQueue())
		}},
		{"移除/随机模式移除当前条目：后继从袋子里选", func(t *testing.T) {
			env := makeEnv(0.99)
			result := RemoveEntry(queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, env), "e1", env)
			assertEqual(t, result.State.CurrentEntryID, "e3")
			assertIDs(t, result.State.Bag, []string{"e2"})
		}},
		{"移除/不存在的条目ID不改变队列；历史与袋子里的该条目被清掉", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, env)
			assertState(t, RemoveEntry(state, "nope", env).State, state)
			state.History = []string{"e2", "e3"}
			result := RemoveEntry(state, "e3", env)
			assertIDs(t, result.State.History, []string{"e2"})
			assertIDs(t, result.State.Bag, []string{"e2"})
		}},
		{"编辑/上移与下移交换相邻条目，两端不动，当前条目不变", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 1, ModeSequential, makeEnv())
			assertIDs(t, entryIDs(MoveEntry(state, "e2", -1).Entries), []string{"e2", "e1", "e3"})
			assertIDs(t, entryIDs(MoveEntry(state, "e2", 1).Entries), []string{"e1", "e3", "e2"})
			assertState(t, MoveEntry(state, "e1", -1), state)
			assertState(t, MoveEntry(state, "e3", 1), state)
			assertState(t, MoveEntry(state, "nope", 1), state)
			assertEqual(t, MoveEntry(state, "e2", -1).CurrentEntryID, "e2")
		}},
		{"编辑/清空队列保留播放模式", func(t *testing.T) {
			state := queueOf([]string{"a", "b"}, 0, ModeRepeatOne, makeEnv())
			assertState(t, ClearQueue(state), State{Mode: ModeRepeatOne})
		}},
		{"编辑/进入随机模式装袋，离开时清袋，模式不变时原样返回", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 1, ModeSequential, makeEnv())
			state.History = []string{"e1"}
			shuffled := SetMode(state, ModeShuffle)
			assertIDs(t, shuffled.Bag, []string{"e3"})
			assertIDs(t, SetMode(shuffled, ModeSequential).Bag, nil)
			assertState(t, SetMode(shuffled, ModeShuffle), shuffled)
		}},
		{"编辑/用户选中条目：成为当前，原来的当前条目进入历史", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, makeEnv())
			jumped := JumpTo(state, "e3")
			assertEqual(t, jumped.CurrentEntryID, "e3")
			assertIDs(t, jumped.History, []string{"e1"})
			assertIDs(t, jumped.Bag, []string{"e2"})
			assertState(t, JumpTo(jumped, "e3"), jumped)
			assertState(t, JumpTo(jumped, "nope"), jumped)
		}},
		{"前进/顺序：往后一项，末项自然结束后没有下一项", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 0, ModeSequential, env)
			first := Advance(state, env, AdvanceOptions{Reason: ReasonNatural})
			assertEqual(t, first.EntryID, "e2")
			assertEqual(t, first.State.CurrentEntryID, "e2")
			assertIDs(t, first.State.History, []string{"e1"})
			end := Advance(first.State, env, AdvanceOptions{Reason: ReasonNatural})
			assertEqual(t, end.EntryID, "")
			assertState(t, end.State, first.State)
		}},
		{"前进/列表循环：末项之后回到首项，单曲队列再播它自己", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 1, ModeRepeatAll, env)
			for _, reason := range []AdvanceReason{ReasonNatural, ReasonManual} {
				assertEqual(t, Advance(state, env, AdvanceOptions{Reason: reason}).EntryID, "e1")
			}
			env = makeEnv()
			single := queueOf([]string{"a"}, 0, ModeRepeatAll, env)
			assertEqual(t, Advance(single, env, AdvanceOptions{Reason: ReasonNatural}).EntryID, "e1")
		}},
		{"前进/单曲循环只影响自然结束；手动下一首按顺序，末项之后没有", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 0, ModeRepeatOne, env)
			natural := Advance(state, env, AdvanceOptions{Reason: ReasonNatural})
			assertEqual(t, natural.EntryID, "e1")
			assertState(t, natural.State, state)
			assertEqual(t, Advance(state, env, AdvanceOptions{Reason: ReasonManual}).EntryID, "e2")
			last := queueOf([]string{"a", "b"}, 1, ModeRepeatOne, makeEnv())
			assertEqual(t, Advance(last, env, AdvanceOptions{Reason: ReasonManual}).EntryID, "")
		}},
		{"前进/空队列没有下一项", func(t *testing.T) {
			assertEqual(t, Advance(EmptyQueue(), makeEnv(), AdvanceOptions{Reason: ReasonNatural}).EntryID, "")
		}},
		{"前进/跳过已知失败的条目；后面全部失败时没有下一项", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b", "c"}, 0, ModeSequential, env)
			assertEqual(t, Advance(state, env, AdvanceOptions{Reason: ReasonNatural, IsSkipped: func(id string) bool { return id == "e2" }}).EntryID, "e3")
			skipRest := AdvanceOptions{Reason: ReasonNatural, IsSkipped: func(id string) bool { return id != "e1" }}
			assertEqual(t, Advance(state, env, skipRest).EntryID, "")
			looping := queueOf([]string{"a", "b", "c"}, 0, ModeRepeatAll, makeEnv())
			assertEqual(t, Advance(looping, env, skipRest).EntryID, "")
		}},
		{"前进/没有当前条目时前进到第一项", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b"}, 0, ModeSequential, env)
			state.CurrentEntryID = ""
			assertEqual(t, Advance(state, env, AdvanceOptions{Reason: ReasonManual}).EntryID, "e1")
		}},
		{"随机/多于一项时不连续重复，一轮内每个条目恰好出现一次", func(t *testing.T) {
			env := makeEnv(0.1, 0.9, 0.5, 0.3, 0.7, 0.2, 0.8)
			state := queueOf([]string{"a", "b", "c", "d", "e"}, 0, ModeShuffle, env)
			played := map[string]bool{state.CurrentEntryID: true}
			for step := 0; step < 4; step++ {
				next := Advance(state, env, AdvanceOptions{Reason: ReasonNatural})
				if next.EntryID == "" {
					t.Fatal("随机前进没有结果")
				}
				state = next.State
				played[next.EntryID] = true
			}
			assertEqual(t, len(played), 5)
		}},
		{"随机/袋子空了重新装袋，轮与轮的交界也不连续重复", func(t *testing.T) {
			env := makeEnv(0, 0.99, 0.5)
			state := queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, env)
			for step := 0; step < 40; step++ {
				next := Advance(state, env, AdvanceOptions{Reason: ReasonNatural})
				if next.EntryID == "" || next.EntryID == state.CurrentEntryID {
					t.Fatalf("第%d步结果 = %q，前项 = %q", step, next.EntryID, state.CurrentEntryID)
				}
				state = next.State
			}
		}},
		{"随机/只有一项时再播它自己", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a"}, 0, ModeShuffle, env)
			assertEqual(t, Advance(state, env, AdvanceOptions{Reason: ReasonNatural}).EntryID, "e1")
		}},
		{"随机/跳过失败的条目", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, env)
			for step := 0; step < 10; step++ {
				assertEqual(t, Advance(state, env, AdvanceOptions{Reason: ReasonManual, IsSkipped: func(id string) bool { return id == "e2" }}).EntryID, "e3")
			}
			assertEqual(t, Advance(state, env, AdvanceOptions{Reason: ReasonManual, IsSkipped: func(id string) bool { return id != "e1" }}).EntryID, "e1")
		}},
		{"后退/顺序：回到列表里的上一项；队首时返回当前条目", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 2, ModeSequential, makeEnv())
			back := Retreat(state, nil)
			assertEqual(t, back.EntryID, "e2")
			assertEqual(t, back.State.CurrentEntryID, "e2")
			atStart := queueOf([]string{"a", "b"}, 0, ModeSequential, makeEnv())
			assertEqual(t, Retreat(atStart, nil).EntryID, "e1")
			assertState(t, Retreat(atStart, nil).State, atStart)
		}},
		{"后退/列表循环：队首后退绕到末项", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 0, ModeRepeatAll, makeEnv())
			assertEqual(t, Retreat(state, nil).EntryID, "e3")
		}},
		{"后退/单曲循环的手动后退照常切换", func(t *testing.T) {
			state := queueOf([]string{"a", "b"}, 1, ModeRepeatOne, makeEnv())
			assertEqual(t, Retreat(state, nil).EntryID, "e1")
		}},
		{"后退/随机：沿实际播放历史返回，而不是再随机一次", func(t *testing.T) {
			env := makeEnv(0.7, 0.2)
			state := queueOf([]string{"a", "b", "c", "d"}, 0, ModeShuffle, env)
			order := []string{state.CurrentEntryID}
			for step := 0; step < 3; step++ {
				next := Advance(state, env, AdvanceOptions{Reason: ReasonManual})
				state = next.State
				order = append(order, next.EntryID)
			}
			for step := 2; step >= 0; step-- {
				back := Retreat(state, nil)
				assertEqual(t, back.EntryID, order[step])
				state = back.State
			}
		}},
		{"后退/随机：后退离开的条目放回袋子，再前进时本轮仍会轮到它", func(t *testing.T) {
			env := makeEnv()
			state := queueOf([]string{"a", "b", "c", "d"}, 0, ModeShuffle, env)
			state = Advance(state, env, AdvanceOptions{Reason: ReasonManual}).State
			assertEqual(t, state.CurrentEntryID, "e2")
			back := Retreat(state, nil)
			assertEqual(t, back.EntryID, "e1")
			assertIDs(t, back.State.Bag, []string{"e3", "e4", "e2"})
			state = back.State
			var visited []string
			for step := 0; step < 3; step++ {
				next := Advance(state, env, AdvanceOptions{Reason: ReasonManual})
				state = next.State
				visited = append(visited, next.EntryID)
			}
			assertIDs(t, visited, []string{"e3", "e4", "e2"})
			assertIDs(t, state.Bag, nil)
		}},
		{"后退/随机且没有历史时返回当前条目；历史里已被移除的条目被跳过", func(t *testing.T) {
			state := queueOf([]string{"a", "b"}, 0, ModeShuffle, makeEnv())
			assertEqual(t, Retreat(state, nil).EntryID, "e1")
			state.CurrentEntryID, state.History = "e2", []string{"gone", "e1"}
			assertEqual(t, Retreat(state, nil).EntryID, "e1")
			state.History = []string{"gone"}
			assertEqual(t, Retreat(state, nil).EntryID, "e2")
		}},
		{"后退/没有当前条目时没有上一项", func(t *testing.T) {
			assertEqual(t, Retreat(EmptyQueue(), nil).EntryID, "")
		}},
		{"后退/跳过已知失败的上一项", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c"}, 2, ModeSequential, makeEnv())
			assertEqual(t, Retreat(state, func(id string) bool { return id == "e2" }).EntryID, "e1")
			assertEqual(t, Retreat(state, func(string) bool { return true }).EntryID, "e3")
		}},
		{"读取/顺序与单曲循环的末项没有下一首，其他模式只要有条目", func(t *testing.T) {
			for _, tc := range []struct {
				tracks  []string
				current int
				mode    Mode
				want    bool
			}{
				{[]string{"a", "b"}, 0, ModeSequential, true},
				{[]string{"a", "b"}, 1, ModeSequential, false},
				{[]string{"a", "b"}, 1, ModeRepeatOne, false},
				{[]string{"a", "b"}, 1, ModeRepeatAll, true},
				{[]string{"a"}, 0, ModeShuffle, true},
				{nil, 0, ModeSequential, false},
			} {
				assertEqual(t, HasNext(queueOf(tc.tracks, tc.current, tc.mode, makeEnv())), tc.want)
			}
		}},
		{"读取/currentEntry取当前条目；upNext取其后条目，随机模式取袋子", func(t *testing.T) {
			state := queueOf([]string{"a", "b", "c", "d"}, 1, ModeSequential, makeEnv())
			current, ok := CurrentEntry(state)
			assertEqual(t, ok, true)
			assertEqual(t, current.TrackID, "b")
			assertIDs(t, entryIDs(UpNext(state, 2)), []string{"e3", "e4"})
			_, ok = CurrentEntry(EmptyQueue())
			assertEqual(t, ok, false)
			shuffled := queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, makeEnv())
			assertIDs(t, entryIDs(UpNext(shuffled, 5)), []string{"e2", "e3"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestZonesQueuePeekNaturalAdvanceToMatchesAdvance(t *testing.T) {
	// 两套独立的同序随机源，走完初始袋及耗尽后重装的一轮。
	peekEnv, directEnv := makeEnv(0.8, 0.1, 0.6), makeEnv(0.8, 0.1, 0.6)
	state := queueOf([]string{"a", "b", "c", "d"}, 0, ModeShuffle, peekEnv)
	for step := 0; step < 6; step++ {
		before := state
		before.Entries, before.History, before.Bag = slices.Clone(state.Entries), slices.Clone(state.History), slices.Clone(state.Bag)
		selected := PeekNatural(state, peekEnv, nil)
		if !reflect.DeepEqual(state, before) {
			t.Fatal("预选修改了输入状态")
		}
		direct := Advance(state, directEnv, AdvanceOptions{Reason: ReasonNatural})
		actual := AdvanceTo(state, selected, nil)
		if selected == "" {
			t.Fatalf("第%d步预选为空", step)
		}
		assertEqual(t, actual.EntryID, direct.EntryID)
		assertEqual(t, actual.EntryID, selected)
		assertState(t, actual.State, direct.State)
		assertEqual(t, actual.State.CurrentEntryID, selected)
		assertIDs(t, actual.State.History, append(slices.Clone(before.History), before.CurrentEntryID))
		if step == 2 || step == 5 {
			assertEqual(t, len(actual.State.Bag), 0)
		}
		if step == 3 {
			assertEqual(t, len(actual.State.Bag), 2)
		}
		state = actual.State
	}
}

func TestZonesQueueRemovedPreselectionCanBeReselected(t *testing.T) {
	env := makeEnv()
	state := queueOf([]string{"a", "b", "c"}, 0, ModeShuffle, env)
	selected := PeekNatural(state, env, nil)
	assertEqual(t, selected, "e2")
	state = RemoveEntry(state, selected, env).State
	stale := AdvanceTo(state, selected, nil)
	assertEqual(t, stale.EntryID, "")
	assertState(t, stale.State, state)
	next := PeekNatural(state, env, nil)
	assertEqual(t, next, "e3")
	assertEqual(t, AdvanceTo(state, next, nil).EntryID, next)
}
