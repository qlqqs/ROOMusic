import { describe, expect, it } from "vitest";
import { playerTrack } from "../../../test/player-data";
import {
  advance,
  appendEntries,
  clearQueue,
  currentEntry,
  emptyQueue,
  hasNext,
  insertAfterCurrent,
  jumpTo,
  MAX_QUEUE_ENTRIES,
  moveEntry,
  removeEntry,
  replaceQueue,
  retreat,
  setMode,
  upNext,
  type QueueEnv,
  type QueueState,
} from "./queue";

const track = (name: string) => playerTrack(name);

/** 条目 ID 依次是 e1、e2……；随机数取自给定序列并循环。 */
function makeEnv(randoms: readonly number[] = [0]): QueueEnv {
  let counter = 0;
  let calls = 0;
  return {
    newEntryId: () => `e${++counter}`,
    random: () => randoms[calls++ % randoms.length] ?? 0,
  };
}

function queueOf(names: readonly string[], options: { current?: number; mode?: QueueState["mode"]; env?: QueueEnv } = {}): { state: QueueState; env: QueueEnv } {
  const env = options.env ?? makeEnv();
  const base = { ...emptyQueue, mode: options.mode ?? "sequential" };
  return { state: replaceQueue(base, names.map(track), options.current ?? 0, env), env };
}

const ids = (state: QueueState) => state.entries.map((entry) => entry.entryId);
const titles = (state: QueueState) => state.entries.map((entry) => entry.track.title);

describe("replaceQueue（立即播放）", () => {
  it("用新曲目替换整个队列，从选中的一项开始", () => {
    const { state } = queueOf(["a", "b", "c"], { current: 1 });
    expect(titles(state)).toEqual(["曲目 a", "曲目 b", "曲目 c"]);
    expect(state.currentEntryId).toBe("e2");
    expect(state.history).toEqual([]);
  });

  it("保留播放模式，旧队列与历史被丢弃", () => {
    const env = makeEnv();
    const first = replaceQueue({ ...emptyQueue, mode: "repeat_all" }, [track("a"), track("b")], 0, env);
    const second = replaceQueue({ ...first, history: ["e1"] }, [track("x")], 0, env);
    expect(second.mode).toBe("repeat_all");
    expect(titles(second)).toEqual(["曲目 x"]);
    expect(second.history).toEqual([]);
  });

  it("起点越界时取第一项；没有曲目得到空队列", () => {
    expect(queueOf(["a", "b"], { current: 9 }).state.currentEntryId).toBe("e1");
    expect(queueOf([]).state).toMatchObject({ entries: [], currentEntryId: null });
  });

  it("同一首曲目多次出现时，每个条目有自己的 ID", () => {
    const env = makeEnv();
    const state = replaceQueue(emptyQueue, [track("a"), track("a")], 0, env);
    expect(new Set(ids(state)).size).toBe(2);
    expect(state.entries[0]?.track.trackId).toBe(state.entries[1]?.track.trackId);
  });
});

