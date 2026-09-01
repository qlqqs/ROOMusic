/* global window */
import { FormEvent, StrictMode, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  decodeAcknowledgement,
  decodeCreatedLibraryRoot,
  decodeLibraryRootList,
  decodeReleaseDetail,
  decodeReleaseList,
  decodeScanStart,
  decodeScanStatus,
  decodeSession,
  decodeSetupStatus,
  LibraryRootDTO,
  ReleaseDetailDTO,
  ReleaseSummaryDTO,
  requestApi,
  ScanStartDTO,
  ScanStatusDTO,
  SessionDTO,
} from "./api";
import "./styles.css";

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : "请求失败";
}

function App() {
  const [session, setSession] = useState<SessionDTO | null>(null); const [setupRequired, setSetupRequired] = useState<boolean | null>(null);
  const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [libraryPath, setLibraryPath] = useState("");
  const [libraryRoots, setLibraryRoots] = useState<LibraryRootDTO[]>([]);
  const [releases, setReleases] = useState<ReleaseSummaryDTO[]>([]); const [releaseTotal, setReleaseTotal] = useState(0); const [selectedRelease, setSelectedRelease] = useState<ReleaseDetailDTO | null>(null);
  const [scan, setScan] = useState<ScanStatusDTO | ScanStartDTO | null>(null); const [message, setMessage] = useState("");
  useEffect(() => { void requestApi("/api/v1/setup/status", decodeSetupStatus).then((status) => { setSetupRequired(status.setup_required); if (!status.setup_required) void requestApi("/api/v1/auth/me", decodeSession).then(setSession).catch(() => undefined); }).catch((error: unknown) => setMessage(describeError(error))); }, []);
  useEffect(() => { if (!session) return; void requestApi("/api/v1/library-roots", decodeLibraryRootList).then((result) => setLibraryRoots(result.items)).catch((error: unknown) => setMessage(describeError(error))); }, [session]);
  useEffect(() => { if (session) void requestApi("/api/v1/releases?page=1&page_size=50", decodeReleaseList).then((result) => { setReleases(result.items); setReleaseTotal(result.pagination.total); }).catch((error: unknown) => setMessage(describeError(error))); }, [session, scan?.status]);
  useEffect(() => { if (!scan || scan.status !== "running") return; const timer = window.setInterval(() => { void requestApi(`/api/v1/scans/${scan.id}`, decodeScanStatus).then(setScan).catch((error: unknown) => setMessage(describeError(error))); }, 1000); return () => window.clearInterval(timer); }, [scan]);
  async function submitAuth(event: FormEvent) { event.preventDefault(); try { const path = setupRequired ? "/api/v1/setup/admin" : "/api/v1/auth/login"; const authenticatedSession = await requestApi(path, decodeSession, { method: "POST", body: JSON.stringify({ username, password }) }); if (setupRequired) { setSetupRequired(false); setSession(await requestApi("/api/v1/auth/login", decodeSession, { method: "POST", body: JSON.stringify({ username, password }) })); } else { setSession(authenticatedSession); } } catch (error: unknown) { setMessage(describeError(error)); } }
  async function registerRoot(event: FormEvent) { event.preventDefault(); try { const createdRoot = await requestApi("/api/v1/library-roots", decodeCreatedLibraryRoot, { method: "POST", body: JSON.stringify({ path: libraryPath }) }); const refreshedRoots = await requestApi("/api/v1/library-roots", decodeLibraryRootList); setLibraryRoots(refreshedRoots.items); setMessage(`目录“${createdRoot.name}”已注册`); setLibraryPath(""); } catch (error: unknown) { setMessage(describeError(error)); } }
  async function startScan() { try { setScan(await requestApi("/api/v1/scans", decodeScanStart, { method: "POST", body: "{}" })); setMessage("扫描已启动"); } catch (error: unknown) { setMessage(describeError(error)); } }
  async function showRelease(releaseID: string) { try { setSelectedRelease(await requestApi(`/api/v1/releases/${releaseID}`, decodeReleaseDetail)); } catch (error: unknown) { setMessage(describeError(error)); } }
  async function logout() { try { await requestApi("/api/v1/auth/logout", decodeAcknowledgement, { method: "POST", body: "{}" }); setSession(null); } catch (error: unknown) { setMessage(describeError(error)); } }
  if (setupRequired === null) return <main className="shell"><p role="status">正在加载...</p></main>;
  if (!session) return <main className="shell"><p className="eyebrow">本地音乐库</p><h1>ROOMusic</h1><h2>{setupRequired ? "创建管理员" : "管理员登录"}</h2><form onSubmit={submitAuth}><label>用户名<input value={username} onChange={(event) => setUsername(event.target.value)} /></label><label>密码<input type="password" value={password} onChange={(event) => setPassword(event.target.value)} /></label><button type="submit">{setupRequired ? "开始使用" : "登录"}</button></form>{message && <p role="alert">{message}</p>}</main>;
  return <main className="shell"><p className="eyebrow">管理员 · {session.username}</p><h1>ROOMusic</h1><form onSubmit={registerRoot}><label>音乐目录<input placeholder="允许的音乐目录路径" value={libraryPath} onChange={(event) => setLibraryPath(event.target.value)} /></label><button type="submit">注册目录</button></form><h2>已注册目录</h2>{libraryRoots.length === 0 ? <p>尚未注册音乐目录</p> : <ul>{libraryRoots.map((root) => <li key={root.id}>{root.path}</li>)}</ul>}<button type="button" onClick={startScan} disabled={scan?.status === "running"}>扫描音乐库</button> <button type="button" onClick={() => void logout()}>退出登录</button>{scan && <p role="status">扫描状态：{scan.status}</p>}<h2>发行版本</h2><p>共 {releaseTotal} 个发行版本</p>{releases.length === 0 ? <p>暂无扫描结果</p> : <ul>{releases.map((release) => <li key={release.id}><button type="button" onClick={() => void showRelease(release.id)}>{release.title} · {release.artist}</button></li>)}</ul>}{selectedRelease && <section aria-label="发行版本详情"><h2>{selectedRelease.title}</h2><p>{selectedRelease.artist}</p>{selectedRelease.media.map((medium) => <section key={medium.id} aria-label={`碟片 ${medium.position}`}><h3>碟片 {medium.position} · {medium.title}</h3>{medium.tracks.length === 0 ? <p>此碟片暂无曲目</p> : <ol>{medium.tracks.map((track) => <li key={track.id}>{track.position}. {track.title} · {track.artist} <small>来源：{track.source}</small></li>)}</ol>}</section>)}</section>}{message && <p role="alert">{message}</p>}</main>;
}

createRoot(document.getElementById("root")!).render(<StrictMode><App /></StrictMode>);
