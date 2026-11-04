import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { formatDateTime } from "../shared/lib/time";
import { claimDetail, claimReleaseId } from "../test/claim-data";
import { alpha, beta, narrowContainer, reviewedAt, setup } from "../test/claims-setup";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { installGovernanceApi } from "../test/fake-governance";
import { respondToClaims, seedClaim } from "../test/governance-data";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const heading = (name: string, level = 1) => screen.findByRole("heading", { level, name });
const claimList = () => screen.getByRole("heading", { level: 2, name: "留有修改的旧版本" }).closest("section") as HTMLElement;
// 列表里的旧版本链接：位于列表的 ul 内，不含分页导航里的页码链接。
const listLinks = () => within(claimList()).getAllByRole("link").filter((link) => link.closest("ul") !== null && link.closest("nav") === null);

describe("待认领页：入口与权限", () => {
  it("管理员在“资料库”下看到“待认领”，点进去是待认领页，不显示数量", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/" });
    const nav = await screen.findByRole("navigation", { name: "主导航" });
    const link = within(nav).getByRole("link", { name: "待认领" });
    expect(link.textContent).toBe("待认领");
    await user.click(link);
    expect(await heading("待认领")).toBeTruthy();
    expect(history.location.pathname).toBe("/claims");
  });

  it("成员直接访问：显示“需要管理员权限”，不请求待认领的接口，导航里也没有这一项", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: "/claims" });
    expect(await heading("需要管理员权限")).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "主导航" })).queryByRole("link", { name: "待认领" })).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/release-claims")).toHaveLength(0);
  });
});

describe("待认领页：列表", () => {
  it("最近看到的在前；每行写旧版本的名字、原来的专辑、位置、最后一次看到的时间与留着的修改", async () => {
    const { api } = setup();
    renderApp({ path: "/claims" });
    await screen.findByRole("link", { name: /Alpha Suites \(corrected\)/ });
    expect(listLinks().map((link) => link.textContent)).toEqual([
      `Alpha Suites (corrected) classical / Alpha/Suites 最后一次看到 ${formatDateTime("2026-01-20T08:00:00Z")} 修正 2 项`,
      `Gamma Sonatas 原来在《Gamma Sonatas Collection》的专辑里 classical / Gamma 最后一次看到 ${formatDateTime("2026-01-10T08:00:00Z")} 修正 1 项 · 专辑 · 默认版本 · 核对 2 项`,
      `Delta Partitas classical 最后一次看到 ${formatDateTime("2025-12-01T08:00:00Z")} 修正 2 项`,
    ]);
    expect(screen.getByText("共 3 个版本")).toBeTruthy();
    expect(screen.getByText("从列表里选一个版本。")).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/release-claims").at(-1)?.path).toBe("/api/v1/release-claims?page=1&page_size=30");
  });

  it("没有待认领的版本时显示空态，不显示分页", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    installGovernanceApi(api, installCatalogApi(api, { groups: [alpha()] }));
    renderApp({ path: "/claims" });
    expect(await screen.findByText("没有待认领的版本。")).toBeTruthy();
    expect(screen.queryByRole("navigation", { name: "待认领列表分页" })).toBeNull();
  });

  it("读取失败时显示原因与“重试”，重试成功后列出旧版本", async () => {
    const { api, governance } = setup();
    const user = userEvent.setup();
    let failures = 1;
    api.on("GET", "/api/v1/release-claims", (request) => {
      if (failures-- > 0) return jsonResponse(500, { error: { code: "internal_error", message: "服务出错了" }, request_id: "req-1" });
      const result = respondToClaims(governance, new URL(request.path, "http://localhost").searchParams);
      return jsonResponse(result.status, result.body);
    });
    renderApp({ path: "/claims" });
    await user.click(await screen.findByRole("button", { name: "重试" }));
    expect(await screen.findByRole("link", { name: /Alpha Suites \(corrected\)/ })).toBeTruthy();
  });

  it("翻页写进地址，页码越界时回到末页", async () => {
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    const governance = installGovernanceApi(api, installCatalogApi(api, { groups: [alpha()] }));
    for (let index = 1; index <= 35; index += 1) seedClaim(governance, claimDetail(index, { title: `Old ${String(index).padStart(2, "0")}` }));
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/claims" });
    await screen.findByRole("link", { name: /Old 01/ });
    expect(listLinks()).toHaveLength(30);
    await user.click(within(screen.getByRole("navigation", { name: "待认领列表分页" })).getByRole("link", { name: /下一页|第 2 页|2/ }));
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
    expect(await screen.findByRole("link", { name: /Old 31/ })).toBeTruthy();
    expect(listLinks()).toHaveLength(5);
    // 越界的页码改到末页（替换历史项）。
    await act(async () => {
      await history.push("/claims?page=9");
    });
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
  });
});

