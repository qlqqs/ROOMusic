import { act, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { albumGroup, mediaOf } from "../../../test/catalog-data";
import { ReleaseMedia } from "./release-media";

const media = mediaOf(albumGroup(1, { medium_count: 2, track_count: 3 }).representative);
const target = media[1]?.tracks[0];
if (target === undefined) throw new Error("定位夹具缺少第二张介质的曲目");
const track = target;
const locate = { trackId: track.id, token: "arrival-1" };
const row = () => screen.getByText(track.title).closest("li");

beforeEach(() => { vi.useFakeTimers(); });
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

function nextFrame() { act(() => { vi.advanceTimersToNextFrame(); }); }

describe("曲目定位", () => {
  it("跨介质定位：滚到目标、焦点落在行上，高亮 2.5 秒后消失但不丢焦点", () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const onLocated = vi.fn();
    render(<ReleaseMedia media={media} albumArtist="" locate={locate} onLocated={onLocated} />);
    expect(row()?.hasAttribute("data-located")).toBe(true);
    expect(onLocated).toHaveBeenCalledWith("arrival-1");
    nextFrame();
    expect(document.activeElement).toBe(row());
    expect(row()?.getAttribute("tabindex")).toBe("-1");
    expect(scroll).toHaveBeenCalledExactlyOnceWith({ block: "center" });
    expect(scroll.mock.instances[0]).toBe(row());
    act(() => { vi.advanceTimersByTime(2483); });
    expect(row()?.hasAttribute("data-located")).toBe(true);
    act(() => { vi.advanceTimersByTime(1); });
    expect(row()?.hasAttribute("data-located")).toBe(false);
    expect(document.activeElement).toBe(row());
    expect(scroll).toHaveBeenCalledTimes(1);
  });

  it("同一到达在重绘、数据刷新、播放标记变化和高亮结束后都不再滚动或抢焦点", () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const view = render(<><button>其他操作</button><ReleaseMedia media={media} albumArtist="" locate={locate} /></>);
    nextFrame();
    const other = screen.getByRole("button", { name: "其他操作" });
    act(() => other.focus());
    view.rerender(<><button>其他操作</button><ReleaseMedia media={structuredClone(media)} albumArtist="刷新艺人" locate={{ ...locate }} currentTrackId={track.id} currentTrackLabel="正在播放" /></>);
    nextFrame();
    act(() => { vi.advanceTimersByTime(3000); });
    expect(document.activeElement).toBe(other);
    expect(scroll).toHaveBeenCalledTimes(1);
    expect(row()?.hasAttribute("data-located")).toBe(false);
  });

  it("目标不在版本里时静默处理，不滚动、不移动焦点、不高亮", () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const onLocated = vi.fn();
    const view = render(<button>其他操作</button>);
    const other = screen.getByRole("button", { name: "其他操作" });
    act(() => other.focus());
    view.rerender(<><button>其他操作</button><ReleaseMedia media={media} albumArtist="" locate={{ trackId: "absent", token: "arrival-2" }} onLocated={onLocated} /></>);
    nextFrame();
    expect(scroll).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(other);
    expect(document.querySelector("[data-located]")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(onLocated).toHaveBeenCalledExactlyOnceWith("arrival-2");
  });

  it("新的到达 token 可以再次定位同一首曲目", () => {
    const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
    const view = render(<ReleaseMedia media={media} albumArtist="" locate={locate} />);
    nextFrame();
    act(() => { vi.advanceTimersByTime(2500); });
    view.rerender(<ReleaseMedia media={media} albumArtist="" locate={{ ...locate, token: "arrival-2" }} />);
    nextFrame();
    expect(scroll).toHaveBeenCalledTimes(2);
    expect(document.activeElement).toBe(row());
    expect(row()?.hasAttribute("data-located")).toBe(true);
  });
});

describe("曲目署名", () => {
  it("按细分分组，显示小提琴与作词，歌词里的混音工程师保留原写法", () => {
    const release = albumGroup(2, { track_count: 1 }).representative;
    const creditedMedia = mediaOf(release, () => ({
      credits: [
        { role: "lyricist", name: "方文山", detail: null },
        { role: "performer", name: "Hilary Hahn", detail: "violin" },
        { role: "performer", name: "Anne-Sophie Mutter", detail: "violin" },
        { role: "producer", name: "某某", detail: "mix" },
        { role: "producer", name: "欧姆", detail: "混音工程师" },
      ],
    }));
    render(<ReleaseMedia media={creditedMedia} albumArtist={release.artist} />);
    expect(screen.getByText("方文山").closest("p")?.textContent).toBe(
      "作词：方文山；小提琴：Hilary Hahn、Anne-Sophie Mutter；混音：某某；混音工程师：欧姆",
    );
  });
});

describe("作品分组", () => {
  it("用作品小标题和组总时长归组，行内只显示乐章名并保留序号与时长", () => {
    const release = albumGroup(3, { medium_count: 1, track_count: 2 }).representative;
    const workTitle = "第五交响曲，Op. 67";
    const firstMovement = "I. Allegro con brio";
    const secondMovement = "II. Andante con moto";
    const workMedia = mediaOf(release, (_medium, position) => ({
      title: `${workTitle}: ${position === 1 ? firstMovement : secondMovement}`,
      duration_seconds: position === 1 ? 180 : 240,
      work: { index: 1, title: workTitle, movement: position === 1 ? firstMovement : secondMovement },
    }));
    render(<ReleaseMedia media={workMedia} albumArtist={release.artist} />);

    expect(screen.getAllByText(workTitle)).toHaveLength(1);
    expect(screen.getByText(workTitle).closest("p")?.textContent).toContain("7:00");
    const movements = screen.getByRole("list", { name: workTitle });
    const rows = within(movements).getAllByRole("listitem");
    expect(rows).toHaveLength(2);
    for (const [index, row] of rows.entries()) {
      expect(within(row).getByText(index === 0 ? firstMovement : secondMovement).textContent).toBe(index === 0 ? firstMovement : secondMovement);
      expect(within(row).getByText(String(index + 1)).textContent).toBe(String(index + 1));
      expect(within(row).getByText(index === 0 ? "3:00" : "4:00").textContent).toBe(index === 0 ? "3:00" : "4:00");
    }
    expect(screen.queryByText(`${workTitle}: ${firstMovement}`)).toBeNull();
    expect(screen.queryByText(`${workTitle}: ${secondMovement}`)).toBeNull();
  });
});
