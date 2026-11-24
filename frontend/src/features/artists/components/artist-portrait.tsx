import { Copyright, Flag, ImagePlus, Link2, User } from "lucide-react";
import { useEffect, useRef, useState, type DragEvent, type ReactNode } from "react";
import { cx } from "../../../shared/lib/class-names";
import { useTooltip } from "../../../shared/ui";
import type { ArtistImageDTO, ArtistLinksDTO } from "../api/decoders";
import type { ArtistMediaControls } from "../hooks/use-artist-media";
import { safeUrl, sourceKindOf, sourceNameOf, sourceSummaryOf } from "./artist-media-source";
import { SourceGlyph } from "./source-glyph";
import { ArtistLinkPopover, linkInputOf } from "./artist-link-popover";
import { ArtistMonogram } from "./artist-monogram";
import { MediaPopover, PopoverActions, PopoverRow } from "./media-popover";
import styles from "./artist-portrait.module.css";

type ArtistPortraitProps = {
  artist: { id: string; name: string; image: string | null };
  image: ArtistImageDTO | null;
  links: ArtistLinksDTO;
  /** 管理员的写入；普通成员为 null，只看得到图片与来源。 */
  media: ArtistMediaControls | null;
};

function IconButton({ label, onClick, icon, tone }: { label: string; onClick: () => void; icon: ReactNode; tone?: "danger" }) {
  const tip = useTooltip(label);
  return (
    <>
      <button type="button" className={cx(styles.iconButton, tone === "danger" && styles.danger)} aria-label={label} onClick={onClick} {...tip.triggerProps}>
        {icon}
      </button>
      {tip.tooltip}
    </>
  );
}

/** 艺人当前的外部对应：MusicBrainz 与 Wikidata 各一行外链。 */
function LinkRows({ links }: { links: ArtistLinksDTO }) {
  return (
    <>
      {links.musicbrainz === null ? null : (
        <PopoverRow icon={<Link2 size={15} aria-hidden="true" />} href={`https://musicbrainz.org/artist/${links.musicbrainz}`}>
          MusicBrainz
        </PopoverRow>
      )}
      {links.wikidata === null ? null : (
        <PopoverRow icon={<Link2 size={15} aria-hidden="true" />} href={`https://www.wikidata.org/wiki/${links.wikidata}`}>
          Wikidata {links.wikidata}
        </PopoverRow>
      )}
    </>
  );
}

/**
 * 艺人页的大圆章与来源印章：
 * - 有图片时显示图片（读不出来时退回名字圆章），右下 45° 处一枚印章，图标表示来源；点开是来源、作者、许可与外部对应。
 * - 管理员：悬停圆章出现遮罩与“加图片”图标，点击选图；把图片拖到圆章上也能上传；上传中先显示本地预览与进度弧。
 *   印章弹层底部有“图片不对”“指定对应”“更换图片”；没有图片时印章是“指定对应”。在页面任意处粘贴 MusicBrainz 或 Wikidata 链接直接打开指定对应。
 */
