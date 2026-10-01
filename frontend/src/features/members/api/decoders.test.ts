import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeCreatedMember, decodeMemberDisabledState, decodeMemberList, decodeSessionsRevoked } from "./decoders";

describe("decodeMemberList", () => {
  const list = sampleBody("users/list");

  it("解码成员，保留停用状态", () => {
    const decoded = decodeMemberList(list);
    expect(decoded.items.map((item) => [item.username, item.role, item.disabled])).toEqual([
      ["archivist", "user", true],
      ["curator", "admin", false],
      ["listener", "user", false],
    ]);
  });

  it("成员列表没有上限，数千个成员也能解码", () => {
    const first = (list as { items: unknown[] }).items[0];
    const many = withValue(list, ["items"], Array.from({ length: 3000 }, () => structuredClone(first)));
    expect(decodeMemberList(many).items).toHaveLength(3000);
  });

  it.each([
    ["缺少 items", {}],
    ["items 不是数组", withValue(list, ["items"], {})],
    ["缺少 id", without(list, ["items", 0, "id"])],
    ["id 不是 UUID", withValue(list, ["items", 0, "id"], "not-a-uuid")],
    ["缺少 disabled", without(list, ["items", 0, "disabled"])],
    ["disabled 不是布尔值", withValue(list, ["items", 0, "disabled"], "false")],
    ["未知角色", withValue(list, ["items", 1, "role"], "owner")],
    ["created_at 不是 RFC 3339", withValue(list, ["items", 0, "created_at"], "昨天")],
    ["用户名为空", withValue(list, ["items", 2, "username"], "")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeMemberList(input)).toThrow(d.DecodeError);
  });
});

describe("decodeCreatedMember", () => {
  const created = sampleBody("users/create");

  it("解码新建的成员", () => {
    expect(decodeCreatedMember(created)).toMatchObject({ username: "listener", role: "user" });
  });

  it.each([
    ["缺少 id", without(created, ["id"])],
    ["缺少 username", without(created, ["username"])],
    ["未知角色", withValue(created, ["role"], "root")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeCreatedMember(input)).toThrow(d.DecodeError);
  });
});

describe("decodeMemberDisabledState 与 decodeSessionsRevoked", () => {
  it("停用与重新启用返回目标状态", () => {
    expect(decodeMemberDisabledState(sampleBody("users/disable"))).toEqual({ disabled: true });
    expect(decodeMemberDisabledState(sampleBody("users/enable"))).toEqual({ disabled: false });
    expect(() => decodeMemberDisabledState({})).toThrow(d.DecodeError);
    expect(() => decodeMemberDisabledState({ disabled: 1 })).toThrow(d.DecodeError);
  });

  it("撤销会话只接受 revoked 为 true", () => {
    expect(decodeSessionsRevoked(sampleBody("users/revoke-sessions"))).toEqual({ revoked: true });
    expect(() => decodeSessionsRevoked({ revoked: false })).toThrow(d.DecodeError);
  });
});
