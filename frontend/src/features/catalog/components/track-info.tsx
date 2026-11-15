import { X } from "lucide-react";
import { Fragment, type ReactNode, type Ref } from "react";
import { formatDuration } from "../../../shared/lib/duration";
import { dsdRateName } from "../../../shared/lib/dsd";
import { Button, Panel } from "../../../shared/ui";
import type { ReleaseCreditDTO, ReleaseTrackDTO } from "../api/decoders";
import { groupTrackCredits, ownTrackCredits } from "../track-credits";
import { ArtistNames } from "./artist-names";
import styles from "./track-info.module.css";

/** 选中曲目在版本里的位置：多张介质时写第几张。 */
export type TrackPlace = { disc: number | null; number: number };

type TrackInfoProps = {
  track: ReleaseTrackDTO;
  place: TrackPlace;
  /** 专辑艺人：曲目艺人与它相同时不写“艺人”一行。 */
  albumArtist: string;
  /** 页头已经列过的共同署名（组的 key）：这里只写本曲独有的。 */
  sharedCredits: ReadonlySet<string>;
  /** 响度等需要别的模块的项：由 app 经插槽给出，渲染成 <dt>/<dd>。 */
  renderExtraFacts?: ((trackId: string) => ReactNode) | undefined;
  headingRef: Ref<HTMLHeadingElement>;
  onClose: () => void;
};

const channelNames = new Map([
  [1, "单声道"],
  [2, "立体声"],
]);

/** 格式：“FLAC 24/96 · 立体声”；DSD 写“DSF DSD64”；缺的部分不写，全都没有时返回 null。 */
function formatText(track: ReleaseTrackDTO): string | null {
  const codec = track.codec?.toUpperCase() ?? null;
  const dsd = track.sample_rate === null ? null : dsdRateName(track.sample_rate);
  const rate = track.sample_rate === null || dsd !== null ? null : `${track.sample_rate / 1000}`;
  const resolution = dsd ?? (rate === null ? null : track.bit_depth === null ? `${rate} kHz` : `${track.bit_depth}/${rate}`);
  const channels = track.channels === null ? null : (channelNames.get(track.channels) ?? `${track.channels} 声道`);
  const head = [codec, resolution].filter((part) => part !== null).join(" ");
  const text = [head === "" ? null : head, channels].filter((part) => part !== null).join(" · ");
  return text === "" ? null : text;
}

/** 本曲要列的署名：去掉页头已列的共同署名，以及名字已写在曲目艺人里的演奏者。 */
function trackCredits(track: ReleaseTrackDTO, shared: ReadonlySet<string>): readonly ReleaseCreditDTO[] {
  const artistNames = new Set(track.artist_refs.map((ref) => ref.name));
  return ownTrackCredits(track.credits, shared).filter((credit) => credit.role !== "performer" || !(artistNames.has(credit.name) || track.artist.includes(credit.name)));
}

/**
 * 专辑页右栏的“曲目信息”：单击曲目行（或菜单里的“曲目信息”）时出现。标题、第几张第几首与时长，下面是艺人（与专辑艺人不同时）、
 * 本曲独有的署名、格式与响度，有数据才写。右上角“关闭”取消选中。
 */
export function TrackInfo({ track, place, albumArtist, sharedCredits, renderExtraFacts, headingRef, onClose }: TrackInfoProps) {
  const title = track.title.trim() === "" ? "（无标题）" : track.title;
  const artist = track.artist.trim();
  const credits = groupTrackCredits(trackCredits(track, sharedCredits));
  const format = formatText(track);
  const placeText = [place.disc === null ? null : `第 ${place.disc} 张`, `第 ${place.number} 首`, track.duration_seconds === null ? null : formatDuration(track.duration_seconds)].filter((part) => part !== null).join(" · ");
  return (
    <Panel title="曲目信息" headingRef={headingRef} action={<Button variant="icon" icon={<X size={16} />} label="关闭曲目信息" onClick={onClose} />}>
      <div className={styles.lead}>
        <p className={styles.title}>{title}</p>
        <p className={styles.place}>{placeText}</p>
      </div>
      <dl className={styles.facts}>
        {artist === "" || artist === albumArtist ? null : (
          <>
            <dt>艺人</dt>
            <dd>
              <ArtistNames text={artist} refs={track.artist_refs} />
            </dd>
          </>
        )}
        {credits.map((group) => (
          <Fragment key={group.key}>
            <dt>{group.label}</dt>
            <dd>
              {group.entries.map((credit, index) => (
                <Fragment key={`${credit.name}:${index}`}>
                  {index === 0 ? null : "、"}
                  <ArtistNames text={credit.name} refs={credit.artist_refs} />
                </Fragment>
              ))}
            </dd>
          </Fragment>
        ))}
        {format === null ? null : (
          <>
            <dt>格式</dt>
            <dd>{format}</dd>
          </>
        )}
        {renderExtraFacts?.(track.id)}
      </dl>
    </Panel>
  );
}
