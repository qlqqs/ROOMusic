import type { MediumDTO, TrackDTO } from "../../../api";
import { TrackRow } from "./track-row";

type MediumSectionProps = {
  medium: MediumDTO;
  expanded: boolean;
  onToggle: (mediumID: string) => void;
  onPlayTrack: (track: TrackDTO) => void;
};

// Medium 使用 button + region disclosure；“全部展开/收起”只改本地展示。
export function MediumSection({ medium, expanded, onToggle, onPlayTrack }: MediumSectionProps) {
  const headingID = `medium-heading-${medium.id}`;
  const regionID = `medium-region-${medium.id}`;
  const label = `碟片 ${medium.position}${medium.title ? ` ${medium.title}` : ""}`;
  return (
    <div className="medium">
      <button
        type="button"
        className="medium-toggle"
        id={headingID}
        aria-expanded={expanded}
        aria-controls={regionID}
        onClick={() => onToggle(medium.id)}
      >
        <span aria-hidden="true">{expanded ? "▾" : "▸"}</span>
        {label}
        <small>{medium.tracks.length} 首</small>
      </button>
      <div role="region" id={regionID} aria-labelledby={headingID} hidden={!expanded}>
        <ol>
          {medium.tracks.map((track) => (
            <TrackRow key={track.id} track={track} onPlay={onPlayTrack} />
          ))}
        </ol>
      </div>
    </div>
  );
}
