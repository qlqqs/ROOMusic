import { expect, type APIRequestContext, type APIResponse, type Page } from "@playwright/test";
import { decode as d } from "../../src/shared/api";
import { decodeReleaseDetail, decodeReleaseGroupPage } from "../../src/features/catalog/api/decoders";

export const admin = { username: "playback-admin", password: "PlaybackFixturePassword42" };
export const origin = process.env["ROOMUSIC_PLAYBACK_BASE_URL"] ?? "";
export const fixtureRoot = process.env["ROOMUSIC_PLAYBACK_FIXTURE_ROOT"] ?? "";
export const decodeID = d.defineDecoder(d.object({ id: d.uuid() }));
const decodeScan = d.defineDecoder(d.object({ id: d.uuid(), status: d.string() }));
export const decodeRoot = d.defineDecoder(d.object({ id: d.uuid(), revision: d.integer({ min: 1 }), status: d.string() }));
export const decodeSetup = d.defineDecoder(d.object({ setup_required: d.boolean() }));
export const decodeError = d.defineDecoder(d.object({ error: d.object({ code: d.string() }) }));
export async function body(response: APIResponse): Promise<unknown> {
  expect(response.ok(), `接口返回 HTTP ${response.status()}`).toBe(true);
  const input: unknown = await response.json();
  return input;
}
export async function post(api: APIRequestContext, path: string, data: unknown) {
  return api.post(path, { data, headers: { Origin: origin, "Idempotency-Key": crypto.randomUUID() } });
}
export async function login(api: APIRequestContext, credentials = admin) {
  return decodeID(await body(await post(api, "/api/v1/auth/login", credentials)));
}
export async function scan(api: APIRequestContext) {
  const result = decodeScan(await body(await post(api, "/api/v1/scans", {})));
  await expect.poll(async () => decodeScan(await body(await api.get(`/api/v1/scans/${result.id}`))).status).toBe("succeeded");
}
export async function catalog(api: APIRequestContext) {
  const groups = decodeReleaseGroupPage(await body(await api.get("/api/v1/release-groups?page_size=100")));
  return Promise.all(groups.items.map(async (group) => ({
    groupId: group.id,
    release: decodeReleaseDetail(await body(await api.get(`/api/v1/releases/${group.representative.id}`))),
  })));
}
export type Albums = Awaited<ReturnType<typeof catalog>>;
export function findTrack(albums: Albums, source: string) {
  for (const album of albums) {
    const track = album.release.media.flatMap((medium) => medium.tracks).find((item) => item.source === source);
    if (track) return { ...album, track };
  }
  throw new Error(`缺少合成素材 ${source}`);
}

// 只记录原生媒体的引用与事件，仍调用原来的 play()，不伪造时间、网络、解码或 ended。
declare global {
  interface Window {
    playbackTestMedia: HTMLMediaElement[];
    playbackTestEvents: { type: string; src: string; time: number }[];
  }
}
export async function observeMedia(page: Page) {
  await page.addInitScript(() => {
    window.playbackTestMedia = [];
    window.playbackTestEvents = [];
    const nativePlay = HTMLMediaElement.prototype.play;
    HTMLMediaElement.prototype.play = function () {
      if (!window.playbackTestMedia.includes(this)) {
        window.playbackTestMedia.push(this);
        for (const type of ["playing", "ended", "seeked", "error"]) {
          this.addEventListener(type, () => window.playbackTestEvents.push({ type, src: this.currentSrc, time: this.currentTime }));
        }
      }
      return nativePlay.call(this);
    };
  });
}
export async function mediaState(page: Page) {
  return page.evaluate(() => {
    const audio = window.playbackTestMedia.at(-1);
    return audio ? { src: audio.currentSrc, time: audio.currentTime, duration: audio.duration, paused: audio.paused, ended: audio.ended } : null;
  });
}
export async function playTrack(page: Page, item: ReturnType<typeof findTrack>) {
  await page.goto(`/albums/${item.groupId}`);
  const row = page.locator("ol > li").filter({ has: page.getByText(item.track.title, { exact: true }) });
  await row.getByRole("button", { name: "从这首开始播放", exact: true }).click();
  await expect.poll(async () => (await mediaState(page))?.paused).toBe(false);
}
