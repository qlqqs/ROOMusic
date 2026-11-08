import { describe, expect, it } from "vitest";
import { changeOwnPassword } from "../features/auth/api/endpoints";
import { resetMemberPassword } from "../features/members/api/endpoints";
import { loadApiSample } from "./api-samples";
import { artistDataOf, artistTestId, respondToArtists, seedArtistCandidates } from "./artist-data";
import { installFakeApi, installFakeSession } from "./fake-api";
import { installMembersApi } from "./fake-members";

const next = "new-account-password";

function failureOfSample(name: string) {
  const sample = loadApiSample(name);
  const body = sample.body;
  if (typeof body !== "object" || body === null || !("error" in body) || !("request_id" in body)) throw new Error("需要错误样本");
  const error = body.error;
  if (typeof error !== "object" || error === null || !("code" in error) || !("message" in error)) throw new Error("需要错误信封");
  return { kind: "http", status: sample.status, code: error.code, message: error.message, requestId: body.request_id };
}

describe("V1/V2/V5：密码替身使用真实响应样本", () => {
  it("自助修改：先校验新密码，再核对当前密码；拒绝时密码和当前会话不变", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "listener" });
    await expect(changeOwnPassword({ currentPassword: "wrong", newPassword: "short" })).rejects.toMatchObject({ failure: failureOfSample("errors/invalid-input-password") });
    await expect(changeOwnPassword({ currentPassword: "wrong", newPassword: next })).rejects.toMatchObject({ failure: failureOfSample("errors/current-password-incorrect") });
    expect(session.accounts.listener?.password).toBe("listener-password");
    expect(session.current).toBe("listener");
    await expect(changeOwnPassword({ currentPassword: "listener-password", newPassword: next })).resolves.toEqual({ status: 200, data: loadApiSample("auth/password-changed").body });
    expect(session.accounts.listener?.password).toBe(next);
    expect(session.current).toBe("listener");
  });

  it("重置：自己和非法新密码使用真实错误样本，不改任何账号", async () => {
    const api = installFakeApi();
    const session = installFakeSession(api, { current: "curator" });
    const members = installMembersApi(api, session);
    const self = members.users.find((member) => member.username === "curator");
    const target = members.users.find((member) => member.username === "listener");
    if (self === undefined || target === undefined) throw new Error("缺少成员样本");
    await expect(resetMemberPassword(self.id.toUpperCase(), next)).rejects.toMatchObject({ failure: failureOfSample("errors/self-password-reset") });
    await expect(resetMemberPassword(target.id, "short")).rejects.toMatchObject({ failure: failureOfSample("errors/invalid-input-password") });
    expect(session.accounts.listener?.password).toBe("listener-password");
    expect(session.accounts.curator?.password).toBe("curator-password");
    await expect(resetMemberPassword(target.id, next)).resolves.toEqual({ status: 200, data: loadApiSample("users/password-reset").body });
    expect(session.accounts.listener?.password).toBe(next);
    expect(session.current).toBe("curator");
  });
});

describe("V4/V5：已忽略候选替身", () => {
  it.each(["ignored=yes", "ignored=", "ignored=TRUE", "ignored=true&ignored=false", "ignored=true&ignored=true"])("%s 与真实 invalid_filter 样本一致", (query) => {
    const response = respondToArtists(artistDataOf(), "GET", new URL(`/api/v1/artist-candidates?${query}`, "http://localhost"));
    const sample = loadApiSample("artists/errors/invalid-candidate-filter");
    expect(response).toEqual({ status: sample.status, body: sample.body });
  });

  it("ignored=false 仍是待处理；忽略只移动这一对名字键，恢复后返回，两列表不混在一起", () => {
    const data = artistDataOf();
    const pair = seedArtistCandidates(data)[0];
    if (pair === undefined) throw new Error("缺少候选样本");
    const read = (query: string) => respondToArtists(data, "GET", new URL(`/api/v1/artist-candidates${query}`, "http://localhost"));
    const before = read("");
    expect(read("?ignored=false")).toEqual(before);
    const ignoredEmpty = loadApiSample("governance/artist-candidates-ignored-empty");
    expect(read("?ignored=true")).toEqual({ status: ignoredEmpty.status, body: ignoredEmpty.body });
    const path = new URL(`/api/v1/artists/${pair.left.id}/candidate-ignore`, "http://localhost");
    const change = (ignored: boolean, revision: number) => respondToArtists(data, "POST", path, {
      expected_revision: revision, other_artist_id: pair.right.id, other_expected_revision: revision, key_pairs: pair.key_pairs, ignored,
    }, artistTestId(30_000 + revision));
    expect(change(true, 1).status).toBe(200);
    const ignored = loadApiSample("governance/artist-candidates-ignored");
    expect(read("?ignored=true")).toEqual({ status: ignored.status, body: ignored.body });
    expect(read("").body).toEqual(ignoredEmpty.body);
    expect(change(false, 2).status).toBe(200);
    expect(read("")).toEqual(before);
    expect(read("?ignored=true").body).toEqual(ignoredEmpty.body);
  });
});
