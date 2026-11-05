import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi, type FakeSession } from "../test/fake-api";
import { installMembersApi, type FakeMembers } from "../test/fake-members";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const usersPath = "/api/v1/users";
const userPath = /^\/api\/v1\/users\/[^/]+$/;
const revokePath = /^\/api\/v1\/users\/[^/]+\/sessions\/revoke$/;
const password = "correct horse battery";
// 后端样本里 listener（成员，启用）与 curator（管理员，启用）、archivist（成员，已停用）的用户 ID 由列表给出，这里按用户名取。

function setup(options: { role?: "admin" | "user"; members?: Partial<FakeMembers> } = {}) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const members = installMembersApi(api, session, options.members);
  return { api, session, members };
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });

function rowOf(name: string): HTMLElement {
  const row = screen.getByRole("rowheader", { name: new RegExp(`^${name}`) }).closest("tr");
  if (row === null) throw new Error(`没有找到成员 ${name} 所在的行`);
  return row;
}

async function openMembers(options: Parameters<typeof setup>[0] = {}) {
  const context = setup(options);
  const rendered = renderApp({ path: "/members" });
  await heading("成员");
  await screen.findByRole("rowheader", { name: /^listener/ });
  return { ...context, ...rendered };
}

async function fillForm(username: string, secret = password, confirmation = secret) {
  await userEvent.type(screen.getByLabelText("用户名"), username);
  await userEvent.type(screen.getByLabelText("密码"), secret);
  await userEvent.type(screen.getByLabelText("确认密码"), confirmation);
}

const create = () => userEvent.click(screen.getByRole("button", { name: "创建成员" }));

function memberId(members: FakeMembers, username: string): string {
  const user = members.users.find((candidate) => candidate.username === username);
  if (user === undefined) throw new Error(`没有成员 ${username}`);
  return user.id;
}

function addAdmin(session: FakeSession, members: FakeMembers, username: string) {
  members.users = [...members.users, { id: "00000000-0000-4000-8000-0000000000aa", username, role: "admin", disabled: false, created_at: "2026-01-01T00:00:00Z" }];
  session.accounts[username] = { id: "00000000-0000-4000-8000-0000000000aa", password, role: "admin" };
}

