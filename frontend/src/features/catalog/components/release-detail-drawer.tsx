/* global HTMLElement, HTMLDivElement, HTMLButtonElement */
import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import type { ReleaseDetailDTO, ReleaseEvidenceDTO, TrackDTO } from "../../../api";
import {
  flattenReleaseTracks,
  formatReleaseLabel,
  formatSourceMediaLabel,
  type DemoQueueItem,
} from "../model/display";
import { MediumSection } from "./medium-section";
import { ReleaseCover } from "./release-cover";

export type ReleaseDetailState =
  | { status: "loading" }
  | { status: "error"; code: string }
  | { status: "ready"; detail: ReleaseDetailDTO };

type ReleaseDetailDrawerProps = {
  state: ReleaseDetailState;
  isAdmin: boolean;
  evidence: ReleaseEvidenceDTO | null;
  evidenceLoading: boolean;
  evidenceError: string;
  onClose: () => void;
  onRetry: () => void;
  onShowEvidence: () => void;
  onPlayItems: (items: DemoQueueItem[]) => void;
  onPlayTrack: (item: DemoQueueItem) => void;
};

const focusableSelector = "button, [href], input, select, textarea, [tabindex]:not([tabindex='-1'])";

// 详情内页：整页翻开的杂志内页（非抽屉），role=dialog、焦点圈定、Escape 关闭、关闭后焦点回到触发卡片。
// 每个发行版本通过 key 重建，本地 disclosure 状态随之重置。
export function ReleaseDetailDrawer(props: ReleaseDetailDrawerProps) {
  const panelRef = useRef<HTMLDivElement | null>(null);
  const closeButtonRef = useRef<HTMLButtonElement | null>(null);
  const [collapsedMedia, setCollapsedMedia] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => {
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButtonRef.current?.focus();
    return () => {
      previouslyFocused?.focus();
    };
  }, []);

  function handleKeyDown(event: ReactKeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.stopPropagation();
      props.onClose();
      return;
    }
    if (event.key !== "Tab" || !panelRef.current) return;
    const focusable = Array.from(panelRef.current.querySelectorAll<HTMLElement>(focusableSelector))
      .filter((element) => !element.hasAttribute("disabled") && element.closest("[hidden]") === null);
    if (focusable.length === 0) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || !panelRef.current.contains(active))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || !panelRef.current.contains(active))) {
      event.preventDefault();
      first.focus();
    }
  }

  function toggleMedium(mediumID: string) {
    setCollapsedMedia((collapsed) => {
      const next = new Set(collapsed);
      if (next.has(mediumID)) next.delete(mediumID);
      else next.add(mediumID);
      return next;
    });
  }

  const queueItems = useMemo(
    () => (props.state.status === "ready" ? flattenReleaseTracks(props.state.detail) : []),
    [props.state],
  );

  function playTrack(track: TrackDTO) {
    const item = queueItems.find((candidate) => candidate.track.id === track.id);
    if (item) props.onPlayTrack(item);
  }

  return (
    <div className="sheet-overlay" onClick={props.onClose}>
      <div
        className="sheet-page"
        role="dialog"
        aria-modal="true"
        aria-labelledby="release-detail-label release-detail-title"
        ref={panelRef}
        onKeyDown={handleKeyDown}
        onClick={(event) => event.stopPropagation()}
      >
        <div className="sheet-heading">
          <p className="eyebrow" id="release-detail-label">ROOMusic · 发行详情</p>
          <button type="button" className="sheet-close" ref={closeButtonRef} onClick={props.onClose} aria-label="关闭详情">
            ✕ 合回本刊
          </button>
        </div>
        {props.state.status === "loading" && (
          <div className="sheet-skeleton" role="status" aria-label="正在加载发行详情">
            <div className="skeleton-block skeleton-cover" />
            <div className="skeleton-block skeleton-line" />
            <div className="skeleton-block skeleton-line short" />
            <div className="skeleton-block skeleton-line" />
          </div>
        )}
        {props.state.status === "error" && (
          <DrawerError code={props.state.code} onRetry={props.onRetry} onClose={props.onClose} />
        )}
        {props.state.status === "ready" && (
          <ReadyDetail
            detail={props.state.detail}
            isAdmin={props.isAdmin}
            evidence={props.evidence}
            evidenceLoading={props.evidenceLoading}
            evidenceError={props.evidenceError}
            onShowEvidence={props.onShowEvidence}
            queueItems={queueItems}
            onPlayItems={props.onPlayItems}
            onPlayTrack={playTrack}
            collapsedMedia={collapsedMedia}
            onToggleMedium={toggleMedium}
            onExpandAll={() => setCollapsedMedia(new Set())}
            onCollapseAll={(mediaIDs) => setCollapsedMedia(new Set(mediaIDs))}
          />
        )}
      </div>
    </div>
  );
}

function DrawerError({ code, onRetry, onClose }: { code: string; onRetry: () => void; onClose: () => void }) {
  if (code === "forbidden") {
    return <p className="state-block error" role="alert">没有权限查看该发行版本的详情。</p>;
  }
  if (code === "not_found") {
    return <p className="state-block error" role="alert">发行版本不存在或已被移除。<button type="button" onClick={onClose}>关闭</button></p>;
  }
  return (
    <p className="state-block error" role="alert">
      详情暂时无法加载，请稍后重试。
      <button type="button" onClick={onRetry}>重试</button>
    </p>
  );
}

