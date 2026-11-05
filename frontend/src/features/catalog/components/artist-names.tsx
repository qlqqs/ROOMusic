import { Link } from "@tanstack/react-router";
import { createContext, Fragment, useContext, type ReactNode } from "react";
import type { ArtistRefDTO } from "../api/decoders";
import { useCatalogState } from "../hooks/use-catalog-state";

// 艺人名能不能做成链接：只有艺人数据已经就绪（artists_state=ready）才行。整理中、状态还没读到时一律是纯文字，
// 不加任何说明；就绪后各处同时变成链接。整页只读一次状态（外壳里的 ArtistLinksScope），每一行只读 context，万首曲目的版本也不为每行订阅查询。
const ArtistLinksContext = createContext(false);

/** 外壳放一次：读目录状态，告诉范围里的艺人名能不能做成链接。 */
export function ArtistLinksScope({ children }: { children: ReactNode }) {
  const state = useCatalogState();
  return <ArtistLinksContext value={state.data?.artists_state === "ready"}>{children}</ArtistLinksContext>;
}

/** 艺人数据是否已经就绪（艺人名可以做成链接）。 */
export function useArtistLinksReady(): boolean {
  return useContext(ArtistLinksContext);
}

type ArtistNamesProps = {
  /** 显示的署名文字（曲目艺人、专辑艺人、一条署名）。 */
  text: string;
  /** 这处署名对应的艺人；空数组（整理中、推断的占位）时显示纯文字。 */
  refs: readonly ArtistRefDTO[];
};

/**
 * 艺人名：就绪且有对应艺人时，每位艺人是一个链接（文字用这处署名的原写法，多位之间用“ / ”，与显示文字的拼法一致），
 * 否则是原样的文字。不用于专辑卡片：卡片整张是链接，里面不能再嵌套链接。
 */
export function ArtistNames({ text, refs }: ArtistNamesProps) {
  const linked = useArtistLinksReady();
  if (!linked || refs.length === 0) return <>{text}</>;
  return (
    <>
      {refs.map((ref, index) => (
        <Fragment key={`${ref.artist_id}:${index}`}>
          {index === 0 ? null : " / "}
          <Link to="/artists/$artistId" params={{ artistId: ref.artist_id }}>
            {ref.name}
          </Link>
        </Fragment>
      ))}
    </>
  );
}
