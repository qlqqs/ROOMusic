import { Check, ChevronDown, Copyright, Flag, Pencil, X } from "lucide-react";
import { useId, useLayoutEffect, useRef, useState, type ReactNode, type Ref } from "react";
import { cx } from "../../../shared/lib/class-names";
import { useTooltip } from "../../../shared/ui";
import { MAX_BIO_CHARS, type ArtistBioDTO } from "../api/decoders";
import type { ArtistMediaControls } from "../hooks/use-artist-media";
import { safeUrl, sourceKindOf, sourceNameOf, sourceSummaryOf } from "./artist-media-source";
import { SourceGlyph } from "./source-glyph";
import { CitationRows } from "./citation-rows";
import { MediaPopover, PopoverActions, PopoverRow } from "./media-popover";
import styles from "./artist-bio.module.css";

function IconButton({
  label,
  onClick,
  children,
  tone,
  expanded,
  buttonRef,
}: {
  label: string;
  onClick: () => void;
  children: ReactNode;
  tone?: "danger" | "accent" | undefined;
  expanded?: boolean | undefined;
  buttonRef?: Ref<HTMLButtonElement> | undefined;
}) {
  const tip = useTooltip(label);
  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={cx(styles.iconButton, tone === "danger" && styles.danger, tone === "accent" && styles.accent)}
        aria-label={label}
        aria-expanded={expanded}
        onClick={onClick}
        {...tip.triggerProps}
      >
        {children}
      </button>
      {tip.tooltip}
    </>
  );
}

function BioEditor({ initial, saving, onSave, onCancel }: { initial: string; saving: boolean; onSave: (text: string) => void; onCancel: () => void }) {
  const [text, setText] = useState(initial);
  const id = useId();
  const length = Array.from(text.trim()).length;
  const valid = length > 0 && length <= MAX_BIO_CHARS;
  const save = () => {
    if (valid && !saving) onSave(text.trim());
  };
  return (
    <div className={styles.editor}>
      <textarea
        id={id}
        className={styles.textarea}
        value={text}
        aria-label="简介"
        aria-describedby={`${id}-count`}
        autoFocus
        onChange={(event) => setText(event.currentTarget.value)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            event.preventDefault();
            onCancel();
          } else if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
            event.preventDefault();
            save();
          }
        }}
      />
      <div className={styles.row}>
        <span id={`${id}-count`} className={styles.count} data-over={length > MAX_BIO_CHARS ? "" : undefined}>
          {length} / {MAX_BIO_CHARS}
        </span>
        <span className={styles.spacer} />
        <IconButton label="取消" onClick={onCancel}>
          <X size={15} aria-hidden="true" />
        </IconButton>
        <IconButton label={saving ? "正在保存" : "保存"} tone="accent" onClick={save}>
          <Check size={15} aria-hidden="true" />
        </IconButton>
      </div>
    </div>
  );
}

/**
 * 艺人页头下方的简介：正文最多 3 行折叠，下面一行只有图标——来源（点开是来源与许可）、展开；
 * 管理员悬停或聚焦时再浮出“编辑”“简介不对”，没有简介时只有一个“写简介”。编辑在原地进行。
 */
export function ArtistBio({ bio, media }: { bio: ArtistBioDTO | null; media: ArtistMediaControls | null }) {
  const textRef = useRef<HTMLParagraphElement>(null);
  const sourceRef = useRef<HTMLButtonElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);
  const [editing, setEditing] = useState(false);
  const [popover, setPopover] = useState(false);
  const shown = media?.hidden.bio ? null : bio;
  const text = shown?.text ?? null;

  // 折叠时内容高于显示高度才给“展开”；宽度变化（转屏、拖窗口）后重新判断。
  useLayoutEffect(() => {
    const element = textRef.current;
    if (element === null || expanded) return;
    const measure = () => setOverflowing(element.scrollHeight > element.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [text, expanded]);

  if (editing && media !== null) {
    return (
      <section className={styles.bio} aria-label="编辑艺人简介">
        <BioEditor
          initial={shown?.text ?? ""}
          saving={media.savingBio}
          onCancel={() => setEditing(false)}
          onSave={(value) => {
            void media.saveBio(value).then((ok) => {
              if (ok) setEditing(false);
            });
          }}
        />
      </section>
    );
  }
  if (shown === null) {
    if (media === null) return null;
    return (
      <section className={styles.bio} aria-label="艺人简介">
        <div className={styles.row}>
          <IconButton label="写简介" onClick={() => setEditing(true)}>
            <Pencil size={14} aria-hidden="true" />
          </IconButton>
        </div>
      </section>
    );
  }
  const summary = sourceSummaryOf("简介", shown);
  return (
    <section className={styles.bio} aria-label="艺人简介">
      <p ref={textRef} className={styles.text} lang={shown.lang === "zh" ? "zh-CN" : "en"} data-expanded={expanded ? "" : undefined}>
        {shown.text}
      </p>
      <div className={styles.row} data-admin={media === null ? undefined : ""}>
        <span className={styles.anchor}>
          <IconButton label={summary} buttonRef={sourceRef} expanded={popover} tone={sourceKindOf(shown.source) === "web" ? "accent" : undefined} onClick={() => setPopover((value) => !value)}>
            <SourceGlyph source={shown.source} size={14} />
          </IconButton>
          {popover ? (
            <MediaPopover label="简介来源" anchorRef={sourceRef} onClose={() => setPopover(false)} className={styles.popover}>
              <PopoverRow icon={<SourceGlyph source={shown.source} size={15} />} href={safeUrl(shown.source_url)}>
                {sourceNameOf(shown.source)}
              </PopoverRow>
              {shown.license === null ? null : (
                <PopoverRow icon={<Copyright size={15} aria-hidden="true" />} href={safeUrl(shown.license_url)}>
                  {shown.license}
                </PopoverRow>
              )}
              <CitationRows citations={shown.citations} />
              {media === null ? null : (
                <PopoverActions
                  start={
                    <IconButton
                      label="简介不对"
                      tone="danger"
                      onClick={() => {
                        setPopover(false);
                        media.reject("bio");
                      }}
                    >
                      <Flag size={15} aria-hidden="true" />
                    </IconButton>
                  }
                  end={
                    <IconButton
                      label="编辑简介"
                      onClick={() => {
                        setPopover(false);
                        setEditing(true);
                      }}
                    >
                      <Pencil size={15} aria-hidden="true" />
                    </IconButton>
                  }
                />
              )}
            </MediaPopover>
          ) : null}
        </span>
        {overflowing || expanded ? (
          <span className={styles.chevron} data-expanded={expanded ? "" : undefined}>
            <IconButton label={expanded ? "收起" : "展开"} expanded={expanded} onClick={() => setExpanded((value) => !value)}>
              <ChevronDown size={15} aria-hidden="true" />
            </IconButton>
          </span>
        ) : null}
        {media === null ? null : (
          <span className={styles.admin}>
            <IconButton label="编辑简介" onClick={() => setEditing(true)}>
              <Pencil size={14} aria-hidden="true" />
            </IconButton>
            <IconButton label="简介不对" tone="danger" onClick={() => media.reject("bio")}>
              <Flag size={14} aria-hidden="true" />
            </IconButton>
          </span>
        )}
      </div>
    </section>
  );
}
