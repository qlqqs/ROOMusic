import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { claimDetail, claimReleaseId, suggestionOf } from "../test/claim-data";
import { alpha, beta, delta, narrowContainer, setup } from "../test/claims-setup";
import { respondToClaimRelease, respondToCorrect, respondToIssueReview, respondToReleaseIssues, seedClaim, seedIssues } from "../test/governance-data";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// 认领的交互（第 6 步）：选择接替的版本、预览、认领与撤销。读取与列表在 claims.test.tsx。
// 旧版本 1 的推荐是 alpha（单版本专辑，同样的文件 + 同一目录）与 beta（同一目录）。

const oldId = claimReleaseId(1);
const alphaId = () => alpha().representative.id;
const user = () => userEvent.setup();

const preview = () => screen.findByRole("region", { name: /认领到《.+》会怎样/ });
const claimButton = (title: string) => screen.findByRole("button", { name: `认领到《${title}》` });
const previewItems = async () => within(await screen.findByRole("list", { name: "认领会怎样处理旧版本上的修改" })).getAllByRole("listitem");

describe("认领：选择接替的版本", () => {
  it("推荐的版本是带名称的单选组；点选之后地址里有 target（替换历史项），预览出现", async () => {
    setup();
    const { history } = renderApp({ path: `/claims?release=${oldId}` });
    const group = await screen.findByRole("group", { name: "推荐的版本" });
    expect(screen.queryByRole("region", { name: /认领到/ })).toBeNull();
    const entriesBefore = history.length;
    await user().click(within(group).getByRole("radio", { name: /Alpha Suites New Rip/ }));
    await waitFor(() => expect(history.location.search).toBe(`?release=${oldId}&target=${alphaId()}`));
    expect(history.length).toBe(entriesBefore); // 选择是同一个页面里的状态变化，不为每次点选留一个后退项
    expect((within(group).getByRole("radio", { name: /Alpha Suites New Rip/ }) as HTMLInputElement).checked).toBe(true);
    expect(await preview()).toBeTruthy();
  });

  it("换一个旧版本会清掉选中的新版本；刷新带 target 的地址仍然预选并显示预览", async () => {
    setup();
    const { history } = renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    expect(await preview()).toBeTruthy();
    expect((within(screen.getByRole("group", { name: "推荐的版本" })).getByRole("radio", { name: /Alpha Suites New Rip/ }) as HTMLInputElement).checked).toBe(true);
    await user().click(screen.getByRole("link", { name: /Gamma Sonatas/ }));
    await waitFor(() => expect(history.location.search).toBe(`?release=${claimReleaseId(2)}`));
    expect(screen.queryByRole("region", { name: /认领到/ })).toBeNull();
  });

  it("不在推荐里的目标（比如从专辑详情的提示点进来）也预选并预览", async () => {
    setup();
    renderApp({ path: `/claims?release=${oldId}&target=${delta().representative.id}` });
    const region = await preview();
    expect(within(region).getByRole("heading", { level: 3, name: `认领到《${delta().representative.title}》会怎样` })).toBeTruthy();
  });

  it("搜索其他版本：单版本的专辑选中就是选中了它的版本", async () => {
    const { api } = setup();
    renderApp({ path: `/claims?release=${claimReleaseId(3)}` });
    const search = await screen.findByRole("search", { name: "搜索其他版本" });
    await user().type(within(search).getByRole("textbox", { name: /搜索其他版本/ }), "Beta{Enter}");
    const albums = await screen.findByRole("group", { name: "选择专辑" });
    await user().click(within(albums).getByRole("radio", { name: /Beta Quartets/ }));
    expect(await preview()).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/release-groups").at(-1)?.path).toContain("q=Beta");
  });

  it("搜索到有多个版本的专辑：再选其中一个版本", async () => {
    setup();
    renderApp({ path: `/claims?release=${claimReleaseId(3)}` });
    const search = await screen.findByRole("search", { name: "搜索其他版本" });
    await user().type(within(search).getByRole("textbox", { name: /搜索其他版本/ }), "Delta{Enter}");
    await user().click(within(await screen.findByRole("group", { name: "选择专辑" })).getByRole("radio", { name: /Delta Concertos/ }));
    const versions = await screen.findByRole("group", { name: "选择《Delta Concertos》的版本" });
    expect(screen.queryByRole("region", { name: /认领到/ })).toBeNull();
    const radios = within(versions).getAllByRole("radio");
    expect(radios).toHaveLength(2);
    await user().click(radios[1] as HTMLElement);
    expect(await preview()).toBeTruthy();
  });

  it("换一个搜索词会重新开始：上一次选中的专辑和它的版本面板不留着", async () => {
    setup();
    renderApp({ path: `/claims?release=${claimReleaseId(3)}` });
    const search = await screen.findByRole("search", { name: "搜索其他版本" });
    const box = within(search).getByRole("textbox", { name: /搜索其他版本/ });
    await user().type(box, "Delta{Enter}");
    await user().click(within(await screen.findByRole("group", { name: "选择专辑" })).getByRole("radio", { name: /Delta Concertos/ }));
    expect(await screen.findByRole("group", { name: "选择《Delta Concertos》的版本" })).toBeTruthy();
    await user().clear(box);
    await user().type(box, "Delta Concertos{Enter}");
    // 搜索词变了，结果重新来过：没有选中的专辑，也就没有版本面板。
    await waitFor(() => expect(screen.queryByRole("group", { name: "选择《Delta Concertos》的版本" })).toBeNull());
    const albums = await screen.findByRole("group", { name: "选择专辑" });
    expect((within(albums).getByRole("radio", { name: /Delta Concertos/ }) as HTMLInputElement).checked).toBe(false);
  });

  it("搜索词为空或太长时说明原因，不发请求", async () => {
    const { api } = setup();
    renderApp({ path: `/claims?release=${claimReleaseId(3)}` });
    const search = await screen.findByRole("search", { name: "搜索其他版本" });
    const box = within(search).getByRole("textbox", { name: /搜索其他版本/ });
    await user().type(box, "   {Enter}");
    expect(await screen.findByText("请输入搜索词。")).toBeTruthy();
    await user().clear(box);
    await user().click(box);
    await user().paste("长".repeat(120));
    await user().keyboard("{Enter}");
    expect(await screen.findByText("搜索词太长了，请短一些。")).toBeTruthy();
    expect(api.requestsTo("GET", "/api/v1/release-groups").filter((request) => request.path.includes("q="))).toHaveLength(0);
  });
});

