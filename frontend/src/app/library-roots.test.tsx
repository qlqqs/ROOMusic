import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installLibraryApi, type FakeLibrary } from "../test/fake-library";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const rootsPath = "/api/v1/library-roots";
const rootPath = /^\/api\/v1\/library-roots\/[^/]+$/;
const restorePath = /^\/api\/v1\/library-roots\/[^/]+\/restore$/;
// 后端样本里 classical-a（启用，revision 1）与 classical-b（已停用，revision 2）的目录 ID。
const classicalA = "00000000-0000-4000-8000-000000000001";

function setup(options: { role?: "admin" | "user"; library?: Partial<FakeLibrary> } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  const library = installLibraryApi(api, options.library);
  return { api, library };
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name });

function rowOf(name: string): HTMLElement {
  const row = screen.getByRole("rowheader", { name }).closest("tr");
  if (row === null) throw new Error(`没有找到目录 ${name} 所在的行`);
  return row;
}

async function openLibrary(options: Parameters<typeof setup>[0] = {}) {
  const context = setup(options);
  const rendered = renderApp({ path: "/settings/library" });
  await heading("扫描目录");
  await screen.findByRole("rowheader", { name: "classical-a" });
  return { ...context, ...rendered };
}

async function addRoot(path: string) {
  await userEvent.type(screen.getByLabelText("目录路径"), path);
  await userEvent.click(screen.getByRole("button", { name: "添加目录" }));
}

describe("扫描目录页：访问与列表", () => {
  it("管理员看到扫描、音乐目录、扫描记录与目录操作记录四个区段，目录只显示名称与状态", async () => {
    await openLibrary();
    for (const name of ["扫描", "音乐目录", "扫描记录", "目录操作记录"]) {
      expect(screen.getByRole("heading", { level: 2, name })).toBeTruthy();
    }
    expect(within(rowOf("classical-a")).getByText("启用")).toBeTruthy();
    expect(within(rowOf("classical-a")).getByRole("button", { name: "停用目录 classical-a" })).toBeTruthy();
    expect(within(rowOf("classical-b")).getByText("已停用")).toBeTruthy();
    expect(within(rowOf("classical-b")).getByRole("button", { name: "恢复目录 classical-b" })).toBeTruthy();
    expect(document.title).toBe("扫描目录 · ROOMusic");
  });

  it("设置分栏里管理员有“扫描目录”，当前页有 aria-current", async () => {
    await openLibrary();
    const nav = screen.getByRole("navigation", { name: "设置" });
    expect(within(nav).getByRole("link", { name: "扫描目录" }).getAttribute("aria-current")).toBe("page");
  });

  it("成员直接访问：显示“需要管理员权限”，不请求任何管理接口，导航里也没有这一项", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: "/settings/library" });
    expect(await heading("需要管理员权限")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "扫描目录" })).toBeNull();
    for (const path of [rootsPath, "/api/v1/library-root-operations", "/api/v1/scans/active", "/api/v1/scans"]) {
      expect(api.requestsTo("GET", path)).toHaveLength(0);
    }
  });

  it("空列表时提示添加第一个目录", async () => {
    setup({ library: { roots: [], operations: [] } });
    renderApp({ path: "/settings/library" });
    expect(await screen.findByText(/还没有音乐目录/)).toBeTruthy();
    expect(screen.getByText("还没有目录操作记录。")).toBeTruthy();
  });
});

