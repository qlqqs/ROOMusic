import type { Page } from "@playwright/test";
import { albumGroup, mediaOf, releaseDetailOf } from "../../src/test/catalog-data";
import type { ApiMock } from "./api-mock";
import { expect } from "./fixtures";
import { installZoneAudio } from "./zones";

/** 四首合成曲目，从第二首开始并暂停，保留已播放、当前与接下来三段。 */
export async function openQueueScene(page: Page, mock: ApiMock) {
  const group = albumGroup(93, { title: "队列专项专辑", track_count: 4 });
  const release = releaseDetailOf(group.representative, {
    media: mediaOf(group.representative, (_medium, track) => ({
      title: `队列曲目 ${track}`, artist: "合成艺术家", source_kind: "flac_vorbis_comment", codec: "flac",
      duration_seconds: 60, bit_depth: 16, sample_rate: 44100, channels: 2, cue: null,
    })),
  });
  mock.groups = [group];
  mock.releases.set(release.id, release);
  await installZoneAudio(page);
  await page.route("**/api/v1/tracks/*/lyrics", (route) => {
    const trackId = new URL(route.request().url()).pathname.split("/").at(-2) ?? "";
    return route.fulfill({ json: { track_id: trackId, lyrics: null } });
  });
  await page.goto(`/albums/${group.id}`);
  const second = page.locator("[data-track-row]").nth(1);
  await second.hover();
  await second.getByRole("button", { name: "从这首开始播放", exact: true }).click();
  const bar = page.getByRole("region", { name: "播放器", exact: true });
  await expect(bar.getByText("队列曲目 2", { exact: true })).toBeVisible();
  await bar.getByRole("button", { name: "暂停", exact: true }).click();
  await expect(bar.getByRole("button", { name: "播放", exact: true })).toBeVisible();
  await bar.getByRole("button", { name: "队列（4）", exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "队列", exact: true });
  await expect(drawer).toBeVisible();
  await expect(drawer).toHaveCSS("transform", "none");
  await expect(drawer.getByRole("button", { name: "已播放 1 首", exact: true })).toHaveAttribute("aria-expanded", "false");
  await expect(drawer.getByRole("region", { name: "正在播放", exact: true })).toContainText("队列曲目 2");
  await expect(drawer.getByRole("region", { name: "接下来", exact: true }).getByRole("listitem")).toHaveCount(2);
  return drawer;
}