describe("insertAfterCurrent（下一首播放）与 appendEntries（加入队列）", () => {
  it("下一首播放插在当前条目之后，不改变当前条目", () => {
    const { state, env } = queueOf(["a", "b", "c"], { current: 1 });
    const result = insertAfterCurrent(state, [track("x"), track("y")], env);
    if (!result.ok) throw new Error("应当成功");
    expect(titles(result.state)).toEqual(["曲目 a", "曲目 b", "曲目 x", "曲目 y", "曲目 c"]);
    expect(result.state.currentEntryId).toBe("e2");
  });

  it("没有当前条目时下一首播放放在队首", () => {
    const { state, env } = queueOf(["a", "b"]);
    const idle = { ...state, currentEntryId: null };
    const result = insertAfterCurrent(idle, [track("x")], env);
    if (!result.ok) throw new Error("应当成功");
    expect(titles(result.state)[0]).toBe("曲目 x");
    expect(result.state.currentEntryId).toBeNull();
  });

  it("加入队列追加到队尾，不打断当前播放", () => {
    const { state, env } = queueOf(["a", "b"], { current: 1 });
    const result = appendEntries(state, [track("x")], env);
    if (!result.ok) throw new Error("应当成功");
    expect(titles(result.state)).toEqual(["曲目 a", "曲目 b", "曲目 x"]);
    expect(result.state.currentEntryId).toBe("e2");
  });

  it("队列为空时，第一个加入的条目成为当前条目", () => {
    const env = makeEnv();
    for (const add of [insertAfterCurrent, appendEntries]) {
      const result = add(emptyQueue, [track("x"), track("y")], env);
      if (!result.ok) throw new Error("应当成功");
      expect(result.state.currentEntryId).toBe(result.state.entries[0]?.entryId);
    }
  });

  it("加入空列表不改变队列", () => {
    const { state, env } = queueOf(["a"]);
    expect(appendEntries(state, [], env)).toEqual({ ok: true, state });
    expect(insertAfterCurrent(state, [], env)).toEqual({ ok: true, state });
  });

  it("容量上限 10,000：刚好装满可以，再多一首整批拒绝", () => {
    const env = makeEnv();
    const full = replaceQueue(emptyQueue, Array.from({ length: MAX_QUEUE_ENTRIES - 2 }, (_, index) => track(String(index))), 0, env);
    const fits = appendEntries(full, [track("x"), track("y")], env);
    expect(fits.ok && fits.state.entries.length).toBe(MAX_QUEUE_ENTRIES);
    expect(appendEntries(full, [track("x"), track("y"), track("z")], env)).toEqual({ ok: false, reason: "queue_full" });
    expect(insertAfterCurrent(full, [track("x"), track("y"), track("z")], env)).toEqual({ ok: false, reason: "queue_full" });
    if (!fits.ok) throw new Error("应当成功");
    expect(appendEntries(fits.state, [track("w")], env)).toEqual({ ok: false, reason: "queue_full" });
  });

  it("随机模式下新加入的条目进入袋子（当前条目除外）", () => {
    const { state, env } = queueOf(["a", "b"], { mode: "shuffle" });
    const result = appendEntries(state, [track("x")], env);
    if (!result.ok) throw new Error("应当成功");
    expect(result.state.bag).toEqual(["e2", "e3"]);
  });
});

describe("removeEntry", () => {
  it("按条目 ID 移除：同一首曲目的其他条目留在队列里", () => {
    const env = makeEnv();
    const state = replaceQueue(emptyQueue, [track("a"), track("a"), track("b")], 2, env);
    const result = removeEntry(state, "e1", env);
    expect(result.removedCurrent).toBe(false);
    expect(ids(result.state)).toEqual(["e2", "e3"]);
    expect(result.state.currentEntryId).toBe("e3");
  });

  it("移除当前条目：后继是原来的下一项", () => {
    const { state, env } = queueOf(["a", "b", "c"], { current: 1 });
    const result = removeEntry(state, "e2", env);
    expect(result.removedCurrent).toBe(true);
    expect(ids(result.state)).toEqual(["e1", "e3"]);
    expect(result.state.currentEntryId).toBe("e3");
  });

  it("移除末项的当前条目：顺序模式停在空闲，列表循环回到首项", () => {
    const sequential = queueOf(["a", "b"], { current: 1 });
    const stopped = removeEntry(sequential.state, "e2", sequential.env);
    expect(stopped.state.currentEntryId).toBeNull();
    expect(ids(stopped.state)).toEqual(["e1"]);

    const looping = queueOf(["a", "b"], { current: 1, mode: "repeat_all" });
    expect(removeEntry(looping.state, "e2", looping.env).state.currentEntryId).toBe("e1");
  });

  it("移除唯一的条目得到空队列", () => {
    const { state, env } = queueOf(["a"]);
    const result = removeEntry(state, "e1", env);
    expect(result.state).toMatchObject({ entries: [], currentEntryId: null });
  });

  it("随机模式移除当前条目：后继从袋子里选", () => {
    const env = makeEnv([0.99]);
    const state = replaceQueue({ ...emptyQueue, mode: "shuffle" }, [track("a"), track("b"), track("c")], 0, env);
    const result = removeEntry(state, "e1", env);
    expect(result.state.currentEntryId).toBe("e3");
    expect(result.state.bag).toEqual(["e2"]);
  });

  it("不存在的条目 ID 不改变队列；历史与袋子里的该条目被清掉", () => {
    const { state, env } = queueOf(["a", "b", "c"], { mode: "shuffle" });
    expect(removeEntry(state, "nope", env).state).toBe(state);
    const withHistory = { ...state, history: ["e2", "e3"] };
    const result = removeEntry(withHistory, "e3", env);
    expect(result.state.history).toEqual(["e2"]);
    expect(result.state.bag).toEqual(["e2"]);
  });
});

