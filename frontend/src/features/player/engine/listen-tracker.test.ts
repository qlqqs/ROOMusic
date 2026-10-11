import { describe, expect, it } from "vitest";
import { listenThreshold, ListenTracker, MAX_LISTEN_THRESHOLD_SECONDS, type ListenRecord, type ListenTrack } from "./listen-tracker";

const track = (overrides: Partial<ListenTrack> = {}): ListenTrack => ({ entryId: "entry-1", trackId: "track-1", durationSeconds: 200, ...overrides });

/** 受控时钟与位置：advance 同时推进真实时间与播放位置（正常出声），jump 只改变位置。 */
function createTracker() {
  let clock = 1_000_000;
  let position = 0;
  const tracker = new ListenTracker(() => clock);
  const records: ListenRecord[] = [];
  const collect = (record: ListenRecord | null): ListenRecord | null => {
    if (record !== null) records.push(record);
    return record;
  };
  return {
    tracker,
    records,
    /** 开始出声：以当前位置为基准。 */
    play(at = position) {
      position = at;
      tracker.resume(position);
    },
    /** 正常出声 seconds 秒（真实时间与位置同步前进），然后采样一次。 */
    advance(seconds: number) {
      clock += seconds * 1000;
      position += seconds;
      return collect(tracker.sample(position));
    },
    /** 位置在 seconds 秒真实时间里前进了 delta 秒（不等于真实流逝），然后采样一次。 */
    move(delta: number, seconds: number) {
      clock += seconds * 1000;
      position += delta;
      return collect(tracker.sample(position));
    },
    /** 真实时间流逝但没有采样（暂停、缓冲）。 */
    wait(seconds: number) {
      clock += seconds * 1000;
    },
    get position() {
      return position;
    },
    set position(value: number) {
      position = value;
    },
  };
}

describe("listenThreshold", () => {
  it("门槛是曲长的一半，最长 240 秒；时长未知时取 240 秒", () => {
    expect(listenThreshold(60)).toBe(30);
    expect(listenThreshold(479)).toBe(239.5);
    expect(listenThreshold(480)).toBe(240);
    expect(listenThreshold(3600)).toBe(MAX_LISTEN_THRESHOLD_SECONDS);
    for (const unknown of [null, 0, -3, Number.NaN, Number.POSITIVE_INFINITY]) expect(listenThreshold(unknown)).toBe(240);
  });
});

describe("ListenTracker 累计与记录", () => {
  it("实际出声累计到门槛时恰好记录一次，之后同一次访问不再记录", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 200 })); // T = 100
    t.play(0);
    expect(t.advance(50)).toBeNull();
    expect(t.advance(49)).toBeNull();
    const record = t.advance(1.5);
    expect(record).toEqual({ trackId: "track-1", playedSeconds: 100.5 });
    expect(t.advance(60)).toBeNull();
    expect(t.records).toHaveLength(1);
  });

  it("时长未知的曲目先用 240 秒，媒体报告时长后改用它的一半", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: null }));
    t.play(0);
    expect(t.advance(30)).toBeNull();
    t.tracker.mediaDuration(100); // T = 50
    expect(t.advance(10)).toBeNull();
    expect(t.advance(10)).toEqual({ trackId: "track-1", playedSeconds: 50 });
  });

  it("扫描有时长时以扫描时长为准，媒体报告的时长不改变门槛（前后端门槛一致）", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 200 })); // T = 100
    t.play(0);
    t.tracker.mediaDuration(20); // 与扫描不符的媒体时长被忽略
    expect(t.advance(60)).toBeNull();
    expect(t.advance(40)).not.toBeNull();
  });

  it("媒体时长也未知时一直用 240 秒", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: null }));
    t.tracker.mediaDuration(null);
    t.tracker.mediaDuration(Number.NaN);
    t.play(0);
    expect(t.advance(239)).toBeNull();
    expect(t.advance(1)).not.toBeNull();
  });

  it("静音不影响累计：跟踪器只看位置与时间", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 20 })); // T = 10
    t.play(0);
    expect(t.advance(10)).not.toBeNull();
  });
});