describe("新增目录", () => {
  it("成功：请求带幂等键，列表刷新，提示结果，输入清空", async () => {
    const { api } = await openLibrary();
    await addRoot("/music/new-album");

    expect(await screen.findByText(/已添加目录“new-album”/)).toBeTruthy();
    const [request] = api.requestsTo("POST", rootsPath);
    expect(request?.body).toEqual({ path: "/music/new-album" });
    expect(request?.headers.get("Idempotency-Key")).toMatch(uuidPattern);
    expect(request?.headers.get("Content-Type")).toBe("application/json");
    expect(screen.getByRole("rowheader", { name: "new-album" })).toBeTruthy();
    expect((screen.getByLabelText("目录路径") as HTMLInputElement).value).toBe("");
  });

  it("路径被后端拒绝：错误显示在输入框旁并保留输入，修改后错误消失", async () => {
    await openLibrary();
    await addRoot("/elsewhere/album");

    const input = screen.getByLabelText("目录路径") as HTMLInputElement;
    expect(await screen.findByText("这个路径不在允许的音乐目录里，或者不是绝对路径。")).toBeTruthy();
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.value).toBe("/elsewhere/album");
    expect(screen.queryByRole("alert")).toBeNull();

    await userEvent.type(input, "x");
    expect(screen.queryByText("这个路径不在允许的音乐目录里，或者不是绝对路径。")).toBeNull();
  });

  it("没有填写路径：不发请求，提示填写", async () => {
    const { api } = await openLibrary();
    await userEvent.click(screen.getByRole("button", { name: "添加目录" }));
    expect(await screen.findByText("请输入目录的绝对路径。")).toBeTruthy();
    expect(api.requestsTo("POST", rootsPath)).toHaveLength(0);
  });

  it("路径已登记且停用：说明已登记但没有自动恢复，列表不变", async () => {
    await openLibrary();
    await addRoot("/music/classical-b");
    expect(await screen.findByText(/已经添加过，现在是停用的/)).toBeTruthy();
    expect(within(rowOf("classical-b")).getByText("已停用")).toBeTruthy();
  });

  it("路径已登记且启用：提示已经添加过", async () => {
    await openLibrary();
    await addRoot("/music/classical-a");
    expect(await screen.findByText(/已经添加过了。/)).toBeTruthy();
  });

  it("响应丢失（服务端已执行）：提示结果未确认，列表已显示新目录，重试沿用同一个幂等键且不重复创建", async () => {
    const { api, library } = await openLibrary({ library: { loseResponses: 1 } });
    await addRoot("/music/lost-response");

    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("结果未确认")).toBeTruthy();
    expect(within(alert).getByText(/不确定操作有没有完成/)).toBeTruthy();
    expect(await screen.findByRole("rowheader", { name: "lost-response" })).toBeTruthy();
    expect((screen.getByLabelText("目录路径") as HTMLInputElement).value).toBe("/music/lost-response");

    const operationsBefore = library.operations.length;
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    expect(await screen.findByText(/已经添加过了。/)).toBeTruthy();

    const [first, second] = api.requestsTo("POST", rootsPath);
    expect(second?.headers.get("Idempotency-Key")).toBe(first?.headers.get("Idempotency-Key"));
    expect(second?.body).toEqual(first?.body);
    expect(library.operations).toHaveLength(operationsBefore);
    expect(library.roots.filter((root) => root.name === "lost-response")).toHaveLength(1);
    // 重试成功后和首次提交成功一样清空输入。
    expect((screen.getByLabelText("目录路径") as HTMLInputElement).value).toBe("");
  });

  it("失败后用户改了输入再点“重试”：重试的是原来的路径，成功后不清掉用户新输入的内容", async () => {
    const { api } = await openLibrary({ library: { loseResponses: 1 } });
    await addRoot("/music/first-try");
    const alert = await screen.findByRole("alert");
    const input = screen.getByLabelText("目录路径") as HTMLInputElement;
    await userEvent.clear(input);
    await userEvent.type(input, "/music/other");

    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await screen.findByText(/已经添加过了。/);
    expect(api.requestsTo("POST", rootsPath).at(-1)?.body).toEqual({ path: "/music/first-try" });
    expect(input.value).toBe("/music/other");
  });

  it("不同路径的两次新增是两个意图，幂等键不同", async () => {
    const { api } = await openLibrary();
    await addRoot("/music/first");
    await screen.findByText(/已添加目录“first”/);
    await addRoot("/music/second");
    await screen.findByText(/已添加目录“second”/);
    const keys = api.requestsTo("POST", rootsPath).map((request) => request.headers.get("Idempotency-Key"));
    expect(keys).toHaveLength(2);
    expect(new Set(keys).size).toBe(2);
  });
});

