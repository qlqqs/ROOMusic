import { useNavigate } from "@tanstack/react-router";
import { FilterChip, FilterField, SortMenu, type SortOption } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { artistFilterRoles, artistRoleLabels, MAX_SEARCH_BYTES, parseSearchText, type ArtistRole } from "../../catalog";
import type { ArtistsSearch } from "../artist-search";
import styles from "./artist-filters.module.css";

type ArtistSort = "name" | "album_count";

const sortOptions: readonly SortOption<ArtistSort>[] = [
  { value: "name", label: "按名字" },
  { value: "album_count", label: "按专辑数" },
];

/** 超长的筛选词不静默丢弃：说明现状，不写进地址。 */
function validate(text: string): string | null {
  return utf8ByteLength(text.trim()) > MAX_SEARCH_BYTES ? "筛选词太长了，请短一些。" : null;
}

/**
 * 艺人列表的工具条：在艺人中筛选（边输入边出结果，一次输入过程只占一条历史，见 FilterField）、一排角色胶囊（单选，“全部”与各角色）与排序菜单；
 * 任何一项改变都回到第 1 页。地址里的 release_artist（只匹配发行艺人）没有单独的胶囊，显示在“专辑艺人”下。
 * 换成按专辑数排序时首字母不再适用（列表不分组），一起去掉。
 */
/** rail：手机上右侧有竖排的首字母索引时为真，工具条右边让出它的位置。 */
export function ArtistFilters({ search, rail = false }: { search: ArtistsSearch; rail?: boolean }) {
  const navigate = useNavigate();
  const go = (next: ArtistsSearch, replace = false) => void navigate({ to: "/artists", search: { ...next, page: undefined }, replace });
  const selected: ArtistRole | undefined = search.role === "release_artist" ? "album_artist" : search.role;
  return (
    <div role="search" aria-label="筛选艺人" className={styles.toolbar} data-rail={rail ? "" : undefined}>
      <div className={styles.filter}>
        <FilterField
          label="筛选艺人"
          placeholder="在艺人中筛选"
          value={search.q}
          normalize={parseSearchText}
          validate={validate}
          onCommit={(q, mode) => go({ ...search, q }, mode === "replace")}
        />
      </div>
      <div role="group" aria-label="角色" className={styles.chips}>
        <FilterChip pressed={selected === undefined} onClick={() => go({ ...search, role: undefined })}>
          全部
        </FilterChip>
        {artistFilterRoles.map((role) => (
          <FilterChip key={role} pressed={selected === role} onClick={() => go({ ...search, role })}>
            {artistRoleLabels[role]}
          </FilterChip>
        ))}
      </div>
      <span className={styles.sort}>
        <SortMenu
          options={sortOptions}
          value={search.sort ?? "name"}
          onChange={(value) => {
            const sort = value === "album_count" ? value : undefined;
            go({ ...search, sort, initial: sort === undefined ? search.initial : undefined });
          }}
        />
      </span>
    </div>
  );
}