describe("成员页：访问与列表", () => {
  it("管理员看到成员列表：角色、状态、标出自己，操作按状态给出", async () => {
    await openMembers();
    expect(document.title).toBe("成员 · ROOMusic");
    expect(screen.getByRole("heading", { level: 2, name: "新建成员" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 2, name: "成员列表" })).toBeTruthy();

    const names = within(screen.getByRole("table", { name: "成员列表" })).getAllByRole("rowheader").map((header) => header.textContent);
    expect(names).toEqual(["archivist", "curator你", "listener"]);

    const curator = rowOf("curator");
    expect(within(curator).getByText("你")).toBeTruthy();
    expect(within(curator).getByText("管理员")).toBeTruthy();
    expect(within(curator).getByText("启用")).toBeTruthy();
    expect(within(curator).getByRole("button", { name: "停用成员 curator" })).toBeTruthy();
    expect(within(curator).getByRole("button", { name: "撤销会话 curator" })).toBeTruthy();

    // 已停用的成员没有会话可撤销，只提供重新启用。
    const archivist = rowOf("archivist");
    expect(within(archivist).getByText("已停用")).toBeTruthy();
    expect(within(archivist).getByText("成员")).toBeTruthy();
    expect(within(archivist).getByRole("button", { name: "重新启用成员 archivist" })).toBeTruthy();
    expect(within(archivist).queryByRole("button", { name: /停用成员|撤销会话/ })).toBeNull();
    expect(within(archivist).queryByText("你")).toBeNull();
  });

  it("“你”按用户 ID 判断，不看用户名：查看者的 ID 对上哪一行，哪一行标“你”，撤销确认框同理", async () => {
    const { session, members } = setup();
    const curator = session.accounts.curator;
    if (curator === undefined) throw new Error("替身缺少 curator 账号");
    // 用户名仍是 curator，但 ID 是 listener 那一行的 ID。
    session.accounts.curator = { ...curator, id: memberId(members, "listener") };
    renderApp({ path: "/members" });
    await heading("成员");
    await screen.findByRole("rowheader", { name: /^listener/ });

    expect(within(rowOf("listener")).getByText("你")).toBeTruthy();
    expect(within(rowOf("curator")).queryByText("你")).toBeNull();
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "撤销会话 listener" }));
    expect(within(await screen.findByRole("dialog")).getByText("这是你自己的账号，确认后你会马上退出登录。")).toBeTruthy();
  });

  it("查看者的 ID 不在列表里：即使有同名成员，也没有任何一行标“你”，确认框不提示是自己的账号", async () => {
    const { session } = setup();
    const curator = session.accounts.curator;
    if (curator === undefined) throw new Error("替身缺少 curator 账号");
    session.accounts.curator = { ...curator, id: "00000000-0000-4000-8000-0000000000ee" };
    renderApp({ path: "/members" });
    await heading("成员");
    await screen.findByRole("rowheader", { name: /^listener/ });

    expect(screen.queryByText("你")).toBeNull();
    await userEvent.click(within(rowOf("curator")).getByRole("button", { name: "撤销会话 curator" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).queryByText(/这是你自己的账号/)).toBeNull();
  });

  it("导航里有“资料库”分组下的“成员”，当前页有 aria-current", async () => {
    await openMembers();
    const nav = screen.getByRole("navigation", { name: "主导航" });
    expect(within(nav).getByRole("link", { name: "成员" }).getAttribute("aria-current")).toBe("page");
    expect(within(nav).getByRole("link", { name: "扫描目录" })).toBeTruthy();
  });

  it("新建表单只能创建“成员”角色：没有选择角色的控件", async () => {
    await openMembers();
    const form = screen.getByRole("button", { name: "创建成员" }).closest("form") as HTMLElement;
    expect(within(form).queryByRole("radio")).toBeNull();
    expect(within(form).queryByRole("combobox")).toBeNull();
  });

  it("成员直接访问：显示“需要管理员权限”，不请求成员接口，导航里也没有这一项", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: "/members" });
    expect(await heading("需要管理员权限")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "成员" })).toBeNull();
    expect(api.requestsTo("GET", usersPath)).toHaveLength(0);
  });

  it("读取失败（403，会话保留）：显示带请求 ID 的拒绝与重试，不退出登录", async () => {
    const { api } = setup();
    api.on("GET", usersPath, () => sampleResponse("errors/forbidden"));
    renderApp({ path: "/members" });
    await heading("成员");
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("你没有权限执行此操作。")).toBeTruthy();
    expect(within(alert).getByText(/请求 ID：/)).toBeTruthy();
    expect(within(alert).getByRole("button", { name: "重试" })).toBeTruthy();
    expect(screen.getByRole("navigation", { name: "主导航" })).toBeTruthy();
  });
});