describe("停用目录", () => {
  async function openDisableDialog(name = "classical-a") {
    await userEvent.click(within(rowOf(name)).getByRole("button", { name: `停用目录 ${name}` }));
    return screen.findByRole("dialog", { name: `停用目录“${name}”？` });
  }

  it("停用前要确认：对话框说明影响范围，初始焦点在取消上；取消不发请求", async () => {
    const { api } = await openLibrary();
    const dialog = await openDisableDialog();
    expect(within(dialog).getByText("停用后，扫描会跳过这个目录，里面的曲目也不能播放。恢复后就能再播。")).toBeTruthy();
    expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "取消" }));

    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("PATCH", rootPath)).toHaveLength(0);
  });

  it("确认后带着界面上看到的 revision 与幂等键发出请求，成功后关闭对话框并刷新列表", async () => {
    const { api, library } = await openLibrary();
    const dialog = await openDisableDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "停用目录" }));

    expect(await screen.findByText(/已停用目录“classical-a”/)).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    const [request] = api.requestsTo("PATCH", rootPath);
    expect(request?.body).toEqual({ status: "disabled", expected_revision: 1 });
    expect(request?.headers.get("Idempotency-Key")).toMatch(uuidPattern);
    expect(within(rowOf("classical-a")).getByText("已停用")).toBeTruthy();
    expect(within(rowOf("classical-a")).getByRole("button", { name: "恢复目录 classical-a" })).toBeTruthy();
    expect(library.operations[0]).toMatchObject({ operation: "disable", status: "succeeded", revision: 2 });
  });

  it("提交中按钮忙碌，重复点击不会再发请求", async () => {
    const { api } = await openLibrary();
    const gate = deferred<Response>();
    api.on("PATCH", `${rootsPath}/${classicalA}`, () => gate.promise);

    const dialog = await openDisableDialog();
    const confirm = within(dialog).getByRole("button", { name: "停用目录" });
    await userEvent.click(confirm);
    await waitFor(() => expect(confirm.getAttribute("aria-busy")).toBe("true"));
    await userEvent.click(confirm);
    expect(api.requestsTo("PATCH", `${rootsPath}/${classicalA}`)).toHaveLength(1);

    gate.resolve(jsonResponse(200, { id: classicalA, path: "classical-a", status: "disabled", revision: 2 }));
    expect(await screen.findByText(/已停用目录“classical-a”/)).toBeTruthy();
  });

  it("409 revision_conflict：目录在此期间变了，对话框关闭，列表已刷新并说明原因", async () => {
    const { api, library } = await openLibrary();
    const dialog = await openDisableDialog();
    const listsBefore = api.requestsTo("GET", rootsPath).length;
    // 另一位管理员在此期间操作了这个目录。
    const [first] = library.roots;
    if (first === undefined) throw new Error("没有种子目录");
    library.roots[0] = { ...first, revision: 5 };

    await userEvent.click(within(dialog).getByRole("button", { name: "停用目录" }));
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText(/目录刚有变化，列表已刷新/)).toBeTruthy();
    expect(within(alert).getByText("操作没有完成")).toBeTruthy();
    expect(within(alert).queryByRole("button", { name: "重试" })).toBeNull();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.requestsTo("GET", rootsPath).length).toBeGreaterThan(listsBefore);
    // 再次停用要用新的 revision：这是新的意图。
    await userEvent.click(within(alert).getByRole("button", { name: "关闭" }));
    await userEvent.click(within(rowOf("classical-a")).getByRole("button", { name: "停用目录 classical-a" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用目录" }));
    expect(await screen.findByText(/已停用目录“classical-a”/)).toBeTruthy();
    expect(api.requestsTo("PATCH", rootPath).at(-1)?.body).toEqual({ status: "disabled", expected_revision: 5 });
  });

  it("404 not_found：目录已不存在，提示并刷新", async () => {
    const { api } = await openLibrary();
    api.on("PATCH", `${rootsPath}/${classicalA}`, () =>
      jsonResponse(404, { error: { code: "not_found", message: "目录不存在" }, request_id: "sample-request" }),
    );
    await userEvent.click(within(await openDisableDialog()).getByRole("button", { name: "停用目录" }));
    expect(await screen.findByText("目录已不存在，列表已刷新。")).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("409 idempotency_conflict：显示错误并要求重新操作，没有重试按钮", async () => {
    const { api } = await openLibrary();
    api.on("PATCH", `${rootsPath}/${classicalA}`, () => sampleResponse("errors/idempotency-conflict"));
    await userEvent.click(within(await openDisableDialog()).getByRole("button", { name: "停用目录" }));
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("操作冲突了，请再试一次。")).toBeTruthy();
    expect(within(alert).queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("响应丢失（服务端已执行）：对话框保持打开并显示结果未确认，列表已是新状态；重试沿用同一个键，不重复执行", async () => {
    const { api, library } = await openLibrary({ library: { loseResponses: 1 } });
    const dialog = await openDisableDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "停用目录" }));

    expect(await within(dialog).findByText("结果未确认")).toBeTruthy();
    expect(within(dialog).getByText(/不确定操作有没有完成/)).toBeTruthy();
    expect(await within(rowOf("classical-a")).findByText("已停用")).toBeTruthy();
    const operationsBefore = library.operations.length;

    await userEvent.click(within(dialog).getByRole("button", { name: "重试停用" }));
    expect(await screen.findByText(/已停用目录“classical-a”/)).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();

    const [first, second] = api.requestsTo("PATCH", rootPath);
    expect(second?.headers.get("Idempotency-Key")).toBe(first?.headers.get("Idempotency-Key"));
    expect(second?.body).toEqual(first?.body);
    expect(library.operations).toHaveLength(operationsBefore);
  });

  it("请求丢失（服务端没有执行）：同样可以用同一个键重试，重试后真正执行", async () => {
    const { api, library } = await openLibrary({ library: { loseRequests: 1 } });
    const dialog = await openDisableDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "停用目录" }));
    expect(await within(dialog).findByText("结果未确认")).toBeTruthy();
    expect(within(rowOf("classical-a")).getByText("启用")).toBeTruthy();

    await userEvent.click(within(dialog).getByRole("button", { name: "重试停用" }));
    expect(await screen.findByText(/已停用目录“classical-a”/)).toBeTruthy();
    const [first, second] = api.requestsTo("PATCH", rootPath);
    expect(second?.headers.get("Idempotency-Key")).toBe(first?.headers.get("Idempotency-Key"));
    expect(library.roots[0]?.status).toBe("disabled");
  });

  it("结果未确认时取消：对话框关闭，页面上不残留错误", async () => {
    await openLibrary({ library: { loseResponses: 1 } });
    const dialog = await openDisableDialog();
    await userEvent.click(within(dialog).getByRole("button", { name: "停用目录" }));
    await within(dialog).findByText("结果未确认");

    await userEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("会话在操作中失效（401）：回到登录页", async () => {
    const { api } = await openLibrary();
    api.on("PATCH", `${rootsPath}/${classicalA}`, () => sampleResponse("errors/unauthorized"));
    await userEvent.click(within(await openDisableDialog()).getByRole("button", { name: "停用目录" }));
    expect(await heading("登录")).toBeTruthy();
    expect(screen.queryByRole("rowheader", { name: "classical-a" })).toBeNull();
  });
});

