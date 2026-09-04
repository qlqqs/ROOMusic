/* global AbortController, URLSearchParams, window, crypto, HTMLDivElement, WheelEvent */
import { FormEvent, StrictMode, useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  ApiRequestError,
  decodeAcknowledgement,
  decodeActiveScan,
  decodeCreatedLibraryRoot,
  decodeCreatedUser,
  decodeLibraryRootList,
  decodeRootOperationList,
  decodeUserList,
  decodeReleaseDetail,
  decodeReleaseEvidence,
  decodeReleaseList,
  decodeScanCancel,
  decodeScanStart,
  decodeScanStatus,
  decodeScanDiagnostics,
  decodeSession,
  decodeUpdatedLibraryRoot,
  decodeUpdatedUser,
  decodeSetupStatus,
  LibraryRootDTO,
  ReleaseEvidenceDTO,
  ReleaseSummaryDTO,
  requestApi,
  ScanStatusDTO,
  ScanDiagnosticsDTO,
  SessionDTO,
  RootOperationDTO,
  TrackDTO,
  UserDTO,
} from "./api";
import { clampReleasePage, readReleaseFilters, releaseFilterURL } from "./release_filters";
import {
  CatalogToolbar,
  MediumSection,
  ReleaseCard,
  ReleaseCover,
  ReleaseDetailDrawer,
  clearDemoQueue,
  currentDemoItem,
  emptyDemoQueue,
  flattenReleaseTracks,
  formatSourceMediaLabel,
  nextDemoTrack,
  playDemoItems,
  playDemoTrack,
  previousDemoTrack,
  removeCurrentDemoItem,
  setDemoPlaying,
  toReleaseCardModel,
  untitledTrackLabel,
  type CatalogViewMode,
  type DemoQueueItem,
  type DemoQueueState,
  type ReleaseDetailState,
} from "./features/catalog";
import "./styles.css";

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : "请求失败";
}

function describeScanState(scan: ScanStatusDTO | null): string {
  if (!scan) return "已同步";
  if (scan.status === "running") return scan.cancel_requested_at ? "正在取消扫描" : "正在扫描";
  const labels: Record<Exclude<ScanStatusDTO["status"], "running">, string> = {
    succeeded: "扫描完成",
    failed: "扫描失败",
    canceled: "扫描已取消",
    incomplete: "扫描未完成",
  };
  return labels[scan.status];
}

// 扫描终态文案互不混淆：只有 succeeded 才宣称结果已更新。
function describeScanNotice(scan: ScanStatusDTO | null): string | null {
  if (!scan) return null;
  switch (scan.status) {
    case "running": return scan.cancel_requested_at ? "正在取消扫描，结果可能继续变化" : "正在扫描，结果可能继续变化";
    case "failed": return "扫描失败，当前显示的是此前的结果";
    case "canceled": return "扫描已取消，结果可能不完整";
    case "incomplete": return "扫描未完成，结果可能不完整";
    case "succeeded": return null;
  }
}

type PageRoute = "home" | "library" | "settings";

// 轻量 hash 路由：#/ 首页、#/library 专辑库、#/settings 编辑部。
// 不引入 router 依赖；Go 同源服务无需 SPA fallback。
function readRoute(hash: string): PageRoute {
  if (hash.startsWith("#/library")) return "library";
  if (hash.startsWith("#/settings")) return "settings";
  return "home";
}