describe("moveEntry、clearQueue、setMode、jumpTo", () => {
  it("上移与下移交换相邻条目，两端不动，当前条目不变", () => {
    const { state } = queueOf(["a", "b", "c"], { current: 1 });
    expect(ids(moveEntry(state, "e2", -1))).toEqual(["e2", "e1", "e3"]);
    expect(ids(moveEntry(state, "e2", 1))).toEqual(["e1", "e3", "e2"]);
    expect(moveEntry(state, "e1", -1)).toBe(state);
    expect(moveEntry(state, "e3", 1)).toBe(state);
    expect(moveEntry(state, "nope", 1)).toBe(state);
    expect(moveEntry(state, "e2", -1).currentEntryId).toBe("e2");
  });

  it("清空队列保留播放模式", () => {
    const { state } = queueOf(["a", "b"], { mode: "repeat_one" });
    expect(clearQueue(state)).toEqual({ ...emptyQueue, mode: "repeat_one" });
  });

  it("进入随机模式装袋（排除当前与已播放），离开时清袋，模式不变时原样返回", () => {
    const { state } = queueOf(["a", "b", "c"], { current: 1 });
    const played = { ...state, history: ["e1"] };
    const shuffled = setMode(played, "shuffle");
    expect(shuffled.bag).toEqual(["e3"]);
    expect(setMode(shuffled, "sequential").bag).toEqual([]);
    expect(setMode(shuffled, "shuffle")).toBe(shuffled);
  });

  it("用户选中条目：成为当前，原来的当前条目进入历史", () => {
    const { state } = queueOf(["a", "b", "c"], { mode: "shuffle" });
    const jumped = jumpTo(state, "e3");
    expect(jumped.currentEntryId).toBe("e3");
    expect(jumped.history).toEqual(["e1"]);
    expect(jumped.bag).toEqual(["e2"]);
    expect(jumpTo(jumped, "e3")).toBe(jumped);
    expect(jumpTo(jumped, "nope")).toBe(jumped);
  });
});

