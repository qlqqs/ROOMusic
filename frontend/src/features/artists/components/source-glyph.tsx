import { BookOpen, Globe, Puzzle, UserRoundPen } from "lucide-react";
import { sourceKindOf, type ArtistMediaSource } from "./artist-media-source";

/** 来源的图标：有许可的外部来源是书本、插件是拼图、网络搜索是地球（与依据网页的外链同一个图标；星光留给管家）、管理员提供是人加笔。装饰性的，名字在旁边或在读屏名里。 */
export function SourceGlyph({ source, size }: { source: ArtistMediaSource; size: number }) {
  switch (sourceKindOf(source)) {
    case "licensed":
      return <BookOpen size={size} aria-hidden="true" />;
    case "plugin":
      return <Puzzle size={size} aria-hidden="true" />;
    case "web":
      return <Globe size={size} aria-hidden="true" />;
    case "manual":
      return <UserRoundPen size={size} aria-hidden="true" />;
  }
}
