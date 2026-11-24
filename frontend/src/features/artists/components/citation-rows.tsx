import { Globe } from "lucide-react";
import type { ArtistCitationDTO } from "../api/decoders";
import { citationLabelOf, safeUrl } from "./artist-media-source";
import { PopoverRow } from "./media-popover";

/** 网络搜索依据的网页：每个一行外链（地球图标、主机名加路径，悬停是网页标题）；不是 http(s) 的不显示。 */
export function CitationRows({ citations }: { citations: readonly ArtistCitationDTO[] }) {
  return (
    <>
      {citations.map((citation) => {
        const label = citationLabelOf(citation.url);
        if (label === null) return null;
        return (
          <PopoverRow key={citation.url} icon={<Globe size={15} aria-hidden="true" />} href={safeUrl(citation.url)} title={citation.title === "" ? label : citation.title}>
            {label}
          </PopoverRow>
        );
      })}
    </>
  );
}