describe("advance（前进）", () => {
  it("顺序：往后一项，末项自然结束后没有下一项", () => {
    const { state, env } = queueOf(["a", "b"]);
    const first = advance(state, env, { reason: "natural" });
    expect(first.entryId).toBe("e2");
    expect(first.state.currentEntryId).toBe("e2");
    expect(first.state.history).toEqual(["e1"]);
    const end = advance(first.state, env, { reason: "natural" });
    expect(end.entryId).toBeNull();
    expect(end.state).toBe(first.state);
  });

  it("列表循环：末项之后回到首项，单曲队列再播它自己", () => {
    const { state, env } = queueOf(["a", "b"], { current: 1, mode: "repeat_all" });
    expect(advance(state, env, { reason: "natural" }).entryId).toBe("e1");
    expect(advance(state, env, { reason: "manual" }).entryId).toBe("e1");
    const single = queueOf(["a"], { mode: "repeat_all" });
    expect(advance(single.state, single.env, { reason: "natural" }).entryId).toBe("e1");
  });

  it("单曲循环只影响自然结束；手动下一首按顺序，末项之后没有", () => {
    const { state, env } = queueOf(["a", "b"], { mode: "repeat_one" });
    const natural = advance(state, env, { reason: "natural" });
    expect(natural.entryId).toBe("e1");
    expect(natural.state).toBe(state);
    expect(advance(state, env, { reason: "manual" }).entryId).toBe("e2");
    const last = queueOf(["a", "b"], { current: 1, mode: "repeat_one" });
    expect(advance(last.state, last.env, { reason: "manual" }).entryId).toBeNull();
  });

  it("空队列没有下一项", () => {
    expect(advance(emptyQueue, makeEnv(), { reason: "natural" }).entryId).toBeNull();
  });

  it("跳过已知失败的条目；后面全部失败时没有下一项", () => {
    const { state, env } = queueOf(["a", "b", "c"]);
    const skipB = advance(state, env, { reason: "natural", isSkipped: (id) => id === "e2" });
    expect(skipB.entryId).toBe("e3");
    expect(advance(state, env, { reason: "natural", isSkipped: (id) => id !== "e1" }).entryId).toBeNull();
    const looping = queueOf(["a", "b", "c"], { mode: "repeat_all" });
    expect(advance(looping.state, looping.env, { reason: "natural", isSkipped: (id) => id !== "e1" }).entryId).toBeNull();
  });

  it("没有当前条目时前进到第一项", () => {
    const { state, env } = queueOf(["a", "b"]);
    const idle = { ...state, currentEntryId: null };
    expect(advance(idle, env, { reason: "manual" }).entryId).toBe("e1");
  });

  describe("随机", () => {
    it("多于一项时不连续重复，一轮内每个条目恰好出现一次", () => {
      const env = makeEnv([0.1, 0.9, 0.5, 0.3, 0.7, 0.2, 0.8]);
      let state = replaceQueue({ ...emptyQueue, mode: "shuffle" }, ["a", "b", "c", "d", "e"].map(track), 0, env);
      const played = [state.currentEntryId];
      for (let step = 0; step < 4; step += 1) {
        const next = advance(state, env, { reason: "natural" });
        expect(next.entryId).not.toBeNull();
        state = next.state;
        played.push(next.entryId);
      }
      expect(new Set(played).size).toBe(5);
    });

    it("袋子空了重新装袋，轮与轮的交界也不连续重复", () => {
      const env = makeEnv([0, 0.99, 0.5]);
      let state = replaceQueue({ ...emptyQueue, mode: "shuffle" }, ["a", "b", "c"].map(track), 0, env);
      let previous = state.currentEntryId;
      for (let step = 0; step < 40; step += 1) {
        const next = advance(state, env, { reason: "natural" });
        expect(next.entryId).not.toBe(previous);
        expect(next.entryId).not.toBeNull();
        previous = next.entryId;
        state = next.state;
      }
    });

    it("只有一项时再播它自己", () => {
      const { state, env } = queueOf(["a"], { mode: "shuffle" });
      expect(advance(state, env, { reason: "natural" }).entryId).toBe("e1");
    });

    it("跳过失败的条目", () => {
      const { state, env } = queueOf(["a", "b", "c"], { mode: "shuffle" });
      for (let step = 0; step < 10; step += 1) {
        expect(advance(state, env, { reason: "manual", isSkipped: (id) => id === "e2" }).entryId).toBe("e3");
      }
      expect(advance(state, env, { reason: "manual", isSkipped: (id) => id !== "e1" }).entryId).toBe("e1");
    });
  });
});

