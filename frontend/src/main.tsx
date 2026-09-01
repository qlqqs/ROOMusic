/* global URL, URLSearchParams, window, crypto */
/* eslint-disable no-irregular-whitespace */
import { FormEvent, StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  decodeAcknowledgement,
  decodeCreatedLibraryRoot,
  decodeCreatedUser,
  decodeLibraryRootList,
  decodeRootOperationList,
  decodeUserList,
  decodeReleaseDetail,
  decodeReleaseList,
  decodeScanStart,
  decodeScanStatus,
  decodeSession,
  decodeUpdatedLibraryRoot,
  decodeUpdatedUser,
  decodeSetupStatus,
  LibraryRootDTO,
  ReleaseDetailDTO,
  ReleaseSummaryDTO,
  requestApi,
  ScanStartDTO,
  ScanStatusDTO,
  SessionDTO,
  RootOperationDTO,
  UserDTO,
} from "./api";
import "./styles.css";

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : "请求失败";
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
  const [searchInput, setSearchInput] = useState(
    () => new URLSearchParams(window.location.search).get("q") ?? "",
  );
  const [searchQuery, setSearchQuery] = useState(
    () => new URLSearchParams(window.location.search).get("q")?.trim() ?? "",
  );
  const [releaseLoading, setReleaseLoading] = useState(false);
  const [releaseError, setReleaseError] = useState("");
  const [releaseRetry, setReleaseRetry] = useState(0);
  const [scan, setScan] = useState<ScanStatusDTO | ScanStartDTO | null>(null);
  const [message, setMessage] = useState("");
  const [nowPlaying, setNowPlaying] = useState<ReleaseDetailDTO["media"][number]["tracks"][number] | null>(null);
  const [isPlaying, setIsPlaying] = useState(false);
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
  }, [session]);
  useEffect(() => {
    const onPopState = () => {
      const query =
        new URLSearchParams(window.location.search).get("q")?.trim() ?? "";
      setSearchInput(query);
      setSearchQuery(query);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);
  useEffect(() => {
    if (!session) return;
    const params = new URLSearchParams({ page: "1", page_size: "50" });
    if (searchQuery) params.set("q", searchQuery);
    setReleaseLoading(true);
    setReleaseError("");
    void requestApi(`/api/v1/releases?${params.toString()}`, decodeReleaseList)
      .then((result) => {
        setReleases(result.items);
        setReleaseTotal(result.pagination.total);
      })
      .catch((error: unknown) => {
        setReleaseError(describeError(error));
      })
      .finally(() => setReleaseLoading(false));
  }, [session, scan?.status, searchQuery, releaseRetry]);
  useEffect(() => {
    if (!scan || scan.status !== "running") return;
    const timer = window.setInterval(() => {
      void requestApi(`/api/v1/scans/${scan.id}`, decodeScanStatus)
        .then(setScan)
        .catch((error: unknown) => setMessage(describeError(error)));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [scan]);
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
    try {
      setSelectedRelease(
        await requestApi(`/api/v1/releases/${releaseID}`, decodeReleaseDetail),
      );
    } catch (error: unknown) {
      setMessage(describeError(error));
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
  }
  function submitSearch(event: FormEvent) {
    event.preventDefault();
    const query = searchInput.trim();
    const url = new URL(window.location.href);
    if (query) url.searchParams.set("q", query);
    else url.searchParams.delete("q");
    window.history.pushState({}, "", url);
    setSearchQuery(query);
  }
  function clearSearch() {
    setSearchInput("");
    const url = new URL(window.location.href);
    url.searchParams.delete("q");
    window.history.pushState({}, "", url);
    setSearchQuery("");
  }
  function playTrack(track: ReleaseDetailDTO["media"][number]["tracks"][number]) {
    setNowPlaying(track);
    setIsPlaying(true);
  }
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
  return <main className="app-shell">
    <aside className="sidebar"><div className="brand"><span className="brand-mark">R</span><span>ROOMusic</span></div><p className="side-label">工作区</p><nav><a className="nav-item active" href="#library">◈ <span>媒体库</span><b>{releaseTotal}</b></a><a className="nav-item" href="#queue">≡ <span>播放队列</span></a>{session.role === "admin" && <a className="nav-item" href="#admin">⚙ <span>管理中心</span></a>}</nav><div className="sidebar-bottom"><div className="profile"><span className="avatar">{session.username.slice(0, 1).toUpperCase()}</span><div><strong>{session.username}</strong><small>{session.role === "admin" ? "管理员" : "成员"}</small></div></div><button className="ghost-button" type="button" onClick={() => void logout()}>退出登录</button></div></aside>
    <section className="workspace"><header className="topbar"><form className="searchbar" onSubmit={submitSearch}><span>⌕</span><label className="sr-only" htmlFor="library-search">搜索音乐库</label><input id="library-search" value={searchInput} onChange={(e) => setSearchInput(e.target.value)} placeholder="搜索艺术家、专辑或曲目" />{searchQuery && <button type="button" onClick={clearSearch}>清除</button>}</form><span className="sync-state"><i /> {scan?.status === "running" ? "正在扫描" : "已同步"}</span></header>
      {message && <p className="toast" role="alert">{message}</p>}
      <div className="content-grid"><section className="library-panel" id="library"><div className="section-heading"><div><p className="eyebrow">私人音乐库</p><h1>探索你的收藏</h1></div><div className="heading-actions"><span className="result-count">{releaseTotal} 个发行版本</span>{session.role === "admin" && <button className="outline-button" type="button" onClick={() => void startScan()} disabled={scan?.status === "running"}>↻ 扫描音乐库</button>}</div></div>{releaseLoading ? <p className="state-block" role="status">正在加载发行版本...</p> : releaseError ? <p className="state-block error" role="alert">{releaseError} <button type="button" onClick={() => setReleaseRetry((r) => r + 1)}>重试</button></p> : releases.length === 0 ? <p className="state-block">暂无匹配的发行版本</p> : <div className="release-grid">{releases.map((release) => <button className="release-card" key={release.id} type="button" onClick={() => void showRelease(release.id)}><div className="cover-placeholder">{release.title.slice(0, 1)}</div><strong title={release.title}>{release.title}</strong><span title={release.artist}>{release.artist}</span><small>{release.year ?? "年份未知"}</small></button>)}</div>}
        {selectedRelease && <article className="detail-panel"><div className="detail-cover">{selectedRelease.artwork ? <img src={`/api/v1/artworks/${encodeURIComponent(selectedRelease.artwork.resource_id)}`} alt={`${selectedRelease.title} 封面`} /> : <span>{selectedRelease.title.slice(0, 1)}</span>}</div><div className="detail-body"><p className="eyebrow">发行详情</p><h2>{selectedRelease.title}</h2><p className="detail-artist">{selectedRelease.artist}</p>{selectedRelease.media.map((medium) => <div className="medium" key={medium.id}><h3>碟片 {medium.position} <span>{medium.title}</span></h3>{medium.tracks.length === 0 ? <p>此碟片暂无曲目</p> : <ol>{medium.tracks.map((track) => <li key={track.id}><button type="button" onClick={() => playTrack(track)}><span>{String(track.position).padStart(2, "0")}</span><b>{track.title}</b><small>{track.artist}</small><i>▶</i></button></li>)}</ol>}</div>)}</div></article>}
      </section><aside className="queue-panel" id="queue"><div className="queue-heading"><h2>即将播放</h2><span>{nowPlaying ? "演示队列" : "空队列"}</span></div>{nowPlaying ? <div className="queue-now"><div className="mini-cover">{nowPlaying.title.slice(0, 1)}</div><div><strong title={nowPlaying.title}>{nowPlaying.title}</strong><span title={nowPlaying.artist}>{nowPlaying.artist}</span></div><button type="button" aria-label={isPlaying ? "暂停播放" : "播放"} onClick={() => setIsPlaying((v) => !v)}>{isPlaying ? "Ⅱ" : "▶"}</button></div> : <div className="queue-empty"><span>♫</span><p>从发行详情中选择一首曲目<br />开始播放演示</p></div>}<div className="queue-tip"><span>⌘</span><p>播放控制为界面演示<br />暂未连接音频流服务</p></div></aside></div>
      {session.role === "admin" && <section className="admin-panel" id="admin"><div className="section-heading"><div><p className="eyebrow">管理员工具</p><h2>管理中心</h2></div></div><div className="admin-grid"><section className="admin-card"><h3>用户</h3><form onSubmit={createUser}><input aria-label="新用户名" placeholder="新用户名" value={newUsername} onChange={(e) => setNewUsername(e.target.value)} /><input aria-label="初始密码" type="password" placeholder="初始密码（至少 12 位）" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} /><button type="submit">创建用户</button></form>{users.map((user) => <div className="admin-row" key={user.id}><span>{user.username}<small>{user.disabled ? "已禁用" : "正常"}</small></span><button type="button" onClick={() => void toggleUser(user)}>{user.disabled ? "启用" : "禁用"}</button><button type="button" onClick={() => void revokeUser(user)}>撤销</button></div>)}</section><section className="admin-card"><h3>音乐目录</h3><form onSubmit={registerRoot}><input aria-label="音乐目录路径" placeholder="允许的音乐目录路径" value={libraryPath} onChange={(e) => setLibraryPath(e.target.value)} /><button type="submit">注册目录</button></form>{libraryRoots.map((root) => <div className="admin-row" key={root.id}><span>{root.name}<small>{root.status} · r{root.revision}</small></span><button type="button" onClick={() => void changeRoot(root)}>{root.status === "active" ? "停用" : "恢复"}</button></div>)}{operations.slice(0, 3).map((item) => <div className="history-row" key={item.id}><span>{item.operation}</span><small>{item.status}</small></div>)}</section></div></section>}
    </section>{nowPlaying && <footer className="player-bar"><div className="player-track"><div className="mini-cover">{nowPlaying.title.slice(0, 1)}</div><div><strong title={nowPlaying.title}>{nowPlaying.title}</strong><span title={nowPlaying.artist}>{nowPlaying.artist}</span></div></div><div className="player-controls"><button type="button" aria-label="上一首">|◀</button><button className="play-button" type="button" aria-label={isPlaying ? "暂停" : "播放"} onClick={() => setIsPlaying((v) => !v)}>{isPlaying ? "Ⅱ" : "▶"}</button><button type="button" aria-label="下一首">▶|</button></div><div className="progress"><span>1:24</span><div><i /></div><span>4:12</span></div><span className="volume">⌁　▮▮▮</span></footer>}
  </main>;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
