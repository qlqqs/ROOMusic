import { albumArtistOf } from "../catalog";
import type { HistoryItemDTO } from "./api/decoders";

/** 空态说明计次规则（概览与历史页共用）：用户才知道为什么刚听过的曲目还没出现。 */
export const PLAY_COUNT_RULE_TEXT = "还没有播放记录。曲目听过一半就会记在这里。";

/** 历史里曲目的显示标题：标题为空白时写“（无标题）”，与放进队列时的规则相同。 */
export function historyTitleOf(item: Pick<HistoryItemDTO, "title">): string {
  return item.title.trim() === "" ? "（无标题）" : item.title;
}

/** 历史里曲目的显示艺人：曲目艺人为空白时退回到专辑艺人（没有专辑艺人时用发行艺人），与放进队列时的规则相同。 */
export function historyArtistOf(item: Pick<HistoryItemDTO, "artist" | "release">): string {
  return item.artist.trim() === "" ? albumArtistOf(item.release) : item.artist;
}
