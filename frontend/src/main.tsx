/* global AbortController, URLSearchParams, window, crypto */
/* eslint-disable no-irregular-whitespace */
import { FormEvent, StrictMode, useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
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
  ReleaseDetailDTO,
  ReleaseEvidenceDTO,
  ReleaseSummaryDTO,
  requestApi,
  ScanStatusDTO,
  ScanDiagnosticsDTO,
  SessionDTO,
  RootOperationDTO,
  UserDTO,
} from "./api";
import { clampReleasePage, readReleaseFilters, releaseFilterURL } from "./release_filters";
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
  const [selectedRelease, setSelectedRelease] =
    useState<ReleaseDetailDTO | null>(null);
  const selectedReleaseGeneration = useRef(0);
  const [releaseEvidence, setReleaseEvidence] =
    useState<ReleaseEvidenceDTO | null>(null);
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
  const [releasePageSize, setReleasePageSize] = useState(50);
  const [releaseLoading, setReleaseLoading] = useState(false);
  const [releaseError, setReleaseError] = useState("");
  const [releaseRetry, setReleaseRetry] = useState(0);
  const [scan, setScan] = useState<ScanStatusDTO | null>(null);
  const [scanDiagnostics, setScanDiagnostics] =
    useState<ScanDiagnosticsDTO | null>(null);
  const [scanDiagnosticsLoading, setScanDiagnosticsLoading] = useState(false);
  const [scanDiagnosticsError, setScanDiagnosticsError] = useState("");
  const [scanMutationPending, setScanMutationPending] = useState(false);
  const [message, setMessage] = useState("");
  const [nowPlaying, setNowPlaying] = useState<ReleaseDetailDTO["media"][number]["tracks"][number] | null>(null);
  const [isPlaying, setIsPlaying] = useState(false);
  const invalidateSelectedRelease = useCallback(() => {
    selectedReleaseGeneration.current += 1;
    setSelectedRelease(null);
    setReleaseEvidence(null);
    setReleaseEvidenceLoading(false);
    setReleaseEvidenceError("");
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
      invalidateSelectedRelease();
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [invalidateSelectedRelease]);
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
          window.history.replaceState({}, "", releaseFilterURL(window.location.href, {
            query: searchQuery,
            attentionRequired,
            page: normalizedPage,
          }));
          setReleases([]);
          setReleaseTotal(result.pagination.total);
          setReleasePageSize(result.pagination.page_size);
          invalidateSelectedRelease();
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
  }, [session, scan?.status, searchQuery, attentionRequired, releasePage, releaseRetry, invalidateSelectedRelease]);
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
  async function showRelease(releaseID: string) {
    invalidateSelectedRelease();
    const requestGeneration = selectedReleaseGeneration.current;
    try {
      const detail = await requestApi(`/api/v1/releases/${releaseID}`, decodeReleaseDetail);
      if (selectedReleaseGeneration.current === requestGeneration) setSelectedRelease(detail);
    } catch (error: unknown) {
      if (selectedReleaseGeneration.current === requestGeneration) setMessage(describeError(error));
    }
  }
  async function showReleaseEvidence() {
    if (session?.role !== "admin" || !selectedRelease) return;
    const releaseID = selectedRelease.id;
    const requestGeneration = selectedReleaseGeneration.current;
    setReleaseEvidenceLoading(true);
    setReleaseEvidenceError("");
    try {
      const evidence = await requestApi(`/api/v1/releases/${releaseID}/evidence`, decodeReleaseEvidence);
      if (selectedReleaseGeneration.current === requestGeneration) setReleaseEvidence(evidence);
    } catch (error: unknown) {
      if (selectedReleaseGeneration.current === requestGeneration) setReleaseEvidenceError(describeError(error));
    } finally {
      if (selectedReleaseGeneration.current === requestGeneration) setReleaseEvidenceLoading(false);
    }
  }
  async function logout() {
    invalidateSelectedRelease();
    try {
      await requestApi("/api/v1/auth/logout", decodeAcknowledgement, {
        method: "POST",
        body: "{}",
      });
      setSession(null);
    } catch (error: unknown) {
      setMessage(describeError(error));
    }
  }
  function submitSearch(event: FormEvent) {
    event.preventDefault();
    const query = searchInput.trim();
    window.history.pushState({}, "", releaseFilterURL(window.location.href, { query, attentionRequired, page: 1 }));
    invalidateSelectedRelease();
    setSearchQuery(query);
    setReleasePage(1);
  }
  function clearSearch() {
    setSearchInput("");
    window.history.pushState({}, "", releaseFilterURL(window.location.href, { query: "", attentionRequired, page: 1 }));
    invalidateSelectedRelease();
    setSearchQuery("");
    setReleasePage(1);
  }
  function toggleAttentionFilter() {
    const nextValue = !attentionRequired;
    window.history.pushState({}, "", releaseFilterURL(window.location.href, { query: searchQuery, attentionRequired: nextValue, page: 1 }));
    invalidateSelectedRelease();
    setAttentionRequired(nextValue);
    setReleasePage(1);
  }
  function changeReleasePage(page: number) {
    const nextPage = Math.max(1, Math.min(page, Math.max(1, Math.ceil(releaseTotal / releasePageSize))));
    if (nextPage === releasePage) return;
    window.history.pushState({}, "", releaseFilterURL(window.location.href, { query: searchQuery, attentionRequired, page: nextPage }));
    invalidateSelectedRelease();
    setReleasePage(nextPage);
  }
  function playTrack(track: ReleaseDetailDTO["media"][number]["tracks"][number]) {
    setNowPlaying(track);
    setIsPlaying(true);
  }
  const releasePageCount = Math.max(1, Math.ceil(releaseTotal / releasePageSize));
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
    <main className="app-shell">
      <aside className="sidebar">
        <div className="brand"><span className="brand-mark">R</span><span>ROOMusic</span></div>
        <p className="side-label">工作区</p>
        <nav>
          <a className="nav-item active" href="#library">◈ <span>媒体库</span><b>{releaseTotal}</b></a>
          <a className="nav-item" href="#queue">≡ <span>播放队列</span></a>
          {session.role === "admin" && <a className="nav-item" href="#admin">⚙ <span>管理中心</span></a>}
        </nav>
        <div className="sidebar-bottom">
          <div className="profile"><span className="avatar">{session.username.slice(0, 1).toUpperCase()}</span><div><strong>{session.username}</strong><small>{session.role === "admin" ? "管理员" : "成员"}</small></div></div>
          <button className="ghost-button" type="button" onClick={() => void logout()}>退出登录</button>
        </div>
      </aside>

      <section className="workspace">
        <header className="topbar">
          <form className="searchbar" onSubmit={submitSearch}>
            <span>⌕</span>
            <label className="sr-only" htmlFor="library-search">搜索音乐库</label>
            <input id="library-search" value={searchInput} onChange={(event) => setSearchInput(event.target.value)} placeholder="搜索艺术家、专辑或曲目" />
            {searchQuery && <button type="button" onClick={clearSearch}>清除</button>}
          </form>
          <span className="sync-state" role="status"><i /> {describeScanState(scan)}</span>
        </header>
        {message && <p className="toast" role="alert">{message}</p>}

        <div className="content-grid">
          <section className="library-panel" id="library">
            <div className="section-heading">
              <div><p className="eyebrow">私人音乐库</p><h1>探索你的收藏</h1></div>
              <div className="heading-actions">
                <button className="filter-toggle" type="button" aria-pressed={attentionRequired} onClick={toggleAttentionFilter}>
                  {attentionRequired ? "显示全部" : "只看需要检查"}
                </button>
                <span className="result-count">{releaseTotal} 个发行版本</span>
                {session.role === "admin" && (scan?.status === "running" ? (
                  <button className="outline-button" type="button" onClick={() => void cancelScan()} disabled={scanMutationPending || scan.cancel_requested_at !== null}>
                    {scan.cancel_requested_at ? "取消请求中" : "停止扫描"}
                  </button>
                ) : (
                  <button className="outline-button" type="button" onClick={() => void startScan()} disabled={scanMutationPending}>↻ 扫描音乐库</button>
                ))}
              </div>
            </div>

            {releaseLoading ? (
              <p className="state-block" role="status">正在加载发行版本...</p>
            ) : releaseError ? (
              <p className="state-block error" role="alert">{releaseError} <button type="button" onClick={() => setReleaseRetry((retry) => retry + 1)}>重试</button></p>
            ) : releases.length === 0 ? (
              <p className="state-block">{attentionRequired ? "当前没有需要检查的发行版本" : "暂无匹配的发行版本"}</p>
            ) : (
              <div className="release-grid">
                {releases.map((release) => (
                  <button className="release-card" key={release.id} type="button" onClick={() => void showRelease(release.id)}>
                    <div className="cover-placeholder">{release.title.slice(0, 1) || "♫"}</div>
                    <strong title={release.title}>{release.title || "未知专辑"}</strong>
                    <span title={release.album_artist ?? release.artist}>{(release.album_artist ?? release.artist) || "艺术家未知"}</span>
                    <small>{release.year ?? "年份未知"} · {release.medium_count} 碟 / {release.track_count} 首</small>
                    {(release.source_type || release.media_type) && <small>{[release.source_type, release.media_type].filter(Boolean).join(" · ")}</small>}
                    {release.attention_count > 0 && <span className="attention-badge">需要检查 {release.attention_count}</span>}
                  </button>
                ))}
              </div>
            )}

            {(releasePage > 1 || releaseTotal > releasePageSize) && (
              <nav className="pagination" aria-label="发行版本分页">
                <button type="button" onClick={() => changeReleasePage(releasePage - 1)} disabled={releaseLoading || releasePage <= 1}>上一页</button>
                <span>第 {releasePage} / {releasePageCount} 页</span>
                <button type="button" onClick={() => changeReleasePage(releasePage + 1)} disabled={releaseLoading || releasePage >= releasePageCount}>下一页</button>
              </nav>
            )}

            {selectedRelease && (
              <article className="detail-panel">
                <div className="detail-cover">
                  {selectedRelease.artwork ? <img src={`/api/v1/artworks/${encodeURIComponent(selectedRelease.artwork.resource_id)}`} alt={`${selectedRelease.title} 封面`} /> : <span>{selectedRelease.title.slice(0, 1) || "♫"}</span>}
                </div>
                <div className="detail-body">
                  <p className="eyebrow">发行详情</p>
                  <h2>{selectedRelease.title || "未知专辑"}</h2>
                  <p className="detail-artist">{(selectedRelease.album_artist ?? selectedRelease.artist) || "艺术家未知"}</p>
                  <dl className="release-facts">
                    <div><dt>年份</dt><dd>{selectedRelease.year ?? "未知"}</dd></div>
                    <div><dt>来源 / 介质</dt><dd>{[selectedRelease.source_type, selectedRelease.media_type].filter(Boolean).join(" / ") || "未记录"}</dd></div>
                    <div><dt>规模</dt><dd>{selectedRelease.medium_count} 碟 · {selectedRelease.track_count} 首</dd></div>
                    <div><dt>候选类型</dt><dd>{selectedRelease.candidate_kind}</dd></div>
                    {selectedRelease.genre && <div><dt>流派</dt><dd>{selectedRelease.genre}</dd></div>}
                    {selectedRelease.catalog_number && <div><dt>目录号</dt><dd>{selectedRelease.catalog_number}</dd></div>}
                  </dl>
                  {selectedRelease.credits.length > 0 && <p className="credit-line">署名：{selectedRelease.credits.map((credit) => `${credit.role} · ${credit.name}`).join("；")}</p>}

                  {selectedRelease.media.map((medium) => (
                    <div className="medium" key={medium.id}>
                      <h3>碟片 {medium.position} <span>{medium.title}</span></h3>
                      <ol>
                        {medium.tracks.map((track) => (
                          <li key={track.id}>
                            <button type="button" onClick={() => playTrack(track)}>
                              <span>{String(track.position).padStart(2, "0")}</span>
                              <b>{track.title || "未命名曲目"}</b>
                              <small>
                                {track.artist || "艺术家未知"}
                                {track.codec ? ` · ${track.codec}` : ""}
                                {track.bit_depth ? ` · ${track.bit_depth} bit` : ""}
                                {track.sample_rate ? ` · ${Math.round(track.sample_rate / 1000)} kHz` : ""}
                                {track.channels ? ` · ${track.channels} 声道` : ""}
                                {track.bitrate ? ` · ${track.bitrate} kbps` : ""}
                                {track.cue?.start_seconds !== null && track.cue ? ` · CUE ${track.cue.start_seconds.toFixed(2)}s` : ""}
                              </small>
                              <i>▶</i>
                            </button>
                          </li>
                        ))}
                      </ol>
                    </div>
                  ))}

                  {selectedRelease.evidence.length > 0 && (
                    <section aria-label="整理证据摘要" className="evidence-panel">
                      <h3>整理证据摘要</h3>
                      <ul>{selectedRelease.evidence.map((item) => <li key={`${item.field}-${item.rule_id}`}><strong>{item.field}</strong><span>{item.source} · {item.confidence} · {item.action}</span></li>)}</ul>
                    </section>
                  )}

                  {session.role === "admin" && (
                    <section aria-label="管理员整理证据" className="evidence-panel detailed-evidence">
                      <div className="panel-heading"><h3>管理员证据</h3><button type="button" onClick={() => void showReleaseEvidence()} disabled={releaseEvidenceLoading}>{releaseEvidenceLoading ? "正在读取..." : "查看完整证据"}</button></div>
                      {releaseEvidenceError && <p className="error" role="alert">{releaseEvidenceError}</p>}
                      {releaseEvidence && <>
                        {releaseEvidence.fields.length === 0 ? <p>此发行版本没有字段证据。</p> : <ul>{releaseEvidence.fields.map((item) => <li key={`${item.field}-${item.rule_id}`}><strong>{item.field}</strong><span>{item.value ?? "未选择值"} · {item.confidence} · {item.action}</span>{item.reason_code && <small>{item.reason_code}</small>}{item.candidates.length > 0 && <small>候选：{item.candidates.join("、")}</small>}</li>)}</ul>}
                        {releaseEvidence.grouping && <div className="grouping-evidence"><strong>归组：{releaseEvidence.grouping.candidate_kind}</strong>{releaseEvidence.grouping.reason_codes.map((reason) => <small key={reason}>{reason}</small>)}{releaseEvidence.grouping.source_refs.length > 0 && <ul>{releaseEvidence.grouping.source_refs.map((sourceRef) => <li key={sourceRef}><code>{sourceRef}</code></li>)}</ul>}</div>}
                        {releaseEvidence.truncated && <p>证据已按安全上限截断。</p>}
                      </>}
                    </section>
                  )}
                  <p className="readonly-hint">如需更正，请修改外部源文件或标签后重新扫描。ROOMusic 不会改写音乐源。</p>
                </div>
              </article>
            )}
          </section>

          <aside className="queue-panel" id="queue">
            <div className="queue-heading"><h2>即将播放</h2><span>{nowPlaying ? "演示队列" : "空队列"}</span></div>
            {nowPlaying ? <div className="queue-now"><div className="mini-cover">{nowPlaying.title.slice(0, 1) || "♫"}</div><div><strong title={nowPlaying.title}>{nowPlaying.title || "未命名曲目"}</strong><span title={nowPlaying.artist}>{nowPlaying.artist || "艺术家未知"}</span></div><button type="button" aria-label={isPlaying ? "暂停播放" : "播放"} onClick={() => setIsPlaying((playing) => !playing)}>{isPlaying ? "Ⅱ" : "▶"}</button></div> : <div className="queue-empty"><span>♫</span><p>从发行详情中选择一首曲目<br />开始播放演示</p></div>}
            <div className="queue-tip"><span>⌘</span><p>播放控制为界面演示<br />暂未连接音频流服务</p></div>
          </aside>
        </div>

        {session.role === "admin" && (
          <section className="admin-panel" id="admin">
            <div className="section-heading"><div><p className="eyebrow">管理员工具</p><h2>管理中心</h2></div></div>
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
      </section>

      {nowPlaying && <footer className="player-bar"><div className="player-track"><div className="mini-cover">{nowPlaying.title.slice(0, 1) || "♫"}</div><div><strong title={nowPlaying.title}>{nowPlaying.title || "未命名曲目"}</strong><span title={nowPlaying.artist}>{nowPlaying.artist || "艺术家未知"}</span></div></div><div className="player-controls"><button type="button" aria-label="上一首">|◀</button><button className="play-button" type="button" aria-label={isPlaying ? "暂停" : "播放"} onClick={() => setIsPlaying((playing) => !playing)}>{isPlaying ? "Ⅱ" : "▶"}</button><button type="button" aria-label="下一首">▶|</button></div><div className="progress"><span>1:24</span><div><i /></div><span>4:12</span></div><span className="volume">⌁　▮▮▮</span></footer>}
    </main>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