describe("认领：选中的版本不可用", () => {
  it("选中的新版本现在不可见或不存在（预览 404）：就地说明，请重新选择，没有认领按钮和重试", async () => {
    setup();
    renderApp({ path: `/claims?release=${oldId}&target=00000000-0000-4000-8000-00000000dead` });
    expect(await screen.findByText("选中的版本已经找不到了，请重新选一个。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^认领到/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("旧版本在选好新版本之后被别处认领走（预览 409 not_claimable）：说明不需要认领", async () => {
    const { api } = setup();
    api.on("GET", /\/claim-preview$/, () => jsonResponse(409, { error: { code: "not_claimable", message: "x" }, request_id: "r" }));
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    expect(await screen.findByText("这个旧版本已经不需要认领了。")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^认领到/ })).toBeNull();
  });
});

describe("认领：预览逐项说明", () => {
  it("新版本是单版本专辑：修正、专辑、默认版本与核对都迁移；每项用文字写明去向", async () => {
    const { governance } = setup();
    seedClaim(
      governance,
      claimDetail(5, {
        title: "Full Old",
        directory: "Full",
        album: { id: "00000000-0000-4000-8000-0000000000f1", title: "Full Collection", siblings: [{ id: "00000000-0000-4000-8000-0000000000d1", title: "Full Collection (live)", visible: true }] },
        defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f1", title: "Full Collection" }],
        reviews: [{ issue: "media_type", reviewed_at: "2026-01-05T09:30:00Z", reviewer: { id: "00000000-0000-4000-8000-000000000002", username: "curator" } }],
        suggestions: [suggestionOf(alpha(), ["same_files"], 3)],
      }),
      { media_type: "valid" },
    );
    renderApp({ path: `/claims?release=${claimReleaseId(5)}&target=${alphaId()}` });
    const items = await previewItems();
    expect(items.map((item) => item.textContent)).toEqual([
      "迁移修正 · 标题旧版本的“标题”改成“Old Edition 5 (corrected)”，迁到这个版本。",
      "迁移修正 · 流派旧版本的“流派”改成“Baroque”，迁到这个版本。",
      "迁移所在的专辑这个版本会归入旧版本原来的专辑《Full Collection》，旧版本离开这张专辑。",
      "迁移默认版本这个版本接任《Full Collection》的默认版本。",
      "迁移核对 · 介质类型旧版本上的核对迁到这个版本。这个版本有同样的问题，核对会直接生效。",
    ]);
    expect(items.map((item) => item.getAttribute("data-action"))).toEqual(["move", "move", "move", "move", "move"]);
  });

  it("新版本已有的以新版本为准：已有的修正、已核对的问题、已在多版本专辑里都保留，并说明原因；默认版本改回自动选择", async () => {
    const { governance } = setup();
    // 目标是有两个版本的专辑 Delta 的代表版本；它自己已经有标题修正，也核对过来源类型。
    const target = delta().representative.id;
    seedIssues(governance, target, { fields: [{ field: "source_type", value: "CD", reason: "inconsistent_candidates", candidates: ["CD", "SACD"] }] });
    expect(respondToCorrect(governance, target, "key-correct", { expected_revision: 1, changes: { title: { mode: "set", value: "Delta (my title)" } } }).status).toBe(200);
    const basis = (respondToReleaseIssues(governance, target).body as { fields: { basis: string }[] }).fields[0]?.basis as string;
    expect(respondToIssueReview(governance, target, "key-review", { expected_revision: 2, issue: "source_type", action: "review", basis }).status).toBe(200);
    seedClaim(
      governance,
      claimDetail(6, {
        title: "Kept Old",
        directory: "Kept",
        album: { id: "00000000-0000-4000-8000-0000000000f1", title: "Kept Collection", siblings: [{ id: "00000000-0000-4000-8000-0000000000d1", title: "Kept Collection (live)", visible: true }] },
        defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f1", title: "Kept Collection" }],
        reviews: [
          { issue: "source_type", reviewed_at: "2026-01-05T09:30:00Z", reviewer: { id: "00000000-0000-4000-8000-000000000002", username: "curator" } },
          { issue: "missing_tracks", reviewed_at: "2026-01-05T09:30:00Z", reviewer: { id: "00000000-0000-4000-8000-000000000002", username: "curator" } },
        ],
        corrections: [{ field: "title", correction: { mode: "set", value: "Old Title" }, scan_value: "Scan" }],
        suggestions: [suggestionOf(delta(), ["same_directory"], 0)],
      }),
    );
    renderApp({ path: `/claims?release=${claimReleaseId(6)}&target=${target}` });
    const items = await previewItems();
    expect(items.map((item) => item.getAttribute("data-action"))).toEqual(["skip", "skip", "reset", "skip", "skip"]);
    expect(items.map((item) => item.textContent)).toEqual([
      "保留修正 · 标题这个版本已经有自己的修正（改成“Delta (my title)”），以它为准。旧版本的“标题”改成“Old Title”。",
      "保留所在的专辑这个版本已经在另一张专辑里，不会归入《Kept Collection》，需要的话可以在专辑页手动归入。旧版本会离开《Kept Collection》。",
      "改回自动选择默认版本这个版本不会进入《Kept Collection》，所以它的默认版本改回自动选择。",
      "保留核对 · 来源类型这个版本已经核对过这一类问题，以它为准。",
      "保留核对 · 缺失曲目缺失曲目的核对只属于旧版本，不迁移。",
    ]);
  });

  it("迁移的核对在新版本上的效果有三种提示：直接生效、同类问题情况不同再次提醒、当前无此类问题时以后出现同样问题才生效", async () => {
    const { governance } = setup();
    const reviewer = { id: "00000000-0000-4000-8000-000000000002", username: "curator" };
    seedClaim(
      governance,
      claimDetail(7, {
        title: "Effects Old",
        corrections: [],
        reviews: ["source_type", "media_type", "grouping"].map((issue) => ({ issue: issue as "source_type", reviewed_at: "2026-01-05T09:30:00Z", reviewer })),
        suggestions: [suggestionOf(alpha(), ["same_files"], 3)],
      }),
      { source_type: "valid", media_type: "stale" },
    );
    renderApp({ path: `/claims?release=${claimReleaseId(7)}&target=${alphaId()}` });
    const items = await previewItems();
    const effects = items.map((item) => item.textContent ?? "");
    expect(effects[0]).toContain("核对 · 来源类型");
    expect(effects[0]).toContain("有同样的问题，核对会直接生效");
    expect(effects[1]).toContain("核对 · 介质类型");
    expect(effects[1]).toContain("核对不会生效，问题会再次提醒");
    expect(effects[2]).toContain("核对 · 归组冲突");
    expect(effects[2]).toContain("这个版本现在没有这类问题，以后出现同样的问题时才生效");
  });

  it("旧版本上没有可以迁移的修改时说明这一点，仍然可以认领", async () => {
    const { governance } = setup();
    seedClaim(governance, claimDetail(8, { title: "Empty Old", corrections: [], suggestions: [suggestionOf(alpha(), ["same_directory"], 0)] }));
    renderApp({ path: `/claims?release=${claimReleaseId(8)}&target=${alphaId()}` });
    expect(await screen.findByText("旧版本上没有可以迁移的修改。")).toBeTruthy();
    expect(await claimButton("Alpha Suites New Rip")).toBeTruthy();
  });
});