describe("新建成员", () => {
  it("成功：用户名去空白后提交，请求没有幂等键，列表出现新成员并清空表单，提示里不回显密码", async () => {
    const { api } = await openMembers();
    await fillForm("  newbie  ");
    await create();

    expect(await screen.findByText(/已创建成员“newbie”/)).toBeTruthy();
    const [request] = api.requestsTo("POST", usersPath);
    expect(request?.body).toEqual({ username: "newbie", password });
    expect(request?.headers.get("Idempotency-Key")).toBeNull();
    expect(request?.headers.get("Content-Type")).toBe("application/json");
    const row = await waitFor(() => rowOf("newbie"));
    expect(within(row).getByText("成员")).toBeTruthy();
    expect(within(row).getByText("启用")).toBeTruthy();
    for (const label of ["用户名", "密码", "确认密码"]) expect((screen.getByLabelText(label) as HTMLInputElement).value).toBe("");
    expect(document.body.textContent).not.toContain(password);
  });

  it("账号字段不让浏览器自动填入当前管理员的凭据", async () => {
    await openMembers();
    expect(screen.getByLabelText("用户名").getAttribute("autocomplete")).toBe("off");
    expect(screen.getByLabelText("密码").getAttribute("autocomplete")).toBe("new-password");
    expect(screen.getByLabelText("确认密码").getAttribute("autocomplete")).toBe("new-password");
  });

  it("填写不合规：不发请求，错误显示在对应字段并把焦点移过去", async () => {
    const { api } = await openMembers();
    await create();
    expect(await screen.findByText("请输入用户名。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByLabelText("用户名"));

    await userEvent.type(screen.getByLabelText("用户名"), "newbie");
    await userEvent.type(screen.getByLabelText("密码"), "short");
    await create();
    expect(await screen.findByText("密码至少要 12 位。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByLabelText("密码"));

    await userEvent.clear(screen.getByLabelText("密码"));
    await userEvent.type(screen.getByLabelText("密码"), password);
    await userEvent.type(screen.getByLabelText("确认密码"), "different password");
    await create();
    expect(await screen.findByText("两次输入的密码不一致。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByLabelText("确认密码"));
    expect(api.requestsTo("POST", usersPath)).toHaveLength(0);
  });

  it("密码只能用英文字母、数字和符号：中文密码不发请求，字段提示；换成英文后可以创建", async () => {
    const { api } = await openMembers();
    await fillForm("han", "密码密码密码密码密码密码");
    await create();
    expect(await screen.findByText("密码只能用英文字母、数字和符号。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByLabelText("密码"));
    expect(api.requestsTo("POST", usersPath)).toHaveLength(0);

    await userEvent.clear(screen.getByLabelText("密码"));
    await userEvent.clear(screen.getByLabelText("确认密码"));
    await userEvent.type(screen.getByLabelText("密码"), "abcdefghijkl");
    await userEvent.type(screen.getByLabelText("确认密码"), "abcdefghijkl");
    await create();
    expect(await screen.findByText(/已创建成员“han”/)).toBeTruthy();
    expect(api.requestsTo("POST", usersPath)[0]?.body).toEqual({ username: "han", password: "abcdefghijkl" });
  });

  it("密码最多 72 位：超出时不发请求，字段提示密码太长；恰好 72 位可以", async () => {
    const { api } = await openMembers();
    await fillForm("wide", "a".repeat(73));
    await create();
    expect(await screen.findByText("密码太长了，请短一些。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByLabelText("密码"));
    expect(api.requestsTo("POST", usersPath)).toHaveLength(0);

    await userEvent.clear(screen.getByLabelText("密码"));
    await userEvent.clear(screen.getByLabelText("确认密码"));
    await userEvent.type(screen.getByLabelText("密码"), "a".repeat(72));
    await userEvent.type(screen.getByLabelText("确认密码"), "a".repeat(72));
    await create();
    expect(await screen.findByText(/已创建成员“wide”/)).toBeTruthy();
    expect(api.requestsTo("POST", usersPath)[0]?.body).toEqual({ username: "wide", password: "a".repeat(72) });
  });

  it("用户名已存在（409）：错误显示在用户名旁并保留输入，页面上没有重复的提示，改了用户名后错误消失", async () => {
    await openMembers();
    await fillForm("listener");
    await create();

    expect(await screen.findByText("用户名已存在，请换一个。")).toBeTruthy();
    const username = screen.getByLabelText("用户名") as HTMLInputElement;
    expect(username.getAttribute("aria-invalid")).toBe("true");
    expect(username.value).toBe("listener");
    expect((screen.getByLabelText("密码") as HTMLInputElement).value).toBe(password);
    expect(screen.queryByRole("alert")).toBeNull();

    await userEvent.type(username, "2");
    expect(screen.queryByText("用户名已存在，请换一个。")).toBeNull();
  });

  it("后端以 invalid_input 拒绝（400）：按错误码显示文案，不直接显示后端的 message", async () => {
    const { api } = await openMembers();
    api.on("POST", usersPath, () => sampleResponse("errors/invalid-input"));
    await fillForm("newbie");
    await create();
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("用户名不能为空，密码至少 12 位，只能用英文字母、数字和符号。")).toBeTruthy();
    expect(within(alert).getByText("操作没有完成")).toBeTruthy();
  });

  it("响应丢失（服务端已创建）：提示结果未确认，列表已显示新成员，不提供重试，表单保留输入", async () => {
    const { api } = await openMembers({ members: { loseResponses: 1 } });
    await fillForm("lost-response");
    await create();

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("结果未确认")).toBeTruthy();
    expect(within(alert).getByText(/不确定操作有没有完成/)).toBeTruthy();
    expect(within(alert).getByText(/列表已刷新，请看一下成员现在的状态再操作/)).toBeTruthy();
    expect(within(alert).queryByRole("button", { name: "重试" })).toBeNull();
    expect(await screen.findByRole("rowheader", { name: /^lost-response/ })).toBeTruthy();
    expect((screen.getByLabelText("用户名") as HTMLInputElement).value).toBe("lost-response");
    // 没有自动重试：只发过一次请求。
    expect(api.requestsTo("POST", usersPath)).toHaveLength(1);

    // 用户核对后重新提交，服务器以“用户名已存在”确认它已经创建。
    await create();
    expect(await screen.findByText("用户名已存在，请换一个。")).toBeTruthy();
    expect(api.requestsTo("POST", usersPath)).toHaveLength(2);
  });

  it("提交中按钮忙碌，重复点击不会再发请求", async () => {
    const { api } = await openMembers();
    const gate = deferred<Response>();
    api.on("POST", usersPath, () => gate.promise);
    await fillForm("slow");
    await create();
    const button = screen.getByRole("button", { name: "正在创建…" });
    await waitFor(() => expect(button.getAttribute("aria-busy")).toBe("true"));
    await userEvent.click(button);
    expect(api.requestsTo("POST", usersPath)).toHaveLength(1);
    gate.resolve(jsonResponse(201, { id: "00000000-0000-4000-8000-0000000000bb", username: "slow", role: "user" }));
    expect(await screen.findByText(/已创建成员“slow”/)).toBeTruthy();
  });
});

describe("停用、重新启用与撤销会话", () => {
  it("停用前要确认：对话框说明会马上退出登录、之后可重新启用，初始焦点在取消上；取消不发请求", async () => {
    const { api } = await openMembers();
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    const dialog = await screen.findByRole("dialog", { name: "停用成员“listener”？" });
    expect(within(dialog).getByText(/停用后对方会马上退出登录，之后可以重新启用/)).toBeTruthy();
    expect(within(dialog).queryByText(/这是你自己的账号/)).toBeNull();
    expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));

    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("PATCH", userPath)).toHaveLength(0);
  });

  it("确认后停用：请求没有幂等键，成功后对话框关闭、列表显示已停用并提供重新启用", async () => {
    const { api, members } = await openMembers();
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));

    expect(await screen.findByText(/已停用成员“listener”/)).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    const [request] = api.requestsTo("PATCH", userPath);
    expect(request?.path).toBe(`${usersPath}/${memberId(members, "listener")}`);
    expect(request?.body).toEqual({ disabled: true });
    expect(request?.headers.get("Idempotency-Key")).toBeNull();
    const row = await waitFor(() => {
      const found = rowOf("listener");
      expect(within(found).getByText("已停用")).toBeTruthy();
      return found;
    });
    expect(within(row).getByRole("button", { name: "重新启用成员 listener" })).toBeTruthy();
    expect(within(row).queryByRole("button", { name: "撤销会话 listener" })).toBeNull();
  });

  it("重新启用直接执行，不需要确认", async () => {
    const { api } = await openMembers();
    await userEvent.click(within(rowOf("archivist")).getByRole("button", { name: "重新启用成员 archivist" }));
    expect(await screen.findByText(/已重新启用成员“archivist”/)).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("PATCH", userPath)[0]?.body).toEqual({ disabled: false });
    await waitFor(() => expect(within(rowOf("archivist")).getByText("启用")).toBeTruthy());
  });

  it("撤销会话要确认：说明对方需要重新登录而账号不受影响，确认后发请求并提示", async () => {
    const { api, members } = await openMembers();
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "撤销会话 listener" }));
    const dialog = await screen.findByRole("dialog", { name: "撤销成员“listener”的全部会话？" });
    expect(within(dialog).getByText(/对方的所有设备都会退出登录，需要重新登录/)).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "撤销会话" }));

    expect(await screen.findByText(/已撤销成员“listener”的全部会话/)).toBeTruthy();
    expect(api.requestsTo("POST", revokePath)[0]?.path).toBe(`${usersPath}/${memberId(members, "listener")}/sessions/revoke`);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("目标是自己：对话框额外提示会被退出登录；停用最后一个管理员被拒绝（409）时会话保留", async () => {
    await openMembers();
    await userEvent.click(within(rowOf("curator")).getByRole("button", { name: "停用成员 curator" }));
    const dialog = await screen.findByRole("dialog", { name: "停用成员“curator”？" });
    expect(within(dialog).getByText("这是你自己的账号，确认后你会马上退出登录。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "停用成员" }));

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("不能停用最后一个管理员。")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    // 会话没有结束：仍在成员页，列表仍显示自己启用。
    expect(screen.getByRole("heading", { level: 1, name: "成员" })).toBeTruthy();
    expect(within(rowOf("curator")).getByText("启用")).toBeTruthy();
  });

  it("停用自己（还有别的管理员）成功后，会话被撤销，随后按 401 流程回到登录页，不再显示成员数据", async () => {
    const { session, members } = setup();
    addAdmin(session, members, "second-admin");
    renderApp({ path: "/members" });
    await heading("成员");
    await userEvent.click(await screen.findByRole("button", { name: "停用成员 curator" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));

    expect(await heading("登录")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "成员列表" })).toBeNull();
    expect(members.users.find((user) => user.username === "curator")?.disabled).toBe(true);
  });

  it("撤销自己的会话：同样会被退出登录", async () => {
    await openMembers();
    await userEvent.click(within(rowOf("curator")).getByRole("button", { name: "撤销会话 curator" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("这是你自己的账号，确认后你会马上退出登录。")).toBeTruthy();
    await userEvent.click(within(dialog).getByRole("button", { name: "撤销会话" }));
    expect(await heading("登录")).toBeTruthy();
  });

  it("成员已不存在（404）：提示并刷新列表", async () => {
    const { api, members } = await openMembers();
    members.users = members.users.filter((user) => user.username !== "listener");
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));
    expect(await screen.findByText("成员已不存在，列表已刷新。")).toBeTruthy();
    await waitFor(() => expect(screen.queryByRole("rowheader", { name: /^listener/ })).toBeNull());
    expect(api.requestsTo("GET", usersPath).length).toBeGreaterThan(1);
  });

  it("撤销会话时成员已不存在（404）：提示并刷新列表，不再提示撤销成功", async () => {
    const { api, members } = await openMembers();
    members.users = members.users.filter((user) => user.username !== "listener");
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "撤销会话 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "撤销会话" }));
    expect(await screen.findByText("成员已不存在，列表已刷新。")).toBeTruthy();
    expect(screen.queryByText(/已撤销成员/)).toBeNull();
    await waitFor(() => expect(screen.queryByRole("rowheader", { name: /^listener/ })).toBeNull());
    expect(api.requestsTo("GET", usersPath).length).toBeGreaterThan(1);
  });

  it("响应丢失（服务端已停用）：对话框关闭，提示结果未确认，列表已显示已停用，不提供重试也不自动重试", async () => {
    const { api } = await openMembers({ members: { loseResponses: 1 } });
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("结果未确认")).toBeTruthy();
    expect(within(alert).queryByRole("button", { name: "重试" })).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    await waitFor(() => expect(within(rowOf("listener")).getByText("已停用")).toBeTruthy());
    expect(api.requestsTo("PATCH", userPath)).toHaveLength(1);
  });

  it("请求丢失（服务端没有执行）：列表仍是启用，用户核对后可以重新操作", async () => {
    const { api } = await openMembers({ members: { loseRequests: 1 } });
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));
    expect(await screen.findByText("结果未确认")).toBeTruthy();
    expect(within(rowOf("listener")).getByText("启用")).toBeTruthy();

    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));
    expect(await screen.findByText(/已停用成员“listener”/)).toBeTruthy();
    expect(api.requestsTo("PATCH", userPath)).toHaveLength(2);
  });

  it("会话在操作中失效（401）：回到登录页", async () => {
    const { api } = await openMembers();
    api.on("PATCH", userPath, () => sampleResponse("errors/unauthorized"));
    await userEvent.click(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用成员" }));
    expect(await heading("登录")).toBeTruthy();
    expect(screen.queryByRole("table", { name: "成员列表" })).toBeNull();
  });

  it("提交中所有操作按钮忙碌，重复点击不会再发请求", async () => {
    const { api } = await openMembers();
    const gate = deferred<Response>();
    api.on("PATCH", userPath, () => gate.promise);
    await userEvent.click(within(rowOf("archivist")).getByRole("button", { name: "重新启用成员 archivist" }));
    await waitFor(() => expect(within(rowOf("archivist")).getByRole("button", { name: "重新启用成员 archivist" }).getAttribute("aria-busy")).toBe("true"));
    expect(within(rowOf("listener")).getByRole("button", { name: "停用成员 listener" }).getAttribute("aria-busy")).toBe("true");
    await userEvent.click(within(rowOf("archivist")).getByRole("button", { name: "重新启用成员 archivist" }));
    expect(api.requestsTo("PATCH", userPath)).toHaveLength(1);
    gate.resolve(jsonResponse(200, { disabled: false }));
    expect(await screen.findByText(/已重新启用成员“archivist”/)).toBeTruthy();
  });
});

describe("概览里的入口", () => {
  it("管理员的概览有进入成员管理的链接，成员的概览没有", async () => {
    setup();
    renderApp({ path: "/" });
    const link = await screen.findByRole("link", { name: "管理成员" });
    expect(link.getAttribute("href")).toBe("/members");
  });

  it("成员的概览看不到成员区块与管理链接", async () => {
    setup({ role: "user" });
    renderApp({ path: "/" });
    await heading("概览");
    expect(screen.queryByRole("link", { name: "管理成员" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 2, name: "成员" })).toBeNull();
  });
});