function App() {
  const [session, setSession] = useState<SessionDTO | null>(null);
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [libraryPath, setLibraryPath] = useState("");
  const [libraryRoots, setLibraryRoots] = useState<LibraryRootDTO[]>([]);
  const [users, setUsers] = useState<UserDTO[]>([]);
  const [operations, setOperations] = useState<RootOperationDTO[]>([]);
  const [newUsername, setNewUsername] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [releases, setReleases] = useState<ReleaseSummaryDTO[]>([]);
  const [releaseTotal, setReleaseTotal] = useState(0);
  const [releaseDetail, setReleaseDetail] = useState<ReleaseDetailState | null>(null);
  const selectedReleaseGeneration = useRef(0);
  const [releaseEvidence, setReleaseEvidence] = useState<ReleaseEvidenceDTO | null>(null);
  const [releaseEvidenceLoading, setReleaseEvidenceLoading] = useState(false);
  const [releaseEvidenceError, setReleaseEvidenceError] = useState("");
  const [searchInput, setSearchInput] = useState(
    () => readReleaseFilters(window.location.search).query,
  );
  const [searchQuery, setSearchQuery] = useState(
    () => readReleaseFilters(window.location.search).query,
  );
  const [attentionRequired, setAttentionRequired] = useState(
    () => readReleaseFilters(window.location.search).attentionRequired,
  );
  const [releasePage, setReleasePage] = useState(
    () => readReleaseFilters(window.location.search).page,
  );
  const [selectedReleaseId, setSelectedReleaseId] = useState<string | null>(
    () => readReleaseFilters(window.location.search).release,
  );
  const [releasePageSize, setReleasePageSize] = useState(50);
  const [releaseLoading, setReleaseLoading] = useState(false);
  const [releaseError, setReleaseError] = useState("");
  const [releaseRetry, setReleaseRetry] = useState(0);
  const [detailRetry, setDetailRetry] = useState(0);
  const [viewMode, setViewMode] = useState<CatalogViewMode>("grid");
  const [scan, setScan] = useState<ScanStatusDTO | null>(null);
  const [scanDiagnostics, setScanDiagnostics] = useState<ScanDiagnosticsDTO | null>(null);
  const [scanDiagnosticsLoading, setScanDiagnosticsLoading] = useState(false);
  const [scanDiagnosticsError, setScanDiagnosticsError] = useState("");
  const [scanMutationPending, setScanMutationPending] = useState(false);
  const [message, setMessage] = useState("");
  const [queue, setQueue] = useState<DemoQueueState>(emptyDemoQueue);
  const [featuredReleaseId, setFeaturedReleaseId] = useState<string | null>(null);
  const [route, setRoute] = useState<PageRoute>(() => readRoute(window.location.hash));
  const [featureDetail, setFeatureDetail] = useState<ReleaseDetailState | null>(null);
  const [featureRetry, setFeatureRetry] = useState(0);
  const [bannerCollapsedMedia, setBannerCollapsedMedia] = useState<ReadonlySet<string>>(new Set());
  const shelfRowRef = useRef<HTMLDivElement | null>(null);

  const pushFilters = useCallback((filters: { query: string; attentionRequired: boolean; page: number; release: string | null }) => {
    window.history.pushState({}, "", releaseFilterURL(window.location.href, filters));
  }, []);

  const closeReleaseSelection = useCallback(() => {
    selectedReleaseGeneration.current += 1;
    if (readReleaseFilters(window.location.search).release !== null) {
      window.history.pushState({}, "", releaseFilterURL(window.location.href, {
        ...readReleaseFilters(window.location.search),
        release: null,
      }));
    }
    setSelectedReleaseId(null);
    setReleaseDetail(null);
    setReleaseEvidence(null);
    setReleaseEvidenceLoading(false);
    setReleaseEvidenceError("");
  }, []);

  const shelfReleases =
    searchQuery === "" && !attentionRequired && releasePage === 1 ? releases.slice(0, 12) : [];
  const featuredRelease =
    shelfReleases.length > 0
      ? (shelfReleases.find((release) => release.id === featuredReleaseId) ?? shelfReleases[0])
      : null;
  const featureModel = featuredRelease ? toReleaseCardModel(featuredRelease) : null;
  const featuredId = featuredRelease?.id ?? null;

  // 首页头条横幅：直接内联详情与曲目，不经过内页；summary 先渲染，详情异步补齐。
  useEffect(() => {
    if (featuredId === null) {
      setFeatureDetail(null);
      return;
    }
    const requestController = new AbortController();
    setFeatureDetail({ status: "loading" });
    setBannerCollapsedMedia(new Set());
    void requestApi(`/api/v1/releases/${encodeURIComponent(featuredId)}`, decodeReleaseDetail, { signal: requestController.signal })
      .then((detail) => {
        if (requestController.signal.aborted) return;
        setFeatureDetail({ status: "ready", detail });
      })
      .catch((error: unknown) => {
        if (requestController.signal.aborted) return;
        const code = error instanceof ApiRequestError ? error.code : "request_failed";
        if (code === "unauthorized") {
          setMessage("登录已过期，请重新登录");
          setSession(null);
          return;
        }
        setFeatureDetail({ status: "error", code });
      });
    return () => requestController.abort();
  }, [featuredId, featureRetry, session]);

  const bannerQueueItems = featureDetail?.status === "ready" ? flattenReleaseTracks(featureDetail.detail) : [];
  const bannerDetail = featureDetail?.status === "ready" ? featureDetail.detail : null;
  // 空值字段不占位：只有真实记录的档案字段才进入横幅。
  const bannerSourceLabel = bannerDetail ? formatSourceMediaLabel(bannerDetail) : null;

  function previewFeature(releaseID: string) {
    setFeaturedReleaseId(releaseID);
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    document.getElementById("feature-story")?.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "start" });
  }

  function playBannerTrack(track: TrackDTO) {
    const item = bannerQueueItems.find((candidate) => candidate.track.id === track.id);
    if (item) queuePlayTrack(item);
  }

  function toggleBannerMedium(mediumID: string) {
    setBannerCollapsedMedia((collapsed) => {
      const next = new Set(collapsed);
      if (next.has(mediumID)) next.delete(mediumID);
      else next.add(mediumID);
      return next;
    });
  }

  // 唱片架：纵向滚轮转横向滚动（需非 passive 监听才能阻止页面联动）。
  const shelfVisible = route === "home" && shelfReleases.length > 0;
  useEffect(() => {
    const row = shelfRowRef.current;
    if (!row) return;
    const onWheel = (event: WheelEvent) => {
      if (Math.abs(event.deltaY) <= Math.abs(event.deltaX)) return;
      event.preventDefault();
      row.scrollLeft += event.deltaY;
    };
    row.addEventListener("wheel", onWheel, { passive: false });
    return () => row.removeEventListener("wheel", onWheel);
  }, [shelfVisible]);

  useEffect(() => {
    const onHashChange = () => setRoute(readRoute(window.location.hash));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  useEffect(() => {
    void requestApi("/api/v1/setup/status", decodeSetupStatus)
      .then((status) => {
        setSetupRequired(status.setup_required);
        if (!status.setup_required)
          void requestApi("/api/v1/auth/me", decodeSession)
            .then(setSession)
            .catch(() => undefined);
      })
      .catch((error: unknown) => setMessage(describeError(error)));
  }, []);

  useEffect(() => {
    if (session?.role !== "admin") return;
    void requestApi("/api/v1/library-roots", decodeLibraryRootList)
      .then((result) => setLibraryRoots(result.items))
      .catch((error: unknown) => setMessage(describeError(error)));
    void requestApi("/api/v1/users", decodeUserList).then((result) => setUsers(result.items)).catch((error: unknown) => setMessage(describeError(error)));
    void requestApi("/api/v1/library-root-operations", decodeRootOperationList).then((result) => setOperations(result.items)).catch((error: unknown) => setMessage(describeError(error)));
    void requestApi("/api/v1/scans/active", decodeActiveScan)
      .then((result) => setScan(result.scan))
      .catch((error: unknown) => setMessage(describeError(error)));
  }, [session]);

  useEffect(() => {
    const onPopState = () => {
      const filters = readReleaseFilters(window.location.search);
      setSearchInput(filters.query);
      setSearchQuery(filters.query);
      setAttentionRequired(filters.attentionRequired);
      setReleasePage(filters.page);
      setSelectedReleaseId(filters.release);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  // 列表请求：带取消与过期响应保护；扫描状态变化（含 succeeded 终态）触发一次刷新。
  useEffect(() => {
    if (!session) return;
    const params = new URLSearchParams({ page: String(releasePage), page_size: "50" });
    if (searchQuery) params.set("q", searchQuery);
    if (attentionRequired) params.set("attention", "required");
    const requestController = new AbortController();
    setReleaseLoading(true);
    setReleaseError("");
    void requestApi(`/api/v1/releases?${params.toString()}`, decodeReleaseList, { signal: requestController.signal })
      .then((result) => {
        if (requestController.signal.aborted) return;
        const normalizedPage = clampReleasePage(releasePage, result.pagination.total, result.pagination.page_size);
        if (normalizedPage !== releasePage) {
          // 越界页码：用当前 URL 真实状态规范化，保留 release 选中项。
          window.history.replaceState({}, "", releaseFilterURL(window.location.href, {
            ...readReleaseFilters(window.location.search),
            page: normalizedPage,
          }));
          setReleaseTotal(result.pagination.total);
          setReleasePageSize(result.pagination.page_size);
          setReleasePage(normalizedPage);
          return;
        }
        setReleases(result.items);
        setReleaseTotal(result.pagination.total);
        setReleasePageSize(result.pagination.page_size);
      })
      .catch((error: unknown) => {
        if (requestController.signal.aborted) return;
        setReleaseError(describeError(error));
      })
      .finally(() => {
        if (!requestController.signal.aborted) setReleaseLoading(false);
      });
    return () => requestController.abort();
  }, [session, scan?.status, searchQuery, attentionRequired, releasePage, releaseRetry]);

  // 详情请求：URL release 参数驱动；旧响应不得覆盖新选择。
  useEffect(() => {
    if (!session || !selectedReleaseId) {
      selectedReleaseGeneration.current += 1;
      setReleaseDetail(null);
      setReleaseEvidence(null);
      setReleaseEvidenceLoading(false);
      setReleaseEvidenceError("");
      return;
    }
    const requestGeneration = ++selectedReleaseGeneration.current;
    const requestController = new AbortController();
    setReleaseDetail({ status: "loading" });
    setReleaseEvidence(null);
    setReleaseEvidenceLoading(false);
    setReleaseEvidenceError("");
    void requestApi(`/api/v1/releases/${encodeURIComponent(selectedReleaseId)}`, decodeReleaseDetail, { signal: requestController.signal })
      .then((detail) => {
        if (requestController.signal.aborted || selectedReleaseGeneration.current !== requestGeneration) return;
        setReleaseDetail({ status: "ready", detail });
      })
      .catch((error: unknown) => {
        if (requestController.signal.aborted || selectedReleaseGeneration.current !== requestGeneration) return;
        const code = error instanceof ApiRequestError ? error.code : "request_failed";
        if (code === "unauthorized") {
          setMessage("登录已过期，请重新登录");
          setSession(null);
          return;
        }
        if (code === "not_found") {
          closeReleaseSelection();
          setMessage("发行版本不存在或已被移除");
          return;
        }
        setReleaseDetail({ status: "error", code });
      });
    return () => requestController.abort();
  }, [session, selectedReleaseId, detailRetry, closeReleaseSelection]);

  useEffect(() => {
    if (!scan || scan.status !== "running") return;
    const requestController = new AbortController();
    const timer = window.setInterval(() => {
      void requestApi(`/api/v1/scans/${scan.id}`, decodeScanStatus, { signal: requestController.signal })
        .then((result) => {
          if (!requestController.signal.aborted) setScan(result);
        })
        .catch((error: unknown) => {
          if (!requestController.signal.aborted) setMessage(describeError(error));
        });
    }, 1000);
    return () => {
      requestController.abort();
      window.clearInterval(timer);
    };
  }, [scan]);

  useEffect(() => {
    if (session?.role !== "admin" || !scan) {
      setScanDiagnostics(null);
      setScanDiagnosticsError("");
      return;
    }
    const requestController = new AbortController();
    setScanDiagnosticsLoading(true);
    setScanDiagnosticsError("");
    void requestApi(`/api/v1/scans/${scan.id}/diagnostics`, decodeScanDiagnostics, { signal: requestController.signal })
      .then((result) => {
        if (!requestController.signal.aborted) setScanDiagnostics(result);
      })
      .catch((error: unknown) => {
        if (!requestController.signal.aborted) setScanDiagnosticsError(describeError(error));
      })
      .finally(() => {
        if (!requestController.signal.aborted) setScanDiagnosticsLoading(false);
      });
    return () => requestController.abort();
  }, [session?.role, scan?.id, scan?.status, scan?.diagnostics.total]);

  async function submitAuth(event: FormEvent) {
    event.preventDefault();
    try {
      const path = setupRequired ? "/api/v1/setup/admin" : "/api/v1/auth/login";
      const authenticatedSession = await requestApi(path, decodeSession, {
        method: "POST",
        body: JSON.stringify({ username, password }),
      });
      if (setupRequired) {
        setSetupRequired(false);
        setSession(
          await requestApi("/api/v1/auth/login", decodeSession, {
            method: "POST",
            body: JSON.stringify({ username, password }),
          }),
        );
      } else {
        setSession(authenticatedSession);
      }
    } catch (error: unknown) {
      setMessage(describeError(error));
    }
  }

  async function registerRoot(event: FormEvent) {
    event.preventDefault();
    try {
      const createdRoot = await requestApi(
        "/api/v1/library-roots",
        decodeCreatedLibraryRoot,
        { method: "POST", body: JSON.stringify({ path: libraryPath }), headers: { "Idempotency-Key": crypto.randomUUID() } },
      );
      const refreshedRoots = await requestApi(
        "/api/v1/library-roots",
        decodeLibraryRootList,
      );
      setLibraryRoots(refreshedRoots.items);
      setMessage(`目录“${createdRoot.name}”已注册`);
      setLibraryPath("");
    } catch (error: unknown) {
      setMessage(describeError(error));
    }
  }

  async function changeRoot(root: LibraryRootDTO) {
    try {
      const restoring = root.status === "disabled";
      const updated = await requestApi(
        restoring ? `/api/v1/library-roots/${root.id}/restore` : `/api/v1/library-roots/${root.id}`,
        decodeUpdatedLibraryRoot,
        { method: restoring ? "POST" : "PATCH", body: JSON.stringify(restoring ? { expected_revision: root.revision } : { status: "disabled", expected_revision: root.revision }), headers: { "Idempotency-Key": crypto.randomUUID() } },
      );
      setLibraryRoots((items) => items.map((item) => item.id === updated.id ? { ...item, status: updated.status, revision: updated.revision } : item));
      setMessage(restoring ? "目录已恢复" : "目录已停用");
    } catch (error: unknown) { setMessage(describeError(error)); }
  }

  async function startScan() {
    setScanMutationPending(true);
    try {
      setScan(
        await requestApi("/api/v1/scans", decodeScanStart, {
          method: "POST",
          body: "{}",
        }),
      );
      setMessage("扫描已启动");
    } catch (error: unknown) {
      setMessage(describeError(error));
    } finally {
      setScanMutationPending(false);
    }
  }

  async function cancelScan() {
    if (!scan || scan.status !== "running") return;
    setScanMutationPending(true);
    try {
      setScan(await requestApi(`/api/v1/scans/${scan.id}/cancel`, decodeScanCancel, {
        method: "POST",
        body: "{}",
      }));
      setMessage("取消请求已提交");
    } catch (error: unknown) {
      setMessage(describeError(error));
    } finally {
      setScanMutationPending(false);
    }
  }

  async function createUser(event: FormEvent) {
    event.preventDefault();
    try {
      await requestApi("/api/v1/users", decodeCreatedUser, { method: "POST", body: JSON.stringify({ username: newUsername, password: newPassword }) });
      const result = await requestApi("/api/v1/users", decodeUserList);
      setUsers(result.items); setNewUsername(""); setNewPassword(""); setMessage("用户已创建");
    } catch (error: unknown) { setMessage(describeError(error)); }
  }

  async function toggleUser(user: UserDTO) {
    try { const updated = await requestApi(`/api/v1/users/${user.id}`, decodeUpdatedUser, { method: "PATCH", body: JSON.stringify({ disabled: !user.disabled }) }); setUsers((items) => items.map((item) => item.id === user.id ? { ...item, disabled: updated.disabled } : item)); }
    catch (error: unknown) { setMessage(describeError(error)); }
  }

  async function revokeUser(user: UserDTO) {
    try { await requestApi(`/api/v1/users/${user.id}/sessions/revoke`, decodeAcknowledgement, { method: "POST", body: "{}" }); setMessage(`已撤销 ${user.username} 的会话`); }
    catch (error: unknown) { setMessage(describeError(error)); }
  }

  async function showReleaseEvidence() {
    if (session?.role !== "admin" || !releaseDetail || releaseDetail.status !== "ready") return;
    const releaseID = releaseDetail.detail.id;
    const requestGeneration = selectedReleaseGeneration.current;
    setReleaseEvidenceLoading(true);
    setReleaseEvidenceError("");
    try {
      const evidence = await requestApi(`/api/v1/releases/${encodeURIComponent(releaseID)}/evidence`, decodeReleaseEvidence);
      if (selectedReleaseGeneration.current === requestGeneration) setReleaseEvidence(evidence);
    } catch (error: unknown) {
      if (selectedReleaseGeneration.current === requestGeneration) setReleaseEvidenceError(describeError(error));
    } finally {
      if (selectedReleaseGeneration.current === requestGeneration) setReleaseEvidenceLoading(false);
    }
  }

  async function logout() {
    try {
      await requestApi("/api/v1/auth/logout", decodeAcknowledgement, {
        method: "POST",
        body: "{}",
      });
      setSession(null);
    } catch (error: unknown) {
      setMessage(describeError(error));
    }
    // 退出登录清理选中详情、浏览状态与演示队列。
    selectedReleaseGeneration.current += 1;
    setReleaseDetail(null);
    setReleaseEvidence(null);
    setReleaseEvidenceError("");
    setReleaseEvidenceLoading(false);
    setReleases([]);
    setReleaseTotal(0);
    setScan(null);
    setQueue(clearDemoQueue);
    setSearchInput("");
    setSearchQuery("");
    setAttentionRequired(false);
    setReleasePage(1);
    setSelectedReleaseId(null);
    setPassword("");
    window.history.pushState({}, "", releaseFilterURL(window.location.href, { query: "", attentionRequired: false, page: 1, release: null }));
  }

  function submitSearch() {
    const query = searchInput.trim();
    pushFilters({ query, attentionRequired, page: 1, release: null });
    setSearchQuery(query);
    setReleasePage(1);
    setSelectedReleaseId(null);
  }

  function clearSearch() {
    setSearchInput("");
    pushFilters({ query: "", attentionRequired, page: 1, release: null });
    setSearchQuery("");
    setReleasePage(1);
    setSelectedReleaseId(null);
  }

  function toggleAttentionFilter() {
    const nextValue = !attentionRequired;
    pushFilters({ query: searchQuery, attentionRequired: nextValue, page: 1, release: null });
    setAttentionRequired(nextValue);
    setReleasePage(1);
    setSelectedReleaseId(null);
  }

  function changeReleasePage(page: number) {
    const nextPage = Math.max(1, Math.min(page, Math.max(1, Math.ceil(releaseTotal / releasePageSize))));
    if (nextPage === releasePage) return;
    pushFilters({ query: searchQuery, attentionRequired, page: nextPage, release: null });
    setReleasePage(nextPage);
    setSelectedReleaseId(null);
  }

  function openRelease(releaseID: string) {
    pushFilters({ query: searchQuery, attentionRequired, page: releasePage, release: releaseID });
    setReleaseDetail({ status: "loading" });
    setSelectedReleaseId(releaseID);
  }

  function queuePlayItems(items: DemoQueueItem[]) {
    setQueue((current) => playDemoItems(current, items));
  }

  function queuePlayTrack(item: DemoQueueItem) {
    setQueue((current) => playDemoTrack(current, item));
  }

  const releasePageCount = Math.max(1, Math.ceil(releaseTotal / releasePageSize));
  const scanNotice = describeScanNotice(scan);
  const initialListLoading = releaseLoading && releases.length === 0 && releaseError === "";
  const staleRefreshing = releaseLoading && releases.length > 0;
  const currentQueueItem = currentDemoItem(queue);

  if (setupRequired === null)
    return (
      <main className="shell">
        <p role="status">正在加载...</p>
        {message && <p role="alert">{message}</p>}
      </main>
    );
  if (!session)
    return (
      <main className="shell">
        <p className="eyebrow">本地音乐库</p>
        <h1>ROOMusic</h1>
        <h2>{setupRequired ? "首次设置管理员" : "登录音乐库"}</h2>
        <p>{setupRequired ? "数据库尚未初始化，请先创建唯一的初始管理员账号。" : "使用管理员或普通用户账号登录。"}</p>
        <form onSubmit={submitAuth}>
          <label>
            用户名
            <input
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>
          <label>
            密码
            <input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <button type="submit">{setupRequired ? "开始使用" : "登录"}</button>
        </form>
        {message && <p role="alert">{message}</p>}
      </main>
    );
  return (
    <div className={currentQueueItem ? "journal has-player" : "journal"}>
      <header className="masthead">
        <div className="masthead-top">
          <span>VOL.01 · 本地音乐库</span>
          <span className="sync-state" role="status"><i /> {describeScanState(scan)}</span>
          <span className="masthead-user">
            {session.username}（{session.role === "admin" ? "管理员" : "成员"}）
            <button className="masthead-logout" type="button" onClick={() => void logout()}>退出</button>
          </span>
        </div>
        <h1 className="masthead-name">ROOMusic</h1>
        <p className="masthead-motto">私人唱片月刊 · 收录 {releaseTotal} 个发行版本</p>
      </header>

      <nav className="sections" aria-label="栏目">
        <a className={route === "home" ? "section-link current" : "section-link"} aria-current={route === "home" ? "page" : undefined} href="#/"><b>01</b>首页</a>
        <a className={route === "library" ? "section-link current" : "section-link"} aria-current={route === "library" ? "page" : undefined} href="#/library"><b>02</b>专辑库<span className="section-count">{releaseTotal}</span></a>
        {session.role === "admin" && (
          <a className={route === "settings" ? "section-link current" : "section-link"} aria-current={route === "settings" ? "page" : undefined} href="#/settings"><b>03</b>编辑部</a>
        )}
      </nav>

      {message && <p className="toast" role="alert">{message}</p>}

      <main className="front-page">
        {route === "home" && (
        <>
        {featureModel && featuredRelease && (
          <section className="feature-banner" id="feature-story" aria-label="头条专辑">
            <div className="feature-art">
              <ReleaseCover artwork={featureModel.artwork} title={featureModel.title} className="feature-cover" />
              <span className="feature-seal">头条</span>
            </div>
            <div className="feature-body">
              <p className="eyebrow">本周主推 · 点下方唱片更换</p>
              <h2>{featureModel.title}</h2>
              <p className="feature-meta">{featureModel.artistLabel} · {featureModel.yearLabel} · {featureModel.sizeLabel}</p>
              {bannerDetail && (
                <>
                  <div className="sheet-actions">
                    <button type="button" onClick={() => queuePlayItems(bannerQueueItems.slice(0, 1))} disabled={bannerQueueItems.length === 0}>
                      播放首曲
                    </button>
                    <button type="button" onClick={() => queuePlayItems(bannerQueueItems)} disabled={bannerQueueItems.length === 0}>
                      播放全部
                    </button>
                  </div>
                  <p className="banner-meta-line">
                    {bannerDetail.year !== null && <span>{bannerDetail.year}</span>}
                    <span>{bannerDetail.medium_count} 碟</span>
                    <span>{bannerDetail.track_count} 首</span>
                    {bannerSourceLabel && <span>{bannerSourceLabel}</span>}
                    {bannerDetail.genre && <span>{bannerDetail.genre}</span>}
                    {bannerDetail.label && <span>{bannerDetail.label}</span>}
                    {bannerDetail.catalog_number && <span>{bannerDetail.catalog_number}</span>}
                  </p>
                </>
              )}
            </div>
            <div className="feature-tracks">
              {featureDetail === null || featureDetail.status === "loading" ? (
                <div role="status" aria-label="正在加载头条详情">
                  <div className="skeleton-block skeleton-line" />
                  <div className="skeleton-block skeleton-line" />
                  <div className="skeleton-block skeleton-line short" />
                  <div className="skeleton-block skeleton-line" />
                  <div className="skeleton-block skeleton-line" />
                </div>
              ) : featureDetail.status === "error" ? (
                <p className="state-block error" role="alert">
                  头条详情加载失败
                  <button type="button" onClick={() => setFeatureRetry((retry) => retry + 1)}>重试</button>
                </p>
              ) : (
                featureDetail.detail.media.map((medium) => (
                  <MediumSection
                    key={medium.id}
                    medium={medium}
                    expanded={!bannerCollapsedMedia.has(medium.id)}
                    onToggle={toggleBannerMedium}
                    onPlayTrack={playBannerTrack}
                  />
                ))
              )}
            </div>
          </section>
        )}

        {shelfReleases.length > 0 && featuredRelease && (
          <section className="front-shelf" aria-label="新入架唱片">
            <div className="shelf-head">
              <p className="eyebrow">新入架</p>
              <a className="shelf-more" href="#/library">查看更多 →</a>
            </div>
            <div className="shelf-row" ref={shelfRowRef}>
              {shelfReleases.map((release) => {
                const model = toReleaseCardModel(release);
                const isCurrent = release.id === featuredRelease.id;
                return (
                  <button
                    key={release.id}
                    type="button"
                    className={isCurrent ? "shelf-item current" : "shelf-item"}
                    aria-pressed={isCurrent}
                    aria-label={`将 ${model.title} 设为头条`}
                    onClick={() => previewFeature(release.id)}
                  >
                    <ReleaseCover artwork={model.artwork} title={model.title} className="shelf-cover" />
                    <strong title={model.title}>{model.title}</strong>
                    <small title={model.artistLabel}>{model.artistLabel}</small>
                  </button>
                );
              })}
            </div>
          </section>
        )}

        <section className="program" id="queue" aria-label="节目单">
          <div className="section-heading">
            <div><p className="eyebrow">播放队列 · 演示模式</p><h2>节目单</h2></div>
            {queue.items.length > 0 && (
              <div className="heading-actions">
                <button className="outline-button" type="button" onClick={() => setQueue(clearDemoQueue)}>清空节目单</button>
              </div>
            )}
          </div>
          {queue.items.length === 0 ? (
            <p className="program-empty">
              节目单还空着——从发行详情里挑几首，排一期自己的节目。
              <span className="program-hint">演示模式，未连接音频服务</span>
            </p>
          ) : (
            <ol className="program-list">
              {queue.items.map((item, index) => (
                <li key={item.track.id} aria-current={index === queue.currentIndex ? "true" : undefined}>
                  <button type="button" className={index === queue.currentIndex ? "program-item current" : "program-item"} onClick={() => queuePlayTrack(item)}>
                    <span className="program-no">{String(index + 1).padStart(2, "0")}</span>
                    <b title={item.track.title}>{item.track.title || untitledTrackLabel}</b>
                    <small title={item.releaseArtist}>{item.releaseArtist}</small>
                  </button>
                </li>
              ))}
            </ol>
          )}
        </section>
        </>
        )}

        {route === "library" && (
        <section className="catalog-section" id="library">
          <div className="section-heading">
            <div><p className="eyebrow">私人音乐库</p><h2>专辑库</h2></div>
            <div className="heading-actions">
              {session.role === "admin" && (scan?.status === "running" ? (
                <button className="outline-button" type="button" onClick={() => void cancelScan()} disabled={scanMutationPending || scan.cancel_requested_at !== null}>
                  {scan.cancel_requested_at ? "取消请求中" : "停止扫描"}
                </button>
              ) : (
                <button className="outline-button" type="button" onClick={() => void startScan()} disabled={scanMutationPending}>↻ 扫描音乐库</button>
              ))}
            </div>
          </div>

          <CatalogToolbar
            searchInput={searchInput}
            submittedQuery={searchQuery}
            onSearchInput={setSearchInput}
            onSubmitSearch={submitSearch}
            onClearSearch={clearSearch}
            attentionRequired={attentionRequired}
            onToggleAttention={toggleAttentionFilter}
            viewMode={viewMode}
            onViewModeChange={setViewMode}
            onRefresh={() => setReleaseRetry((retry) => retry + 1)}
            pending={releaseLoading}
            stale={staleRefreshing}
            total={releaseTotal}
            page={releasePage}
            pageCount={releasePageCount}
            onPageChange={changeReleasePage}
          />

          {scanNotice && <p className="scan-notice" role="status">{scanNotice}</p>}

          {initialListLoading ? (
            <div className="release-grid" role="status" aria-label="正在加载发行版本">
              {Array.from({ length: 8 }, (_, index) => (
                <div className="skeleton-card" key={index}>
                  <div className="skeleton-block skeleton-cover" />
                  <div className="skeleton-block skeleton-line" />
                  <div className="skeleton-block skeleton-line short" />
                </div>
              ))}
            </div>
          ) : releaseError ? (
            <p className="state-block error" role="alert">{releaseError} <button type="button" onClick={() => setReleaseRetry((retry) => retry + 1)}>重试</button></p>
          ) : releases.length === 0 ? (
            attentionRequired ? (
              <p className="state-block">当前没有需要检查的发行版本 <button type="button" onClick={toggleAttentionFilter}>显示全部</button></p>
            ) : searchQuery ? (
              <p className="state-block">没有找到与“{searchQuery}”匹配的发行版本 <button type="button" onClick={clearSearch}>清除搜索</button></p>
            ) : (
              <p className="state-block">
                音乐库暂无发行版本
                {session.role === "admin"
                  ? <button type="button" onClick={() => void startScan()} disabled={scanMutationPending || scan?.status === "running"}>扫描音乐库</button>
                  : <span>请等待管理员完成扫描。</span>}
              </p>
            )
          ) : (
            <div className={viewMode === "grid" ? "release-grid" : "release-list"} aria-busy={staleRefreshing}>
              {releases.map((release) => (
                <ReleaseCard key={release.id} model={toReleaseCardModel(release)} layout={viewMode} onOpen={openRelease} />
              ))}
            </div>
          )}
        </section>
        )}

        {route === "settings" && session.role !== "admin" && (
          <p className="state-block">编辑部仅管理员可用。<a href="#/">回到首页</a></p>
        )}

        {route === "settings" && session.role === "admin" && (
          <section className="desk-section" id="admin">
            <div className="section-heading"><div><p className="eyebrow">管理员工具</p><h2>编辑部</h2></div></div>
            <div className="admin-grid">
              <section className="admin-card">
                <h3>用户</h3>
                <form onSubmit={createUser}><input aria-label="新用户名" placeholder="新用户名" value={newUsername} onChange={(event) => setNewUsername(event.target.value)} /><input aria-label="初始密码" type="password" placeholder="初始密码（至少 12 位）" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /><button type="submit">创建用户</button></form>
                {users.map((user) => <div className="admin-row" key={user.id}><span>{user.username}<small>{user.disabled ? "已禁用" : "正常"}</small></span><button type="button" onClick={() => void toggleUser(user)}>{user.disabled ? "启用" : "禁用"}</button><button type="button" onClick={() => void revokeUser(user)}>撤销</button></div>)}
              </section>
              <section className="admin-card">
                <h3>音乐目录</h3>
                <form onSubmit={registerRoot}><input aria-label="音乐目录路径" placeholder="允许的音乐目录路径" value={libraryPath} onChange={(event) => setLibraryPath(event.target.value)} /><button type="submit">注册目录</button></form>
                {libraryRoots.map((root) => <div className="admin-row" key={root.id}><span>{root.name}<small>{root.status} · r{root.revision}</small></span><button type="button" onClick={() => void changeRoot(root)}>{root.status === "active" ? "停用" : "恢复"}</button></div>)}
                {operations.slice(0, 3).map((item) => <div className="history-row" key={item.id}><span>{item.operation}</span><small>{item.status}</small></div>)}
              </section>
              <section className="admin-card scan-diagnostics-card">
                <h3>扫描诊断</h3>
                {!scan ? <p>当前没有进行中的扫描。</p> : <p>{describeScanState(scan)} · {scan.diagnostics.total} 条诊断</p>}
                {scanDiagnosticsLoading && <p role="status">正在读取诊断...</p>}
                {scanDiagnosticsError && <p className="error" role="alert">{scanDiagnosticsError}</p>}
                {scanDiagnostics && <>
                  <ul className="diagnostic-counts">{scanDiagnostics.summary.counts.map((count) => <li key={count.kind}><span>{count.kind}</span><strong>{count.count}</strong></li>)}</ul>
                  {scanDiagnostics.items.length === 0 ? <p>没有硬错误诊断。</p> : <ul className="diagnostic-list">{scanDiagnostics.items.map((item) => <li key={item.id}><strong>{item.kind}</strong><span>{item.path ?? "扫描级"}</span><small>{item.message}</small></li>)}</ul>}
                  {scanDiagnostics.items.length > 0 && <p className="readonly-hint">修正外部源文件、标签或目录权限后重新扫描。</p>}
                </>}
              </section>
            </div>
          </section>
        )}
      </main>

      {selectedReleaseId && (
        <ReleaseDetailDrawer
          key={selectedReleaseId}
          state={releaseDetail ?? { status: "loading" }}
          isAdmin={session.role === "admin"}
          evidence={releaseEvidence}
          evidenceLoading={releaseEvidenceLoading}
          evidenceError={releaseEvidenceError}
          onClose={closeReleaseSelection}
          onRetry={() => setDetailRetry((retry) => retry + 1)}
          onShowEvidence={() => void showReleaseEvidence()}
          onPlayItems={queuePlayItems}
          onPlayTrack={queuePlayTrack}
        />
      )}

      {currentQueueItem && queue.currentIndex !== null && (
        <footer className="player-bar">
          <div className="player-track">
            <ReleaseCover artwork={currentQueueItem.releaseArtwork} title={currentQueueItem.releaseTitle} className="mini-cover" />
            <div>
              <strong title={currentQueueItem.track.title}>{currentQueueItem.track.title || untitledTrackLabel}</strong>
              <span title={`${currentQueueItem.releaseArtist} · ${currentQueueItem.releaseTitle}`}>{currentQueueItem.releaseArtist} · {currentQueueItem.releaseTitle}</span>
            </div>
          </div>
          <div className="player-controls">
            <button type="button" aria-label="上一首" disabled={queue.currentIndex <= 0} onClick={() => setQueue(previousDemoTrack)}>|◀</button>
            <button className="play-button" type="button" aria-label={queue.isPlaying ? "暂停" : "播放"} onClick={() => setQueue((current) => setDemoPlaying(current, !current.isPlaying))}>{queue.isPlaying ? "Ⅱ" : "▶"}</button>
            <button type="button" aria-label="下一首" disabled={queue.currentIndex >= queue.items.length - 1} onClick={() => setQueue(nextDemoTrack)}>▶|</button>
          </div>
          <span className="player-position">{queue.currentIndex + 1} / {queue.items.length}</span>
          <button className="ghost-button" type="button" onClick={() => setQueue(removeCurrentDemoItem)}>移除当前</button>
          <span className="demo-badge">演示模式，未连接音频服务</span>
        </footer>
      )}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