describe("ListenTracker 只累计真实的出声时间", () => {
  it("拖动跨过的距离不算：seeking 之后以新位置为基准", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 200 })); // T = 100
    t.play(0);
    t.advance(40);
    t.tracker.seeking();
    t.position = 190;
    expect(t.advance(0)).toBeNull(); // seeking 期间的 timeupdate 只建立基准
    t.tracker.seeked(190);
    expect(t.advance(5)).toBeNull(); // 40 + 5
    expect(t.advance(50)).toBeNull(); // 95
    expect(t.advance(5)).not.toBeNull(); // 100
  });

  it("没有 seeking 事件的跳变也不算：前进量超过真实流逝加 1 秒就丢弃", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 200 }));
    t.play(0);
    expect(t.move(150, 0.25)).toBeNull(); // 0.25 秒里前进 150 秒
    expect(t.move(1.2, 0.25)).toBeNull(); // 1.2 ≤ 0.25 + 1 不超界，会累计 1.2，但远不到门槛
    expect(t.advance(50)).toBeNull(); // 累计 51.2
  });

  it("后退不算", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 100 })); // T = 50
    t.play(80);
    t.advance(40);
    t.move(-60, 1);
    expect(t.advance(5)).toBeNull(); // 40 + 5
    expect(t.advance(5)).not.toBeNull(); // 50
  });

  it("后台标签页的 timeupdate 变稀：位置前进量与真实流逝相当时照常累计", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 200 })); // T = 100
    t.play(0);
    expect(t.advance(30)).toBeNull();
    expect(t.advance(30)).toBeNull();
    expect(t.advance(30)).toBeNull();
    expect(t.advance(30)).not.toBeNull();
  });

  it("时钟向后跳变时丢弃那一段，不多算", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 60 })); // T = 30
    t.play(0);
    t.advance(10);
    expect(t.move(30, -60)).toBeNull(); // 真实时间倒退 60 秒：这 30 秒不能算（算了就到门槛了）
    expect(t.advance(10)).toBeNull(); // 累计 20
    expect(t.advance(10)).not.toBeNull(); // 累计 30
  });

  it("暂停期间流逝的时间不算：恢复出声以当前位置为基准", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 100 })); // T = 50
    t.play(0);
    t.advance(30);
    t.tracker.suspend();
    t.wait(600);
    t.play(30);
    expect(t.advance(10)).toBeNull(); // 40
    expect(t.advance(10)).not.toBeNull(); // 50
  });

  it("没有基准时的第一次采样只建立基准，不累计", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 100 }));
    expect(t.advance(500)).toBeNull();
    expect(t.advance(40)).toBeNull();
    expect(t.advance(10)).not.toBeNull(); // 只有后两次的 50 秒算
  });

  it("没有访问时不累计也不记录", () => {
    const t = createTracker();
    t.play(0);
    expect(t.advance(1000)).toBeNull();
    expect(t.records).toEqual([]);
  });

  it("位置不是有限数时忽略", () => {
    const t = createTracker();
    t.tracker.begin(track());
    t.play(0);
    expect(t.tracker.sample(Number.NaN)).toBeNull();
    expect(t.advance(10)).toBeNull();
  });
});

describe("ListenTracker 访问", () => {
  it("暂停后继续、重试继续是同一次访问：累计保留，已记录的不再记录", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 100 })); // T = 50
    t.play(0);
    t.advance(30);
    t.tracker.suspend();
    t.tracker.continueOrBegin(track({ durationSeconds: 100 })); // 同一个条目：继续
    t.play(30);
    expect(t.advance(20)).toEqual({ trackId: "track-1", playedSeconds: 50 });
    t.tracker.suspend();
    t.tracker.continueOrBegin(track({ durationSeconds: 100 }));
    t.play(50);
    expect(t.advance(40)).toBeNull(); // 同一次访问已经记录过
    expect(t.records).toHaveLength(1);
  });

  it("换到另一个条目是新的访问：旧的累计作废，新的访问可以再记录一次", () => {
    const t = createTracker();
    t.tracker.begin(track({ entryId: "entry-1", trackId: "track-1", durationSeconds: 100 }));
    t.play(0);
    t.advance(40);
    t.tracker.begin(track({ entryId: "entry-2", trackId: "track-2", durationSeconds: 100 }));
    t.play(0);
    expect(t.advance(40)).toBeNull(); // 不继承旧条目的 40 秒
    expect(t.advance(10)).toEqual({ trackId: "track-2", playedSeconds: 50 });
  });

  it("continueOrBegin 遇到另一个条目（或没有访问）开始新的访问", () => {
    const t = createTracker();
    t.tracker.continueOrBegin(track({ entryId: "entry-1", durationSeconds: 100 }));
    t.play(0);
    t.advance(40);
    t.tracker.continueOrBegin(track({ entryId: "entry-2", trackId: "track-2", durationSeconds: 100 }));
    t.play(0);
    expect(t.advance(40)).toBeNull();
    expect(t.advance(10)).toEqual({ trackId: "track-2", playedSeconds: 50 });
  });

  it("同一个条目从头再播（begin）是新的访问，可以再记录一次", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 100 }));
    t.play(0);
    expect(t.advance(50)).not.toBeNull();
    t.tracker.begin(track({ durationSeconds: 100 })); // 单曲循环的下一轮
    t.play(0);
    expect(t.advance(49)).toBeNull();
    expect(t.advance(1)).not.toBeNull();
    expect(t.records).toHaveLength(2);
  });

  it("访问结束后再出声是新的访问", () => {
    const t = createTracker();
    t.tracker.begin(track({ durationSeconds: 100 }));
    t.play(0);
    t.advance(30);
    t.tracker.end();
    expect(t.advance(1000)).toBeNull();
    t.tracker.continueOrBegin(track({ durationSeconds: 100 }));
    t.play(0);
    expect(t.advance(49)).toBeNull(); // 不继承之前的 30 秒
    expect(t.advance(1)).not.toBeNull();
  });
});