export function ArtistPortrait({ artist, image, links, media }: ArtistPortraitProps) {
  const [popover, setPopover] = useState<null | "source" | "link">(null);
  const [pastedLink, setPastedLink] = useState("");
  const [dropping, setDropping] = useState(false);
  const sealRef = useRef<HTMLButtonElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const shown = media?.hidden.image ? null : image;
  const upload = media?.upload ?? { status: "idle" as const };
  const resource = upload.status === "uploading" ? null : (shown?.resource ?? (media?.hidden.image ? null : artist.image));
  const sealLabel = shown === null ? "指定对应" : sourceSummaryOf("图片", shown);
  const sealTip = useTooltip(popover === null ? sealLabel : undefined);
  const veilTip = useTooltip(shown === null ? "上传图片" : "更换图片");

  // 管理员在页面任意处粘贴可识别的链接：打开指定对应并带上它（输入框、文本框里的粘贴照常）。
  useEffect(() => {
    if (media === null) return;
    const onPaste = (event: ClipboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.closest("input, textarea, [contenteditable='true']")) return;
      const text = event.clipboardData?.getData("text").trim() ?? "";
      if (linkInputOf(text) === null) return;
      event.preventDefault();
      setPastedLink(text);
      setPopover("link");
    };
    document.addEventListener("paste", onPaste);
    return () => document.removeEventListener("paste", onPaste);
  }, [media]);

  const chooseFile = () => {
    setPopover(null);
    fileRef.current?.click();
  };
  const onDrag = (event: DragEvent) => {
    if (media === null || !event.dataTransfer.types.includes("Files")) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setDropping(true);
  };
  const onDrop = (event: DragEvent) => {
    if (media === null) return;
    event.preventDefault();
    setDropping(false);
    const file = event.dataTransfer.files[0];
    if (file !== undefined) media.uploadImage(file);
  };

  const showSeal = shown !== null || media !== null;
  const closePopover = () => setPopover(null);

  return (
    <div
      className={styles.portrait}
      data-drop={dropping ? "" : undefined}
      onDragEnter={onDrag}
      onDragOver={onDrag}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDropping(false);
      }}
      onDrop={onDrop}
    >
      <div className={styles.disc}>
        {upload.status === "uploading" ? (
          <span className={styles.preview} aria-hidden="true">
            <img src={upload.preview} alt="" />
          </span>
        ) : (
          <ArtistMonogram id={artist.id} name={artist.name} image={resource} size="large" />
        )}
        {media === null ? null : (
          <>
            <button type="button" className={styles.veil} aria-label={shown === null ? "上传图片" : "更换图片"} onClick={chooseFile} {...veilTip.triggerProps}>
              <ImagePlus size={26} aria-hidden="true" />
            </button>
            {veilTip.tooltip}
            <input
              ref={fileRef}
              className={styles.file}
              type="file"
              accept="image/jpeg,image/png,image/webp"
              tabIndex={-1}
              aria-hidden="true"
              onChange={(event) => {
                const file = event.currentTarget.files?.[0];
                event.currentTarget.value = "";
                if (file !== undefined) media.uploadImage(file);
              }}
            />
          </>
        )}
      </div>
      {upload.status === "uploading" ? (
        <svg className={styles.ring} data-indeterminate={upload.progress === null ? "" : undefined} viewBox="0 0 100 100" role="progressbar" aria-label="正在上传图片" aria-valuemin={0} aria-valuemax={100} {...(upload.progress === null ? {} : { "aria-valuenow": Math.round(upload.progress * 100) })}>
          <circle className={styles.track} cx="50" cy="50" r="48" pathLength={100} />
          <circle className={styles.arc} cx="50" cy="50" r="48" pathLength={100} style={{ strokeDashoffset: upload.progress === null ? undefined : 100 - upload.progress * 100 }} />
        </svg>
      ) : null}
      {showSeal ? (
        <>
          <button
            ref={sealRef}
            type="button"
            className={styles.seal}
            data-kind={shown === null ? "link" : sourceKindOf(shown.source)}
            aria-label={sealLabel}
            aria-expanded={popover !== null}
            aria-haspopup="dialog"
            onClick={() => setPopover((current) => (current === null ? (shown === null ? "link" : "source") : null))}
            {...sealTip.triggerProps}
          >
            {shown === null ? <Link2 size={15} aria-hidden="true" /> : <SourceGlyph source={shown.source} size={15} />}
          </button>
          {sealTip.tooltip}
        </>
      ) : null}
      {popover === "source" && shown !== null ? (
        <MediaPopover label="图片来源" anchorRef={sealRef} onClose={closePopover} className={styles.popover}>
          <PopoverRow icon={<SourceGlyph source={shown.source} size={15} />} href={safeUrl(shown.source_url)}>
            {sourceNameOf(shown.source)}
          </PopoverRow>
          {shown.author === null ? null : <PopoverRow icon={<User size={15} aria-hidden="true" />}>{shown.author}</PopoverRow>}
          {shown.license === null ? null : (
            <PopoverRow icon={<Copyright size={15} aria-hidden="true" />} href={safeUrl(shown.license_url)}>
              {shown.license}
            </PopoverRow>
          )}
          <LinkRows links={links} />
          {media === null ? null : (
            <PopoverActions
              start={
                <>
                  <IconButton
                    label="图片不对"
                    tone="danger"
                    icon={<Flag size={15} aria-hidden="true" />}
                    onClick={() => {
                      setPopover(null);
                      media.reject("image");
                    }}
                  />
                  <IconButton label="指定对应" icon={<Link2 size={15} aria-hidden="true" />} onClick={() => setPopover("link")} />
                </>
              }
              end={<IconButton label="更换图片" icon={<ImagePlus size={15} aria-hidden="true" />} onClick={chooseFile} />}
            />
          )}
        </MediaPopover>
      ) : null}
      {popover === "link" && media !== null ? (
        <ArtistLinkPopover
          key={pastedLink}
          artistId={artist.id}
          initial={pastedLink}
          links={links}
          anchorRef={sealRef}
          className={styles.popover}
          onApply={async (preview) => {
            const ok = await media.applyLink(preview);
            if (ok) setPopover(null);
            return ok;
          }}
          onClose={() => {
            setPopover(null);
            setPastedLink("");
          }}
        />
      ) : null}
    </div>
  );
}