describe("恢复目录", () => {
  it("恢复直接执行，不需要确认：带 revision 与幂等键，成功后列表显示启用", async () => {
    const { api } = await openLibrary();
    await userEvent.click(within(rowOf("classical-b")).getByRole("button", { name: "恢复目录 classical-b" }));

    expect(await screen.findByText(/已恢复目录“classical-b”/)).toBeTruthy();
    expect(screen.queryByRole("dialog")).toBeNull();
    const [request] = api.requestsTo("POST", restorePath);
    expect(request?.body).toEqual({ expected_revision: 2 });
    expect(request?.headers.get("Idempotency-Key")).toMatch(uuidPattern);
    expect(within(rowOf("classical-b")).getByText("启用")).toBeTruthy();
  });

  it("恢复遇到 409：提示目录已变化，不假装成功", async () => {
    const { library } = await openLibrary();
    const second = library.roots[1];
    if (second === undefined) throw new Error("没有种子目录");
    library.roots[1] = { ...second, revision: 9 };
    await userEvent.click(within(rowOf("classical-b")).getByRole("button", { name: "恢复目录 classical-b" }));
    expect(await screen.findByText(/目录刚有变化，列表已刷新/)).toBeTruthy();
    expect(within(rowOf("classical-b")).getByText("已停用")).toBeTruthy();
  });

  it("恢复后的下一次停用与恢复是不同的意图，幂等键不同", async () => {
    const { api } = await openLibrary();
    await userEvent.click(within(rowOf("classical-b")).getByRole("button", { name: "恢复目录 classical-b" }));
    await screen.findByText(/已恢复目录“classical-b”/);
    await userEvent.click(within(rowOf("classical-b")).getByRole("button", { name: "停用目录 classical-b" }));
    await userEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "停用目录" }));
    await screen.findByText(/已停用目录“classical-b”/);

    const restoreKey = api.requestsTo("POST", restorePath)[0]?.headers.get("Idempotency-Key");
    const disableKey = api.requestsTo("PATCH", rootPath)[0]?.headers.get("Idempotency-Key");
    expect(restoreKey).toMatch(uuidPattern);
    expect(disableKey).toMatch(uuidPattern);
    expect(restoreKey).not.toBe(disableKey);
    // 停用请求带的是恢复之后的新 revision。
    expect(api.requestsTo("PATCH", rootPath)[0]?.body).toEqual({ status: "disabled", expected_revision: 3 });
  });
});

