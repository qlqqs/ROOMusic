import type { TrackDTO } from "../../../api";
import { toTrackRowModel } from "../model/display";

type TrackRowProps = {
  track: TrackDTO;
  onPlay: (track: TrackDTO) => void;
};

export function TrackRow({ track, onPlay }: TrackRowProps) {
  const model = toTrackRowModel(track);
  const detailParts = [model.artistLabel, model.factsLabel, model.creditsLabel]
    .filter((part): part is string => part !== null && part !== "");
  return (
    <li className="track-row">
      <button type="button" onClick={() => onPlay(track)} aria-label={`播放曲目 ${model.title}`}>
        <span className="track-position">{model.positionLabel}</span>
        <b title={model.title}>{model.title}</b>
        <small>{detailParts.join(" · ")}</small>
        <span className="track-duration">{model.durationLabel}</span>
        <i aria-hidden="true">▶</i>
      </button>
    </li>
  );
}
