import { decodeMemberList, type MemberDTO } from "../features/members/api/decoders";
import { sampleBody } from "./api-samples";
import type { FakeApi, FakeSession } from "./fake-api";
import { jsonResponse, sampleResponse } from "./http";

/** 成员接口替身的状态：测试可以直接读写，用来预置账号或模拟别处的改动。 */
export type FakeMembers = {
  /** 按用户名排序，与后端一致。 */
  users: MemberDTO[];
  /** 接下来这么多次成员写请求在到达服务端之前就中断（服务端没有执行）。 */
  loseRequests: number;
  /** 接下来这么多次成员写请求服务端已经执行，但响应丢失（客户端得到网络失败）。 */
  loseResponses: number;
};

const byUsername = (left: MemberDTO, right: MemberDTO) => (left.username < right.username ? -1 : left.username > right.username ? 1 : 0);
const utf8Length = (value: string) => new TextEncoder().encode(value).length;

/**
 * 成员接口的替身，行为依据后端实现：未登录 401、非管理员 403；新建只能是 user 角色，用户名去空白后非空且密码至少 12 字节，
 * 重复用户名 409；停用最后一个启用的管理员 409 last_admin；停用与撤销会话会撤销目标的全部会话
 * （目标是当前登录的账号时，替身把会话置为失效，之后的请求得到 401）。成员写操作没有幂等保证。
 */
export function installMembersApi(api: FakeApi, session: FakeSession, overrides: Partial<FakeMembers> = {}): FakeMembers {
  const members: FakeMembers = {
    users: decodeMemberList(sampleBody("users/list")).items,
    loseRequests: 0,
    loseResponses: 0,
    ...overrides,
  };
  let nextUser = 100;

  const admin = (): Response | null => {
    if (session.current === null) return sampleResponse("errors/unauthorized");
    return session.accounts[session.current]?.role === "admin" ? null : sampleResponse("errors/forbidden");
  };
  const unreliable = (execute: () => Response): Response => {
    if (members.loseRequests > 0) {
      members.loseRequests -= 1;
      throw new TypeError("Failed to fetch");
    }
    const response = execute();
    if (members.loseResponses > 0) {
      members.loseResponses -= 1;
      throw new TypeError("Failed to fetch");
    }
    return response;
  };
  const revokeSessionsOf = (user: MemberDTO) => {
    if (user.username === session.current) session.current = null;
  };
  const idOf = (path: string) => /\/users\/([^/?]+)/.exec(path)?.[1] ?? "";

  api.on("GET", "/api/v1/users", () => admin() ?? jsonResponse(200, { items: members.users }));

  api.on("POST", "/api/v1/users", (request) =>
    unreliable(() => {
      const denied = admin();
      if (denied !== null) return denied;
      const { username, password } = request.body as { username: string; password: string };
      if (username.trim() === "" || utf8Length(password) < 12) return sampleResponse("errors/invalid-input");
      if (members.users.some((user) => user.username === username)) return sampleResponse("errors/username-taken");
      const created: MemberDTO = {
        id: `00000000-0000-4000-8000-${String(nextUser++).padStart(12, "0")}`,
        username,
        role: "user",
        disabled: false,
        created_at: "2026-01-02T00:00:00Z",
      };
      members.users = [...members.users, created].sort(byUsername);
      session.accounts[username] = { password, role: "user" };
      return jsonResponse(201, { id: created.id, username, role: "user" });
    }),
  );

  api.on("PATCH", /^\/api\/v1\/users\/[^/]+$/, (request) =>
    unreliable(() => {
      const denied = admin();
      if (denied !== null) return denied;
      const { disabled } = request.body as { disabled: boolean };
      const index = members.users.findIndex((user) => user.id === idOf(request.path));
      const target = members.users[index];
      if (target === undefined) return jsonResponse(404, { error: { code: "not_found", message: "用户不存在" }, request_id: "sample-request" });
      const enabledAdmins = members.users.filter((user) => user.role === "admin" && !user.disabled).length;
      if (disabled && !target.disabled && target.role === "admin" && enabledAdmins <= 1) return sampleResponse("errors/last-admin");
      members.users[index] = { ...target, disabled };
      if (disabled) revokeSessionsOf(target);
      return jsonResponse(200, { disabled });
    }),
  );

  api.on("POST", /^\/api\/v1\/users\/[^/]+\/sessions\/revoke$/, (request) =>
    unreliable(() => {
      const denied = admin();
      if (denied !== null) return denied;
      const target = members.users.find((user) => user.id === idOf(request.path));
      if (target !== undefined) revokeSessionsOf(target);
      return jsonResponse(200, { revoked: true });
    }),
  );

  return members;
}