describe("目录操作记录", () => {
  it("显示操作、结果、revision 与错误码，目录名由目录 ID 对应，对应不上的显示占位", async () => {
    await openLibrary();
    const table = await screen.findByRole("table", { name: "目录操作记录" });
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(6);

    // 样本：最新一条是 classical-b 的失败停用，带错误码，没有 revision。
    const latest = within(rows[0] as HTMLElement).getAllByRole("cell");
    expect(latest.map((cell) => cell.textContent?.replace(/\s+/g, ""))).toEqual([expect.any(String), "classical-b", "停用", "失败", "—", "operation_failed"]);
    // 样本：最早一条的目录 ID 不在当前列表里。
    const earliest = within(rows[5] as HTMLElement).getAllByRole("cell");
    expect(earliest[1]?.textContent).toBe("—");
    expect(within(rows[1] as HTMLElement).getByText("恢复")).toBeTruthy();
    expect(within(rows[1] as HTMLElement).getByText("成功")).toBeTruthy();
  });

  it("新的操作完成后记录刷新，最新的在最前", async () => {
    await openLibrary();
    await userEvent.click(within(rowOf("classical-b")).getByRole("button", { name: "恢复目录 classical-b" }));
    await screen.findByText(/已恢复目录“classical-b”/);
    const table = await screen.findByRole("table", { name: "目录操作记录" });
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(8));
    const first = within(within(table).getAllByRole("row")[1] as HTMLElement).getAllByRole("cell");
    expect(first[2]?.textContent).toBe("恢复");
    expect(first[3]?.textContent).toContain("成功");
  });
});
