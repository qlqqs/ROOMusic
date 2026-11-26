import { describe, expect, it } from "vitest";
import { decodeProxySettings, decodeProxyTest } from "./decoders";

const saved = { enabled: true, scheme: "socks5", host: "192.168.1.2", port: 1080, username: "u", password_set: true, version: 4 };

describe("代理设置解码", () => {
  it("接受已保存与未填写的设置", () => {
    expect(decodeProxySettings(saved)).toEqual(saved);
    const blank = { enabled: false, scheme: "http", host: "", port: null, username: "", password_set: false, version: 0 };
    expect(decodeProxySettings(blank)).toEqual(blank);
  });

  it("拒绝未知类型、越界端口与缺少的字段", () => {
    expect(() => decodeProxySettings({ ...saved, scheme: "socks4" })).toThrow();
    expect(() => decodeProxySettings({ ...saved, port: 70000 })).toThrow();
    const { password_set: _omitted, ...missing } = saved;
    expect(() => decodeProxySettings(missing)).toThrow();
  });

  it("测试结果：成功时 code 为空，失败码限定在四种", () => {
    expect(decodeProxyTest({ ok: true, code: null, latency_ms: 120 })).toEqual({ ok: true, code: null, latency_ms: 120 });
    expect(decodeProxyTest({ ok: false, code: "timeout", latency_ms: null }).code).toBe("timeout");
    expect(() => decodeProxyTest({ ok: false, code: "dns_failed", latency_ms: null })).toThrow();
  });
});
