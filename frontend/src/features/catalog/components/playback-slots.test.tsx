import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { albumGroup, releaseDetailOf } from "../../../test/catalog-data";
import type { ReleaseTrackDTO } from "../api/decoders";
import { AlbumHeader } from "./album-header";
import { ReleaseMedia } from "./release-media";

describe("专辑播放插槽", () => {
  it("catalog 不依赖播放器：只渲染传入的头部动作、曲目动作与当前曲目标记", () => {
    const release = releaseDetailOf(albumGroup(1, { medium_count: 1, track_count: 2 }).representative);
    const currentTrackId = release.media[0]?.tracks[0]?.id;
    render(<><AlbumHeader release={release} detail={release} actions={<button>播放整张专辑</button>} /><ReleaseMedia media={release.media} albumArtist={release.album_artist ?? release.artist} currentTrackId={currentTrackId} currentTrackLabel="已暂停" renderTrackActions={(track) => <button aria-label={`播放 ${track.title}`}>播放</button>} /></>);
    expect(screen.getByRole("button", { name: "播放整张专辑" })).toBeTruthy();
    expect(screen.getAllByRole("button", { name: /^播放 Track/ })).toHaveLength(2);
    expect(screen.getAllByText("已暂停")).toHaveLength(1);
  });
  it("只给当前曲目 ID 时写“当前曲目”", () => {
    const release = releaseDetailOf(albumGroup(1, { medium_count: 1, track_count: 2 }).representative);
    render(<ReleaseMedia media={release.media} albumArtist={release.artist} currentTrackId={release.media[0]?.tracks[1]?.id} />);
    expect(screen.getAllByText("当前曲目")).toHaveLength(1);
  });
  it("没有插槽时继续只显示曲目事实", () => {
    const release = releaseDetailOf(albumGroup(1, { medium_count: 1, track_count: 1 }).representative);
    render(<ReleaseMedia media={release.media} albumArtist={release.artist} />);
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByText("当前曲目")).toBeNull();
  });
  it("当前曲目或它的状态变化时，只重绘标记变化的行", () => {
    const release = releaseDetailOf(albumGroup(1, { medium_count: 1, track_count: 4 }).representative);
    const [first, second] = release.media[0]?.tracks ?? [];
    if (first === undefined || second === undefined) throw new Error("缺少测试曲目");
    const rendered: string[] = [];
    const renderTrackActions = (track: ReleaseTrackDTO) => {
      rendered.push(track.id);
      return <button>播放</button>;
    };
    const media = (currentTrackId: string, currentTrackLabel: string) => <ReleaseMedia media={release.media} albumArtist={release.artist} renderTrackActions={renderTrackActions} currentTrackId={currentTrackId} currentTrackLabel={currentTrackLabel} />;
    const { rerender } = render(media(first.id, "正在播放"));
    expect(rendered).toHaveLength(4);
    rendered.length = 0;
    rerender(media(first.id, "正在播放"));
    expect(rendered).toEqual([]);
    rerender(media(first.id, "已暂停"));
    expect(rendered).toEqual([first.id]);
    rendered.length = 0;
    rerender(media(second.id, "正在加载"));
    expect([...rendered].sort()).toEqual([first.id, second.id].sort());
    expect(screen.getAllByText("正在加载")).toHaveLength(1);
    expect(screen.queryByText("已暂停")).toBeNull();
  });
});