describe("待认领页：翻页与选中", () => {
  it("在第 2 页选中一个版本，页码保留在地址里；返回列表时仍在第 2 页", async () => {
    narrowContainer(600);
    const api = installFakeApi();
    installFakeSession(api, { current: "curator" });
    installOverviewApi(api);
    const governance = installGovernanceApi(api, installCatalogApi(api, { groups: [alpha()] }));
    for (let index = 1; index <= 35; index += 1) seedClaim(governance, claimDetail(index, { title: `Old ${String(index).padStart(2, "0")}` }));
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/claims?page=2" });
    await user.click(await screen.findByRole("link", { name: /Old 31/ }));
    await waitFor(() => expect(history.location.search).toBe(`?page=2&release=${claimReleaseId(31)}`));
    await user.click(await screen.findByRole("link", { name: "返回待认领列表" }));
    await waitFor(() => expect(history.location.search).toBe("?page=2"));
    expect(await screen.findByRole("link", { name: /Old 31/ })).toBeTruthy();
  });
});

describe("待认领页：详情", () => {
  it("选中一个旧版本写进地址，右栏列出它留着的修改和推荐的新版本（理由写明文字）", async () => {
    const { api } = setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/claims" });
    await user.click(await screen.findByRole("link", { name: /Alpha Suites \(corrected\)/ }));
    await waitFor(() => expect(history.location.search).toBe(`?release=${claimReleaseId(1)}`));
    expect(await heading("Alpha Suites (corrected)", 2)).toBeTruthy();
    expect(listLinks()[0]?.getAttribute("aria-current")).toBe("page");
    expect(screen.getByText(/扫描时没找到它的音乐文件，最后一次是 .* 在 classical \/ Alpha\/Suites 看到的。/)).toBeTruthy();

    const corrections = screen.getByRole("table", { name: "旧版本上的字段修正" });
    expect(within(corrections).getAllByRole("row").map((row) => row.textContent)).toEqual([
      "字段你改成了扫描值",
      "标题改成“Old Edition 1 (corrected)”Old Edition 1",
      "流派改成“Baroque”（空）",
    ]);
    // 没有专辑与核对的节不出现。
    expect(screen.queryByRole("heading", { level: 3, name: "所在的专辑" })).toBeNull();
    expect(screen.queryByRole("heading", { level: 3, name: /问题核对/ })).toBeNull();

    // 推荐的版本是带名称的单选组，每项写明理由；还没有选中任何一个。
    const suggestions = screen.getByRole("group", { name: "推荐的版本" });
    const options = within(suggestions).getAllByRole("radio");
    expect(options.map((option) => (option.closest("label") as HTMLElement).textContent)).toEqual([
      expect.stringContaining("同样的文件（12 个）、同一目录"),
      expect.stringContaining("同一目录"),
    ]);
    expect(options.every((option) => !(option as HTMLInputElement).checked)).toBe(true);
    expect(api.requestsTo("GET", `/api/v1/release-claims/${claimReleaseId(1)}`)).toHaveLength(1);
  });

  it("留有专辑、默认版本与核对的旧版本：分节说明，同专辑的可见版本是链接、不可见的写“已不可见”，核对人缺失时说明", async () => {
    setup();
    renderApp({ path: `/claims?release=${claimReleaseId(2)}` });
    expect(await heading("Gamma Sonatas", 2)).toBeTruthy();
    const album = screen.getByRole("heading", { level: 3, name: "所在的专辑" }).closest("section") as HTMLElement;
    expect(within(album).getByText(/它和另外 2 个版本同在《Gamma Sonatas Collection》这张专辑里。/)).toBeTruthy();
    expect(within(album).getByRole("link", { name: "Gamma Sonatas Collection (live)" }).getAttribute("href")).toBe(
      "/albums/00000000-0000-4000-8000-0000000000f1?release=00000000-0000-4000-8000-0000000000d1",
    );
    expect(within(album).getByText(/Gamma Sonatas Collection \(hidden\)/).textContent).toContain("（已不可见）");
    expect(within(album).queryByRole("link", { name: /hidden/ })).toBeNull();
    expect(within(album).getByText("它是《Gamma Sonatas Collection》这张专辑的默认版本。")).toBeTruthy();

    const reviews = screen.getByRole("table", { name: "旧版本上的问题核对" });
    expect(within(reviews).getAllByRole("row").map((row) => row.textContent)).toEqual([
      "问题核对人核对时间",
      `来源类型curator${formatDateTime(reviewedAt)}`,
      `归组冲突（用户已不存在）${formatDateTime(reviewedAt)}`,
    ]);
    expect(screen.getByRole("heading", { level: 3, name: "字段修正（1 项）" })).toBeTruthy();
    expect(screen.getByText("没有找到文件或目录相同的版本，可以在下面搜索。")).toBeTruthy();
    // 年份明确设为空，扫描值是 2003。
    expect(within(screen.getByRole("table", { name: "旧版本上的字段修正" })).getAllByRole("row")[1]?.textContent).toBe("年份设为空2003");
  });

  it("没有修正的旧版本没有“字段修正”节；只是自己那个组的默认版本（不在多版本专辑里）时只写默认版本", async () => {
    const { governance } = setup();
    seedClaim(
      governance,
      claimDetail(4, {
        title: "Epsilon Etudes",
        corrections: [],
        defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f2", title: "Epsilon Etudes" }],
        reviews: [{ issue: "media_type", reviewed_at: reviewedAt, reviewer: { id: "00000000-0000-4000-8000-000000000002", username: "curator" } }],
      }),
    );
    renderApp({ path: `/claims?release=${claimReleaseId(4)}` });
    expect(await heading("Epsilon Etudes", 2)).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 3, name: /字段修正/ })).toBeNull();
    const album = screen.getByRole("heading", { level: 3, name: "所在的专辑" }).closest("section") as HTMLElement;
    expect(within(album).queryByText(/同在/)).toBeNull();
    expect(within(album).getByText("它是《Epsilon Etudes》这张专辑的默认版本。")).toBeTruthy();
    expect(screen.getByRole("heading", { level: 3, name: "问题核对（1 项）" })).toBeTruthy();
  });

  it("地址里的 release 刷新后仍然选中；不是 UUID 的值被丢弃", async () => {
    setup();
    const { history } = renderApp({ path: `/claims?release=${claimReleaseId(3).toUpperCase()}&page=1` });
    expect(await heading("Delta Partitas", 2)).toBeTruthy();
    expect(history.location.search).toBe(`?release=${claimReleaseId(3)}`);
    await act(async () => {
      await history.push("/claims?release=not-a-uuid");
    });
    expect(await screen.findByText("从列表里选一个版本。")).toBeTruthy();
  });

  it("旧版本已不需要认领（404）时就地说明，不给无用的重试", async () => {
    const { governance } = setup();
    governance.claims.delete(claimReleaseId(1));
    renderApp({ path: `/claims?release=${claimReleaseId(1)}` });
    expect(await screen.findByText("这个版本已经不需要认领了。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });
});

describe("待认领页：窄容器的顺序导航", () => {
  it("没有选中的版本只显示列表；选中后只显示详情与“返回待认领列表”，返回之后又是列表", async () => {
    narrowContainer(600);
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/claims" });
    await screen.findByRole("link", { name: /Alpha Suites \(corrected\)/ });
    expect(screen.queryByText("从列表里选一个版本。")).toBeNull();
    await user.click(screen.getByRole("link", { name: /Alpha Suites \(corrected\)/ }));
    expect(await heading("Alpha Suites (corrected)", 2)).toBeTruthy();
    expect(screen.queryByRole("heading", { level: 2, name: "留有修改的旧版本" })).toBeNull();
    await user.click(screen.getByRole("link", { name: "返回待认领列表" }));
    await waitFor(() => expect(history.location.search).toBe(""));
    expect(await screen.findByRole("heading", { level: 2, name: "留有修改的旧版本" })).toBeTruthy();
  });
});

describe("操作记录里的认领", () => {
  it("认领记录写来源、迁移与保留的内容；撤销认领写“把迁走的修改还给……”；不可撤销的原因不是“专辑已不可见”", async () => {
    const { api } = setup();
    const source = { id: claimReleaseId(1), title: "Alpha Suites (corrected)", visible: false };
    const moved = { corrections: ["title" as const, "genre" as const], album: true, defaults: 1, reviews: ["source_type" as const] };
    const skipped = { corrections: ["year" as const], album: false, defaults: 0, reviews: [] };
    const target = { id: alpha().representative.id, title: "Alpha Suites New Rip", group_id: alpha().id, visible: true };
    const actor = { id: "00000000-0000-4000-8000-000000000002", username: "curator" };
    api.on("GET", "/api/v1/operations", () =>
      jsonResponse(200, {
        items: [
          {
            id: "00000000-0000-4000-8000-000000000f02",
            type: "revert",
            created_at: "2026-01-21T10:00:00Z",
            actor,
            release: target,
            album: null,
            changes: { kind: "release_claim", source },
            reverts: "00000000-0000-4000-8000-000000000f01",
            reverted_by: null,
            revertible: false,
          },
          {
            id: "00000000-0000-4000-8000-000000000f01",
            type: "release_claim",
            created_at: "2026-01-21T09:00:00Z",
            actor,
            release: { ...target, visible: false },
            album: null,
            changes: { kind: "release_claim", source, previous_group_id: beta().id, moved, skipped },
            reverts: null,
            reverted_by: "00000000-0000-4000-8000-000000000f02",
            revertible: false,
          },
        ],
        pagination: { page: 1, page_size: 50, total: 2 },
      }),
    );
    renderApp({ path: "/operations" });
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);
    expect(rows[0]?.textContent).toContain("撤销认领");
    expect(rows[0]?.textContent).toContain("把迁走的修改还给《Alpha Suites (corrected)》（已不可见）");
    expect(rows[1]?.textContent).toContain("认领");
    expect(rows[1]?.textContent).toContain("从《Alpha Suites (corrected)》（已不可见）认领：修正 标题、流派；专辑；默认版本；核对 来源类型");
    expect(rows[1]?.textContent).toContain("保留这个版本已有的：修正 年份");
    expect(rows[1]?.textContent).toContain("已撤销");
  });

  it("目标不可见、之后又有修改的认领写“之后又有修改”，而不是“专辑已不可见”", async () => {
    const { api } = setup();
    api.on("GET", "/api/v1/operations", () =>
      jsonResponse(200, {
        items: [
          {
            id: "00000000-0000-4000-8000-000000000f01",
            type: "release_claim",
            created_at: "2026-01-21T09:00:00Z",
            actor: { id: "00000000-0000-4000-8000-000000000002", username: "curator" },
            release: { id: alpha().representative.id, title: "Alpha Suites New Rip", group_id: alpha().id, visible: false },
            album: null,
            changes: { kind: "release_claim", source: { id: claimReleaseId(1), title: "Old", visible: false }, previous_group_id: alpha().id, moved: { corrections: [], album: false, defaults: 0, reviews: [] }, skipped: { corrections: [], album: false, defaults: 0, reviews: [] } },
            reverts: null,
            reverted_by: null,
            revertible: false,
          },
        ],
        pagination: { page: 1, page_size: 50, total: 1 },
      }),
    );
    renderApp({ path: "/operations" });
    const row = (await screen.findAllByRole("row"))[1];
    expect(row?.textContent).toContain("之后又有修改");
    expect(row?.textContent).not.toContain("专辑已不可见");
    expect(row?.textContent).toContain("从《Old》（已不可见）认领：没有迁移任何修改");
  });
});
