import { Check, Link2, LoaderCircle, TriangleAlert, X } from "lucide-react";
import { useEffect, useId, useState, type ReactNode, type RefObject } from "react";
import { describeFailure, failureOf } from "../../../shared/api";
import { useTooltip } from "../../../shared/ui";
import type { ArtistLinkPreviewDTO, ArtistLinksDTO } from "../api/decoders";
import { previewArtistLink } from "../api/endpoints";
import { MediaPopover } from "./media-popover";
import styles from "./artist-link-popover.module.css";

const MUSICBRAINZ = /^(?:https?:\/\/(?:beta\.)?musicbrainz\.org\/artist\/)?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:[/?#].*)?$/i;
const WIKIDATA = /^(?:https?:\/\/(?:www\.|m\.)?wikidata\.org\/(?:wiki|entity)\/)?(Q[1-9][0-9]{0,11})(?:[/?#].*)?$/i;

/** 能识别的输入：MusicBrainz 艺人链接或 MBID、Wikidata 链接或 Q 号。识别不了时为 null。后端照同样的规则再校验一次。 */
export function linkInputOf(text: string): { source: "musicbrainz" | "wikidata"; id: string } | null {
  const value = text.trim();
  const mb = MUSICBRAINZ.exec(value);
  if (mb?.[1] !== undefined) return { source: "musicbrainz", id: mb[1].toLowerCase() };
  const wd = WIKIDATA.exec(value);
  if (wd?.[1] !== undefined) return { source: "wikidata", id: wd[1].toUpperCase() };
  return null;
}

const previewMessages: Readonly<Record<string, string>> = {
  not_found: "找不到这个条目。",
  invalid_input: "无法识别这个链接。",
  source_inactive: "这个来源没有启用，请先在“自动整理”的来源里打开。",
};

type Preview = { status: "idle" } | { status: "loading" } | { status: "ready"; preview: ArtistLinkPreviewDTO } | { status: "failed"; message: string };
type Result = { key: string; value: { status: "ready"; preview: ArtistLinkPreviewDTO } | { status: "failed"; message: string } };

function ConfirmButton({ preview, onApply }: { preview: ArtistLinkPreviewDTO; onApply: () => void }) {
  const label = preview.name_matches ? "对应到这位" : "名字不一致，仍然对应到这位";
  const tip = useTooltip(label);
  return (
    <>
      <button type="button" className={styles.confirm} aria-label={label} onClick={onApply} {...tip.triggerProps}>
        <Check size={15} aria-hidden="true" />
      </button>
      {tip.tooltip}
    </>
  );
}

/**
 * 指定对应：输入（或粘贴）MusicBrainz / Wikidata 链接，识别后先显示骨架再显示条目——名字、简介短语、条目号与名字是否一致——点勾确认。
 * 与当前对应相同的条目不再提交。
 */
export function ArtistLinkPopover({
  artistId,
  initial,
  links,
  anchorRef,
  className,
  onApply,
  onClose,
  footer,
}: {
  artistId: string;
  initial: string;
  links: ArtistLinksDTO;
  anchorRef: RefObject<HTMLElement | null>;
  className?: string | undefined;
  onApply: (preview: ArtistLinkPreviewDTO) => Promise<boolean>;
  onClose: () => void;
  /** 浮层底部的另一种找身份的办法（艺人页传入“联网查找”）。 */
  footer?: ReactNode | undefined;
}) {
  const [text, setText] = useState(initial);
  const [result, setResult] = useState<Result | null>(null);
  const [applying, setApplying] = useState(false);
  const inputId = useId();
  const parsed = linkInputOf(text);
  const parsedKey = parsed === null ? null : `${parsed.source}:${parsed.id}`;

  // 识别出的条目还没有结果时是“读取中”；结果属于别的条目时同样当作读取中。
  const preview: Preview = parsedKey === null ? { status: "idle" } : result?.key === parsedKey ? result.value : { status: "loading" };

  useEffect(() => {
    if (parsedKey === null) return;
    const key = parsedKey;
    const controller = new AbortController();
    // 输入停顿一下再查。
    const timer = window.setTimeout(() => {
      previewArtistLink(artistId, text.trim(), controller.signal)
        .then(({ data }) => setResult({ key, value: { status: "ready", preview: data } }))
        .catch((cause: unknown) => {
          const failure = failureOf(cause);
          if (failure?.kind === "aborted") return;
          setResult({ key, value: { status: "failed", message: failure === null ? "没能读取这个条目。" : describeFailure(failure, { messages: previewMessages }).message } });
        });
    }, 250);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
    // 只在识别出的条目变化时重新查；同一条目的不同写法不重复请求。
  }, [artistId, parsedKey]); // eslint-disable-line react-hooks/exhaustive-deps

  // 预览给出的每个条目号（MBID、Q 号）都与当前对应相同时才算“已是当前对应”。
  const current = preview.status === "ready" && (preview.preview.musicbrainz === null || links.musicbrainz === preview.preview.musicbrainz) && (preview.preview.wikidata === null || links.wikidata === preview.preview.wikidata);

  return (
    <MediaPopover label="指定对应" anchorRef={anchorRef} onClose={onClose} className={className}>
      <label className={styles.field} htmlFor={inputId} data-valid={parsed === null ? undefined : ""}>
        <Link2 size={15} aria-hidden="true" />
        <input
          id={inputId}
          className={styles.input}
          value={text}
          placeholder="MusicBrainz / Wikidata"
          aria-label="MusicBrainz 或 Wikidata 链接"
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => setText(event.currentTarget.value)}
        />
        {text === "" ? null : (
          <button type="button" className={styles.clear} aria-label="清空" onClick={() => setText("")}>
            <X size={14} aria-hidden="true" />
          </button>
        )}
      </label>
      {preview.status === "loading" ? (
        <div className={styles.candidate} aria-busy="true" aria-label="正在读取条目">
          <span className={styles.skeletonDisc} />
          <span className={styles.skeletonLines}>
            <span />
            <span />
          </span>
          <LoaderCircle className={styles.spin} size={15} aria-hidden="true" />
        </div>
      ) : null}
      {preview.status === "failed" ? (
        <p className={styles.message} role="alert">
          {preview.message}
        </p>
      ) : null}
      {preview.status === "ready" ? (
        <div className={styles.candidate}>
          <span className={styles.disc} aria-hidden="true">
            {Array.from(preview.preview.name)[0] ?? ""}
          </span>
          <span className={styles.body}>
            <span className={styles.name}>{preview.preview.name}</span>
            {preview.preview.description === null ? null : <span className={styles.description}>{preview.preview.description}</span>}
            <span className={styles.ids}>
              {[preview.preview.wikidata, preview.preview.musicbrainz?.slice(0, 8)].filter((id) => id !== null && id !== undefined).join(" · ")}
              {preview.preview.name_matches ? (
                <Check className={styles.match} size={12} aria-label="名字一致" />
              ) : (
                <TriangleAlert className={styles.mismatch} size={12} aria-label="名字与本地不一致" />
              )}
            </span>
          </span>
          {current ? (
            <Check className={styles.already} size={15} aria-label="已是当前对应" />
          ) : applying ? (
            <LoaderCircle className={styles.spin} size={15} aria-label="正在对应" />
          ) : (
            <ConfirmButton
              preview={preview.preview}
              onApply={() => {
                setApplying(true);
                void onApply(preview.preview).finally(() => setApplying(false));
              }}
            />
          )}
        </div>
      ) : null}
      {footer}
    </MediaPopover>
  );
}