const claimsPost = (api: ReturnType<typeof setup>["api"], targetId: string) => api.requestsTo("POST", new RegExp(`/api/v1/releases/${targetId}/claim$`));

describe("认领：提交、结果与撤销", () => {
  it("点“认领到《…》”提交两个版本号与预览令牌；成功之后提示迁了什么，旧版本从列表消失，有“撤销”和“打开这张专辑”", async () => {
    const { api } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    await user().click(await claimButton("Alpha Suites New Rip"));
    const notice = await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到《Alpha Suites New Rip》/);
    expect(notice.textContent).toBe("已把《Alpha Suites (corrected)》上的修改认领到《Alpha Suites New Rip》：修正 标题、流派。");
    const post = claimsPost(api, alphaId());
    expect(post).toHaveLength(1);
    const body = post[0]?.body as { expected_revision: number; source_release_id: string; source_revision: number; preview: string };
    expect(body).toMatchObject({ source_release_id: oldId, source_revision: 2 });
    expect(body.expected_revision).toBeGreaterThanOrEqual(1);
    expect(body.preview).toMatch(/^[0-9a-f]{64}$/);
    expect(post[0]?.headers.get("Idempotency-Key")).toMatch(/^[0-9a-f-]{36}$/);

    const status = notice.closest('[role="status"]') as HTMLElement;
    expect(within(status).getByRole("button", { name: "撤销" })).toBeTruthy();
    expect(within(status).getByRole("link", { name: "打开这张专辑" }).getAttribute("href")).toBe(`/albums/${alpha().id}?release=${alphaId()}`);
    // 旧版本不再待认领：从列表消失，详情说明不需要认领。
    await waitFor(() => expect(within(screen.getByRole("heading", { level: 2, name: "留有修改的旧版本" }).closest("section") as HTMLElement).queryByRole("link", { name: /Alpha Suites \(corrected\)/ })).toBeNull());
    expect(screen.getByText("这个版本已经不需要认领了。")).toBeTruthy();
  });

  it("认领改变了新版本所在的专辑：“打开这张专辑”指向它现在所在的专辑，不是认领之前的", async () => {
    const { governance } = setup();
    const oldAlbum = "00000000-0000-4000-8000-0000000000f7";
    seedClaim(
      governance,
      claimDetail(10, {
        title: "Album Old",
        corrections: [],
        album: { id: oldAlbum, title: "Old Album", siblings: [{ id: "00000000-0000-4000-8000-0000000000d1", title: "Old Album (live)", visible: true }] },
        suggestions: [suggestionOf(alpha(), ["same_files"], 4)],
      }),
    );
    renderApp({ path: `/claims?release=${claimReleaseId(10)}&target=${alphaId()}` });
    await user().click(await claimButton("Alpha Suites New Rip"));
    const link = await screen.findByRole("link", { name: "打开这张专辑" });
    expect(link.getAttribute("href")).toBe(`/albums/${oldAlbum}?release=${alphaId()}`);
    expect(link.getAttribute("href")).not.toContain(alpha().id);
  });

  it("认领之后焦点落在结果提示上，不掉到 body（提交按钮随预览一起被换掉）", async () => {
    setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    const button = await claimButton("Alpha Suites New Rip");
    await user().click(button);
    const notice = (await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到/)).closest('[tabindex="-1"]') as HTMLElement;
    await waitFor(() => expect(document.activeElement).toBe(notice));
    expect(document.body.contains(button)).toBe(false);
  });

  it("什么都没迁移（新版本已有的都保留了）时说明新版本已经接替了旧版本", async () => {
    const { governance } = setup();
    // 旧版本有标题和流派两个修正，而目标已经把这两个字段都改成了自己的。
    const target = beta().representative.id;
    expect(respondToCorrect(governance, target, "key-correct", { expected_revision: 1, changes: { title: { mode: "set", value: "Beta (mine)" }, genre: { mode: "set", value: "Mine" } } }).status).toBe(200);
    renderApp({ path: `/claims?release=${claimReleaseId(1)}&target=${target}` });
    await user().click(await screen.findByRole("button", { name: /^认领到《/ }));
    const text = (await screen.findByText(/已经接替了/)).textContent;
    expect(text).toContain("没有迁移任何修改，它已有的都保留了。");
  });

  it("默认版本改回自动选择时，提示里说明", async () => {
    const { governance } = setup();
    seedClaim(
      governance,
      claimDetail(9, {
        title: "Default Old",
        corrections: [],
        defaultOf: [{ id: "00000000-0000-4000-8000-0000000000f1", title: "Elsewhere" }],
        suggestions: [suggestionOf(alpha(), ["same_files"], 2)],
      }),
    );
    renderApp({ path: `/claims?release=${claimReleaseId(9)}&target=${alphaId()}` });
    await user().click(await claimButton("Alpha Suites New Rip"));
    expect((await screen.findByText(/有 1 张专辑的默认版本改回了自动选择/)).textContent).toContain("有 1 张专辑的默认版本改回了自动选择。");
  });

  it("撤销：提示“已撤销这次认领”，旧版本回到待认领列表，认领的两个版本号与记录对得上", async () => {
    const { api } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    await user().click(await claimButton("Alpha Suites New Rip"));
    await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到/);
    await user().click(screen.getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销这次认领，修改还给了旧版本。")).toBeTruthy();
    const list = screen.getByRole("heading", { level: 2, name: "留有修改的旧版本" }).closest("section") as HTMLElement;
    expect(await within(list).findByRole("link", { name: /Alpha Suites \(corrected\)/ })).toBeTruthy();
    expect(api.requestsTo("POST", /\/operations\/[^/]+\/revert$/)).toHaveLength(1);
    // 撤销之后又能认领一次。
    expect(await claimButton("Alpha Suites New Rip")).toBeTruthy();
  });
});