describe("retreat（后退）", () => {
  it("顺序：回到列表里的上一项；队首时返回当前条目（从头播放）", () => {
    const { state } = queueOf(["a", "b", "c"], { current: 2 });
    const back = retreat(state);
    expect(back.entryId).toBe("e2");
    expect(back.state.currentEntryId).toBe("e2");
    const atStart = queueOf(["a", "b"]);
    expect(retreat(atStart.state).entryId).toBe("e1");
    expect(retreat(atStart.state).state).toBe(atStart.state);
  });

  it("列表循环：队首后退绕到末项", () => {
    const { state } = queueOf(["a", "b", "c"], { mode: "repeat_all" });
    expect(retreat(state).entryId).toBe("e3");
  });

  it("单曲循环的手动后退照常切换", () => {
    const { state } = queueOf(["a", "b"], { current: 1, mode: "repeat_one" });
    expect(retreat(state).entryId).toBe("e1");
  });

  it("随机：沿实际播放历史返回，而不是再随机一次", () => {
    const env = makeEnv([0.7, 0.2]);
    let state = replaceQueue({ ...emptyQueue, mode: "shuffle" }, ["a", "b", "c", "d"].map(track), 0, env);
    const order = [state.currentEntryId];
    for (let step = 0; step < 3; step += 1) {
      const next = advance(state, env, { reason: "manual" });
      state = next.state;
      order.push(next.entryId);
    }
    for (let step = 2; step >= 0; step -= 1) {
      const back = retreat(state);
      expect(back.entryId).toBe(order[step]);
      state = back.state;
    }
  });

  it("随机：后退离开的条目放回袋子，再前进时本轮仍会轮到它", () => {
    const env = makeEnv([0]);
    let state = replaceQueue({ ...emptyQueue, mode: "shuffle" }, ["a", "b", "c", "d"].map(track), 0, env);
    state = advance(state, env, { reason: "manual" }).state;
    expect(state.currentEntryId).toBe("e2");
    const back = retreat(state);
    expect(back.entryId).toBe("e1");
    expect(back.state.bag).toEqual(["e3", "e4", "e2"]);
    state = back.state;
    const visited: (string | null)[] = [];
    for (let step = 0; step < 3; step += 1) {
      const next = advance(state, env, { reason: "manual" });
      state = next.state;
      visited.push(next.entryId);
    }
    expect(visited).toEqual(["e3", "e4", "e2"]);
    expect(state.bag).toEqual([]);
  });

  it("随机且没有历史时返回当前条目；历史里已被移除的条目被跳过", () => {
    const { state } = queueOf(["a", "b"], { mode: "shuffle" });
    expect(retreat(state).entryId).toBe("e1");
    const stale = { ...state, currentEntryId: "e2", history: ["gone", "e1"] };
    expect(retreat(stale).entryId).toBe("e1");
    expect(retreat({ ...state, currentEntryId: "e2", history: ["gone"] }).entryId).toBe("e2");
  });

  it("没有当前条目时没有上一项", () => {
    expect(retreat(emptyQueue).entryId).toBeNull();
  });

  it("跳过已知失败的上一项", () => {
    const { state } = queueOf(["a", "b", "c"], { current: 2 });
    expect(retreat(state, (id) => id === "e2").entryId).toBe("e1");
    expect(retreat(state, () => true).entryId).toBe("e3");
  });
});

describe("hasNext、currentEntry、upNext", () => {
  it("顺序与单曲循环的末项没有下一首，其他模式只要有条目", () => {
    expect(hasNext(queueOf(["a", "b"]).state)).toBe(true);
    expect(hasNext(queueOf(["a", "b"], { current: 1 }).state)).toBe(false);
    expect(hasNext(queueOf(["a", "b"], { current: 1, mode: "repeat_one" }).state)).toBe(false);
    expect(hasNext(queueOf(["a", "b"], { current: 1, mode: "repeat_all" }).state)).toBe(true);
    expect(hasNext(queueOf(["a"], { mode: "shuffle" }).state)).toBe(true);
    expect(hasNext(emptyQueue)).toBe(false);
  });

  it("currentEntry 取当前条目；upNext 取其后的条目，随机模式取袋子里还没播放的", () => {
    const { state } = queueOf(["a", "b", "c", "d"], { current: 1 });
    expect(currentEntry(state)?.track.title).toBe("曲目 b");
    expect(upNext(state, 2).map((entry) => entry.entryId)).toEqual(["e3", "e4"]);
    expect(currentEntry(emptyQueue)).toBeNull();
    const shuffled = queueOf(["a", "b", "c"], { mode: "shuffle" });
    expect(upNext(shuffled.state, 5).map((entry) => entry.entryId)).toEqual(["e2", "e3"]);
  });
});
