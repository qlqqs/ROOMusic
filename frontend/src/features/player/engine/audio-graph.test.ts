import { describe, expect, it, vi } from "vitest";
import type { AudioLike } from "./player-engine";
import { AudioGraph, type AudioContextLike, type GainNodeLike } from "./audio-graph";

function fixture() {
  const params: { values: number[]; curves: Float32Array[] }[] = [];
  const nodes: GainNodeLike[] = [];
  let state = "running";
  const listeners = new Set<() => void>();
  const context: AudioContextLike = {
    get state() { return state; }, currentTime: 2, destination: {}, resume: async () => undefined,
    createGain: () => {
      const record = { values: [] as number[], curves: [] as Float32Array[] };
      params.push(record);
      const gain = { value: 1, setValueAtTime: (value: number) => { record.values.push(value); }, setTargetAtTime: (value: number) => { record.values.push(value); }, setValueCurveAtTime: (value: Float32Array) => { record.curves.push(value); }, cancelScheduledValues: () => undefined };
      const node = { gain, connect: vi.fn() } as unknown as GainNodeLike;
      nodes.push(node);
      return node;
    },
    createMediaElementSource: () => ({ connect: vi.fn() }),
    addEventListener: (_type, listener) => { listeners.add(listener); },
    removeEventListener: (_type, listener) => { listeners.delete(listener); },
  };
  const graph = new AudioGraph(() => context, () => undefined);
  graph.ensure();
  return { graph, params, nodes, context, setState: (next: string) => { state = next; listeners.forEach((listener) => listener()); } };
}
const element = () => ({ volume: 1 }) as AudioLike;

describe("Web Audio 图节点", () => {
  it("总音量与均衡分别写到对应增益节点，元素支路支持连接", () => {
    const f = fixture();
    const audio = element();
    expect(f.graph.connect(audio)).toBe(true);
    f.graph.setMaster(0.4);
    f.graph.setLevel(audio, 2.45);
    expect(f.params[0]?.values).toEqual([0.4]);
    expect(f.params[1]?.values).toEqual([2.45]);
    expect(audio.volume).toBe(1);
  });

  it("交叉渐变为旧、新元素分别写入淡出与淡入曲线", () => {
    const f = fixture();
    const old = element();
    const next = element();
    f.graph.connect(old);
    f.graph.connect(next);
    f.graph.crossfade(old, next, 5);
    expect(f.params[2]?.curves).toHaveLength(1);
    expect(f.params[4]?.curves).toHaveLength(1);
    const fadeOut = f.params[2]?.curves[0];
    const fadeIn = f.params[4]?.curves[0];
    expect(fadeOut?.[0]).toBeCloseTo(1);
    expect(fadeOut?.[63]).toBeCloseTo(0);
    expect(fadeIn?.[0]).toBeCloseTo(0);
    expect(fadeIn?.[63]).toBeCloseTo(1);
  });

  it("上下文非 running 时拒绝连接新元素", () => {
    const f = fixture();
    f.setState("suspended");
    expect(f.graph.connect(element())).toBe(false);
  });
});