describe("认领：失败与不确定的结果", () => {
  it("预览之后状态变了（revision_conflict）：警告提示，预览重新读取，不丢失选中的版本", async () => {
    const { api, governance } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    const button = await claimButton("Alpha Suites New Rip");
    // 别人在预览之后修改了新版本：版本号过期。
    expect(respondToCorrect(governance, alphaId(), "key-someone-else", { expected_revision: 1, changes: { label: { mode: "set", value: "Someone" } } }).status).toBe(200);
    const previews = api.requestsTo("GET", /claim-preview$/).length;
    await user().click(button);
    expect(await screen.findByText("两个版本刚被改动过")).toBeTruthy();
    expect(screen.getByText("预览之后又有扫描或修改，已重新读取。请看过新的预览再认领。")).toBeTruthy();
    await waitFor(() => expect(api.requestsTo("GET", /claim-preview$/).length).toBeGreaterThan(previews));
    // 重新读到的预览带新的版本号：这次可以认领。
    await user().click(await claimButton("Alpha Suites New Rip"));
    expect(await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到/)).toBeTruthy();
  });

  it("预览令牌过期（claim_changed）同样提示重新看预览", async () => {
    const { api } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    const button = await claimButton("Alpha Suites New Rip");
    api.on("POST", /\/claim$/, () => jsonResponse(409, { error: { code: "claim_changed", message: "x" }, request_id: "r" }));
    await user().click(button);
    expect(await screen.findByText("两个版本刚被改动过")).toBeTruthy();
  });

  it("旧版本已经被认领走（not_claimable）：说明不需要认领，没有“重试”", async () => {
    const { api } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    const button = await claimButton("Alpha Suites New Rip");
    api.on("POST", /\/claim$/, () => jsonResponse(409, { error: { code: "not_claimable", message: "x" }, request_id: "r" }));
    await user().click(button);
    expect(await screen.findByText("这个旧版本现在不需要认领")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("结果不确定（网络失败）：提示“结果未确认”，“重试认领”沿用同一个幂等键，不会认领两次", async () => {
    const { api, governance } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    const button = await claimButton("Alpha Suites New Rip");
    let failures = 1;
    api.on("POST", /\/claim$/, (request) => {
      if (failures-- > 0) throw new TypeError("network down");
      const result = respondToClaimRelease(governance, alphaId(), request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user().click(button);
    expect(await screen.findByText("结果未确认")).toBeTruthy();
    await user().click(await screen.findByRole("button", { name: "重试认领" }));
    expect(await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到/)).toBeTruthy();
    const keys = claimsPost(api, alphaId()).map((request) => request.headers.get("Idempotency-Key"));
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
  });

  it("提交期间选择被锁住：不能换新版本", async () => {
    const { api, governance } = setup();
    renderApp({ path: `/claims?release=${oldId}&target=${alphaId()}` });
    const button = await claimButton("Alpha Suites New Rip");
    let release: (() => void) | undefined;
    const gate = new Promise<void>((resolve) => (release = resolve));
    api.on("POST", /\/claim$/, async (request) => {
      await gate;
      const result = respondToClaimRelease(governance, alphaId(), request.headers.get("Idempotency-Key"), request.body);
      return jsonResponse(result.status, result.body);
    });
    await user().click(button);
    const group = screen.getByRole("group", { name: "推荐的版本" });
    // 通过祖先 fieldset 禁用的控件：input.disabled 属性仍是 false，但 :disabled 匹配（浏览器里也不能操作）。
    await waitFor(() => expect(within(group).getByRole("radio", { name: /Beta Quartets/ }).matches(":disabled")).toBe(true));
    expect(screen.getByRole("button", { name: "正在认领…" })).toBeTruthy();
    await act(async () => release?.());
    expect(await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到/)).toBeTruthy();
  });
});

describe("认领：窄容器", () => {
  it("窄容器里同样能选择、预览、认领；结果提示在详情上方", async () => {
    narrowContainer(600);
    setup();
    renderApp({ path: `/claims?release=${oldId}` });
    await user().click(await screen.findByRole("radio", { name: /Alpha Suites New Rip/ }));
    await user().click(await claimButton("Alpha Suites New Rip"));
    expect(await screen.findByText(/已把《Alpha Suites \(corrected\)》上的修改认领到/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "返回待认领列表" })).toBeTruthy();
  });
});

describe("专辑详情里的认领提示", () => {
  const albumPath = () => `/albums/${alpha().id}`;
  const hintsList = () => screen.findByRole("list", { name: "可能接替了已不可见的版本" });

  it("管理员打开被推荐为接替者的版本：提示“可能接替了已不可见的《…》”，链接带着旧版本和当前版本", async () => {
    setup();
    renderApp({ path: albumPath() });
    const hints = await hintsList();
    expect(within(hints).getByText("可能接替了已不可见的《Alpha Suites (corrected)》")).toBeTruthy();
    const link = within(hints).getByRole("link", { name: "查看并认领《Alpha Suites (corrected)》，它可能被这个版本接替了" });
    expect(link.getAttribute("href")).toBe(`/claims?release=${oldId}&target=${alphaId()}`);
  });

  it("点进去就是认领：旧版本选中、这个版本已预选，预览直接出现", async () => {
    setup();
    const { history } = renderApp({ path: albumPath() });
    await user().click(within(await hintsList()).getByRole("link", { name: /查看并认领/ }));
    await waitFor(() => expect(history.location.pathname).toBe("/claims"));
    expect(history.location.search).toBe(`?release=${oldId}&target=${alphaId()}`);
    expect(await screen.findByRole("heading", { level: 2, name: "Alpha Suites (corrected)" })).toBeTruthy();
    expect((screen.getByRole("radio", { name: /Alpha Suites New Rip/ }) as HTMLInputElement).checked).toBe(true);
    expect(await claimButton("Alpha Suites New Rip")).toBeTruthy();
  });

  it("没有旧版本推荐它时不显示提示", async () => {
    setup();
    renderApp({ path: `/albums/${delta().id}` });
    await screen.findByRole("button", { name: "编辑发行信息" });
    expect(screen.queryByRole("list", { name: "可能接替了已不可见的版本" })).toBeNull();
  });

  it("多个旧版本都推荐它：每个一行，最多 3 个", async () => {
    const { governance } = setup();
    for (let index = 11; index <= 14; index += 1) seedClaim(governance, claimDetail(index, { title: `Another Old ${index}`, suggestions: [suggestionOf(alpha(), ["same_directory"], 0)] }));
    renderApp({ path: albumPath() });
    const hints = await hintsList();
    // 旧版本 1 最新；其余按序号递减的时间排：1, 11, 12 是最近看到的三个。
    expect(within(hints).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
      "可能接替了已不可见的《Alpha Suites (corrected)》查看并认领",
      "可能接替了已不可见的《Another Old 11》查看并认领",
      "可能接替了已不可见的《Another Old 12》查看并认领",
    ]);
  });

  it("普通成员看不到提示，也不请求这个接口", async () => {
    const { api } = setup({ role: "user" });
    renderApp({ path: albumPath() });
    await screen.findByText("Track 01");
    expect(screen.queryByRole("list", { name: "可能接替了已不可见的版本" })).toBeNull();
    expect(api.requestsTo("GET", /claim-hints$/)).toHaveLength(0);
  });

  it("读取提示失败时不打扰页面：专辑详情与操作照常，只是没有提示", async () => {
    const { api } = setup();
    api.on("GET", /claim-hints$/, () => jsonResponse(500, { error: { code: "internal_error", message: "x" }, request_id: "r" }));
    renderApp({ path: albumPath() });
    expect(await screen.findByRole("button", { name: "编辑发行信息" })).toBeTruthy();
    // 等到请求真的失败了再断言：提示不重试，失败就是终态。
    await waitFor(() => expect(api.requestsTo("GET", /claim-hints$/)).toHaveLength(1));
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    expect(screen.queryByRole("list", { name: "可能接替了已不可见的版本" })).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("button", { name: "编辑发行信息" })).toBeTruthy();
  });

  it("认领之后回到这张专辑：提示消失（旧版本已不再待认领）", async () => {
    setup();
    const { history } = renderApp({ path: albumPath() });
    await user().click(within(await hintsList()).getByRole("link", { name: /查看并认领/ }));
    await user().click(await claimButton("Alpha Suites New Rip"));
    await user().click(await screen.findByRole("link", { name: "打开这张专辑" }));
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${alpha().id}`));
    await screen.findByRole("button", { name: "编辑发行信息" });
    await waitFor(() => expect(screen.queryByRole("list", { name: "可能接替了已不可见的版本" })).toBeNull());
  });
});

describe("认领记录：修改记录里的撤销", () => {
  /** 旧版本在另一张专辑里（Old Album）；认领把单版本的 alpha 归入那张专辑，所以认领之后 alpha 的专辑变了。 */
  function seedAlbumClaim(governance: ReturnType<typeof setup>["governance"]) {
    seedClaim(
      governance,
      claimDetail(12, {
        title: "Album Old",
        corrections: [{ field: "label", correction: { mode: "set", value: "Old Label" }, scan_value: null }],
        album: { id: "00000000-0000-4000-8000-0000000000f7", title: "Old Album", siblings: [{ id: "00000000-0000-4000-8000-0000000000d1", title: "Old Album (live)", visible: true }] },
        suggestions: [suggestionOf(alpha(), ["same_files"], 4)],
      }),
    );
    return "00000000-0000-4000-8000-0000000000f7";
  }

  it("专辑详情的“修改记录”里撤销认领：确认框写明改回什么，撤销之后页面跟着这个版本回到认领之前的专辑", async () => {
    const { governance } = setup();
    const oldAlbum = seedAlbumClaim(governance);
    const { history } = renderApp({ path: `/claims?release=${claimReleaseId(12)}&target=${alphaId()}` });
    await user().click(await claimButton("Alpha Suites New Rip"));
    await user().click(await screen.findByRole("link", { name: "打开这张专辑" }));
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${oldAlbum}`));

    await user().click(await screen.findByText("修改记录"));
    const table = await screen.findByRole("table", { name: "修改记录" });
    const row = within(table).getAllByRole("row")[1] as HTMLElement;
    expect(row.textContent).toContain("认领");
    expect(row.textContent).toContain("从《Album Old》（已不可见）认领：修正 厂牌；专辑");
    await user().click(within(row).getByRole("button", { name: /撤销/ }));
    const dialog = await screen.findByRole("dialog", { name: "撤销这次认领？" });
    expect(within(dialog).getByText("把迁走的修改还给《Album Old》（已不可见）")).toBeTruthy();
    await user().click(within(dialog).getByRole("button", { name: "撤销" }));

    // 页面跟着新版本回到认领之前所在的专辑，结果提示仍在页面上。
    await waitFor(() => expect(history.location.pathname).toBe(`/albums/${alpha().id}`));
    expect(await screen.findByText("已撤销这次认领，修改还给了旧版本。")).toBeTruthy();
  });

  it("操作记录页里撤销认领：留在操作记录页，不跟随专辑的新位置", async () => {
    const { governance } = setup();
    seedAlbumClaim(governance);
    const claimed = renderApp({ path: `/claims?release=${claimReleaseId(12)}&target=${alphaId()}` });
    await user().click(await claimButton("Alpha Suites New Rip"));
    await screen.findByText(/已把《Album Old》上的修改认领到/);
    await act(async () => {
      await claimed.history.push("/operations");
    });
    const row = (await screen.findAllByRole("row"))[1] as HTMLElement;
    expect(row.textContent).toContain("认领");
    await user().click(within(row).getByRole("button", { name: /撤销/ }));
    await user().click(within(await screen.findByRole("dialog", { name: "撤销这次认领？" })).getByRole("button", { name: "撤销" }));
    expect(await screen.findByText("已撤销这次认领，修改还给了旧版本。")).toBeTruthy();
    expect(claimed.history.location.pathname).toBe("/operations");
  });
});
