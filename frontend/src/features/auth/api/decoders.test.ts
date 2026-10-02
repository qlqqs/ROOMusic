import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeLoggedOut, decodeSetupStatus, decodeViewer } from "./decoders";

describe("decodeViewer", () => {
  const admin = sampleBody("auth/me-admin");

  it("解码管理员与成员", () => {
    expect(decodeViewer(admin)).toEqual({ id: "00000000-0000-4000-8000-000000000001", username: "curator", role: "admin" });
    expect(decodeViewer(sampleBody("auth/me-user"))).toEqual({ id: "00000000-0000-4000-8000-000000000001", username: "listener", role: "user" });
  });

  it.each([
    ["缺少 id", without(admin, ["id"])],
    ["id 不是 UUID", withValue(admin, ["id"], "curator")],
    ["id 不是字符串", withValue(admin, ["id"], 1)],
    ["缺少 username", without(admin, ["username"])],
    ["缺少 role", without(admin, ["role"])],
    ["未知角色", withValue(admin, ["role"], "owner")],
    ["用户名为空", withValue(admin, ["username"], "")],
    ["用户名不是字符串", withValue(admin, ["username"], 7)],
    ["不是对象", []],
    ["null", null],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeViewer(input)).toThrow(d.DecodeError);
  });
});

describe("decodeSetupStatus", () => {
  it("解码两种状态", () => {
    expect(decodeSetupStatus(sampleBody("auth/setup-status-required"))).toEqual({ setup_required: true });
    expect(decodeSetupStatus(sampleBody("auth/setup-status-completed"))).toEqual({ setup_required: false });
  });

  it("拒绝缺字段与非布尔值", () => {
    expect(() => decodeSetupStatus({})).toThrow(d.DecodeError);
    expect(() => decodeSetupStatus({ setup_required: "yes" })).toThrow(d.DecodeError);
  });
});

describe("decodeLoggedOut", () => {
  it("只接受 logged_out 为 true", () => {
    expect(decodeLoggedOut(sampleBody("auth/logout"))).toEqual({ logged_out: true });
    expect(() => decodeLoggedOut({ logged_out: false })).toThrow(d.DecodeError);
    expect(() => decodeLoggedOut({})).toThrow(d.DecodeError);
  });
});