type ReadyDetailProps = {
  detail: ReleaseDetailDTO;
  isAdmin: boolean;
  evidence: ReleaseEvidenceDTO | null;
  evidenceLoading: boolean;
  evidenceError: string;
  onShowEvidence: () => void;
  queueItems: DemoQueueItem[];
  onPlayItems: (items: DemoQueueItem[]) => void;
  onPlayTrack: (track: TrackDTO) => void;
  collapsedMedia: ReadonlySet<string>;
  onToggleMedium: (mediumID: string) => void;
  onExpandAll: () => void;
  onCollapseAll: (mediaIDs: string[]) => void;
};

function ReadyDetail(props: ReadyDetailProps) {
  const { detail } = props;
  const title = detail.title.trim() === "" ? "未知专辑" : detail.title;
  const sourceLabel = formatSourceMediaLabel(detail);
  const hasTracks = props.queueItems.length > 0;
  return (
    <div className="sheet-body">
      <div className="spread-left">
        <ReleaseCover artwork={detail.artwork} title={title} className="detail-cover sheet-cover" allowRetry />
        <dl className="release-facts">
          {detail.year !== null && <div><dt>年份</dt><dd>{detail.year}</dd></div>}
          {sourceLabel !== null && <div><dt>来源 / 介质</dt><dd>{sourceLabel}</dd></div>}
          <div><dt>规模</dt><dd>{detail.medium_count} 碟 · {detail.track_count} 首</dd></div>
          <div><dt>候选类型</dt><dd>{detail.candidate_kind}</dd></div>
          {detail.genre && <div><dt>流派</dt><dd>{detail.genre}</dd></div>}
          {detail.catalog_number && <div><dt>目录号</dt><dd>{detail.catalog_number}</dd></div>}
          {detail.edition && <div><dt>版本</dt><dd>{detail.edition}</dd></div>}
          {detail.label && <div><dt>唱片公司</dt><dd>{detail.label}</dd></div>}
          {detail.barcode && <div><dt>条码</dt><dd>{detail.barcode}</dd></div>}
        </dl>
        {detail.credits.length > 0 && (
          <p className="credit-line">署名：{detail.credits.map((credit) => `${credit.role} · ${credit.name}`).join("；")}</p>
        )}
      </div>
      <div className="spread-right">
        <h2 id="release-detail-title">{title}</h2>
        <p className="detail-artist">{formatReleaseLabel(detail)}</p>
        <div className="sheet-actions">
          <button type="button" onClick={() => props.onPlayItems(props.queueItems.slice(0, 1))} disabled={!hasTracks}>
            播放首曲
          </button>
          <button type="button" onClick={() => props.onPlayItems(props.queueItems)} disabled={!hasTracks}>
            播放全部
          </button>
          <button type="button" onClick={props.onExpandAll}>全部展开</button>
          <button type="button" onClick={() => props.onCollapseAll(detail.media.map((medium) => medium.id))}>
            全部收起
          </button>
        </div>

        {detail.media.map((medium) => (
          <MediumSection
            key={medium.id}
            medium={medium}
            expanded={!props.collapsedMedia.has(medium.id)}
            onToggle={props.onToggleMedium}
            onPlayTrack={props.onPlayTrack}
          />
        ))}

        {detail.evidence.length > 0 && (
          <details className="evidence-panel">
            <summary>整理证据摘要（{detail.evidence.length} 条）</summary>
            <ul>{detail.evidence.map((item) => <li key={`${item.field}-${item.rule_id}`}><strong>{item.field}</strong><span>{item.source} · {item.confidence} · {item.action}</span></li>)}</ul>
          </details>
        )}

        {props.isAdmin && (
          <section aria-label="管理员整理证据" className="evidence-panel detailed-evidence">
            <div className="panel-heading">
              <h3>管理员证据</h3>
              <button type="button" onClick={props.onShowEvidence} disabled={props.evidenceLoading}>
                {props.evidenceLoading ? "正在读取..." : "查看完整证据"}
              </button>
            </div>
            {props.evidenceError && <p className="error" role="alert">{props.evidenceError}</p>}
            {props.evidence && <>
              {props.evidence.fields.length === 0 ? <p>此发行版本没有字段证据。</p> : <ul>{props.evidence.fields.map((item) => <li key={`${item.field}-${item.rule_id}`}><strong>{item.field}</strong><span>{item.value ?? "未选择值"} · {item.confidence} · {item.action}</span>{item.reason_code && <small>{item.reason_code}</small>}{item.candidates.length > 0 && <small>候选：{item.candidates.join("、")}</small>}</li>)}</ul>}
              {props.evidence.grouping && <div className="grouping-evidence"><strong>归组：{props.evidence.grouping.candidate_kind}</strong>{props.evidence.grouping.reason_codes.map((reason) => <small key={reason}>{reason}</small>)}{props.evidence.grouping.source_refs.length > 0 && <ul>{props.evidence.grouping.source_refs.map((sourceRef) => <li key={sourceRef}><code>{sourceRef}</code></li>)}</ul>}</div>}
              {props.evidence.truncated && <p>证据已按安全上限截断。</p>}
            </>}
          </section>
        )}
        <p className="readonly-hint">如需更正，请修改外部源文件或标签后重新扫描。ROOMusic 不会改写音乐源。</p>
      </div>
    </div>
  );
}
