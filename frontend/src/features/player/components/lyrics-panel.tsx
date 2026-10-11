import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { Button, QueryContent, VisuallyHidden } from "../../../shared/ui";
import type { PlainLyricsLine, SyncedLyricsLine } from "../api/decoders";
import { activeGroupIndex, followIndex, groupSyncedLines, type LyricsGroup } from "../engine/lyrics";
import { useTrackLyrics } from "../hooks/use-track-lyrics";
import { usePlayerEngine, usePlayerProgressSelector } from "../player-provider";
import styles from "./lyrics-panel.module.css";

/**
 * 正在播放页的歌词面板：数据是扫描时从音频标签提取、由后端解析的真实歌词，没有就如实说明，不生成占位文字。
 * 同步歌词随播放位置高亮当前组并跟随滚动，纯文本歌词静态显示；当前行变化不做读屏播报（没有 aria-live）。
 */
export function LyricsPanel({ trackId }: { trackId: string }) {
  const query = useTrackLyrics(trackId);
  const headingId = useId();
  return <section className={styles.panel} aria-labelledby={headingId}>
    <h2 id={headingId} className={styles.heading}><VisuallyHidden>歌词</VisuallyHidden></h2>
    <div className={styles.frame}>
      <QueryContent query={query} loading={<p className={styles.state}>正在加载歌词…</p>}>
        {(lyrics) => {
          if (lyrics === null) return <p className={styles.state}>这首曲目没有内嵌歌词</p>;
          // 按曲目重建：关闭跟随等界面状态不带到下一首。
          return lyrics.kind === "synced" ? <SyncedLyrics key={trackId} lines={lyrics.lines} /> : <PlainLyrics key={trackId} lines={lyrics.lines} />;
        }}
      </QueryContent>
    </div>
  </section>;
}

/** 纯文本歌词：空行分段，不高亮、不可点。可滚动区域本身可聚焦，键盘用户才能滚动阅读。 */
function PlainLyrics({ lines }: { lines: readonly PlainLyricsLine[] }) {
  const paragraphs = useMemo(() => {
    const result: string[][] = [];
    let paragraph: string[] = [];
    for (const line of lines) {
      if (line.text === "") {
        if (paragraph.length > 0) result.push(paragraph);
        paragraph = [];
      } else paragraph.push(line.text);
    }
    if (paragraph.length > 0) result.push(paragraph);
    return result;
  }, [lines]);
  return <>
    <div className={styles.scroller} role="group" aria-label="歌词文本" tabIndex={0}>
      <div className={styles.text}>
        {paragraphs.map((paragraph, index) => <p key={index} className={styles.paragraph}>{paragraph.map((text, lineIndex) => <span key={lineIndex} className={styles.plainLine}>{text}</span>)}</p>)}
      </div>
    </div>
    <div className={styles.footer}><p className={styles.note}>内嵌歌词 · 纯文本</p></div>
  </>;
}

// 在面板内用这些键滚动就是用户在自己浏览歌词：暂停跟随。
const scrollKeys = new Set(["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End"]);
const focusKeys = new Set(["ArrowUp", "ArrowDown", "Home", "End"]);

/** 把第 index 组滚到可视区中部；减少动态效果偏好下不做平滑滚动。 */
function centerGroup(scroller: HTMLElement | null, list: HTMLElement | null, index: number, smooth: boolean) {
  const item = list?.children[index];
  if (scroller === null || !(item instanceof HTMLElement)) return;
  scroller.scrollTo({ top: Math.max(0, item.offsetTop - (scroller.clientHeight - item.offsetHeight) / 2), behavior: smooth ? "smooth" : "auto" });
}

function SyncedLyrics({ lines }: { lines: readonly SyncedLyricsLine[] }) {
  const engine = usePlayerEngine();
  const groups = useMemo(() => groupSyncedLines(lines), [lines]);
  // 只订阅“当前组下标”与“可否定位”：约每秒 4 次的进度事件在组没变时不会重绘整份歌词。
  const active = usePlayerProgressSelector((progress) => activeGroupIndex(groups, progress.positionSeconds * 1000));
  const seekable = usePlayerProgressSelector((progress) => progress.seekable);
  const target = followIndex(groups, active);
  const [following, setFollowing] = useState(true);
  const scroller = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLOListElement>(null);
  const placed = useRef(false);

  useEffect(() => {
    if (!following || target < 0) return;
    // 第一次定位直接到位，之后的换行与“回到当前行”才平滑滚动。
    centerGroup(scroller.current, list.current, target, placed.current && !window.matchMedia("(prefers-reduced-motion: reduce)").matches);
    placed.current = true;
  }, [following, target]);

  // 只有用户自己的输入才暂停跟随；程序触发的滚动（上面的 centerGroup）不经过这些事件。
  // 各句的按钮只有一个在 Tab 顺序里（当前组，没有当前组时是第一句）；上下方向键、Home、End 在各句之间移动焦点。
  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (scrollKeys.has(event.key) || (event.key === " " && event.target === event.currentTarget)) setFollowing(false);
    if (!focusKeys.has(event.key)) return;
    const buttons = Array.from(event.currentTarget.querySelectorAll("button"));
    const from = buttons.findIndex((button) => button === document.activeElement);
    if (from < 0) return;
    event.preventDefault();
    const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : from + (event.key === "ArrowDown" ? 1 : -1);
    buttons[Math.min(Math.max(next, 0), buttons.length - 1)]?.focus();
  }
  function seekTo(group: LyricsGroup) {
    engine.seek(group.startMs / 1000);
    // 点一行说明用户要从这里听：恢复跟随。
    setFollowing(true);
  }

  const tabStop = target >= 0 ? target : groups.findIndex((group) => group.lines.length > 0);
  return <>
    <div ref={scroller} className={styles.scroller} role="group" aria-label="逐行歌词" {...(seekable ? {} : { tabIndex: 0 })}
      onWheel={() => setFollowing(false)} onTouchMove={() => setFollowing(false)} onKeyDown={onKeyDown}
      onPointerDown={(event) => { if (event.target === event.currentTarget) setFollowing(false); }}>
      <ol ref={list} role="list" className={styles.list}>
        {groups.map((group, index) => {
          if (group.lines.length === 0) return <li key={group.startMs} className={styles.gap} aria-hidden="true" />;
          const [primary, ...secondary] = group.lines;
          const content = <>
            <span className={styles.primary}>{primary}</span>
            {secondary.map((text, secondaryIndex) => <span key={secondaryIndex} className={styles.secondary}>{text}</span>)}
          </>;
          return <li key={group.startMs} className={styles.group} data-state={index === active ? "active" : index < active ? "past" : "upcoming"} aria-current={index === active ? "true" : undefined}>
            {seekable
              ? <button type="button" className={styles.line} tabIndex={index === tabStop ? 0 : -1} onClick={() => seekTo(group)}>{content}</button>
              : <div className={styles.line}>{content}</div>}
          </li>;
        })}
      </ol>
    </div>
    <div className={styles.footer}>
      <p className={styles.note}>{seekable ? "内嵌歌词 · 逐行同步 · 点任一行跳到该处" : "内嵌歌词 · 逐行同步"}</p>
      {following || target < 0 ? null : <Button variant="text" onClick={() => setFollowing(true)}>回到当前行</Button>}
    </div>
  </>;
}
