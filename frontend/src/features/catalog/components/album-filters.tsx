import { useNavigate } from "@tanstack/react-router";
import { FilterChip, FilterField, SortMenu, type SortOption } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { MAX_SEARCH_BYTES, parseSearchText, type AlbumSearch } from "../album-search";
import styles from "./album-filters.module.css";

type AlbumSort = "title" | "added_at_desc";

const sortOptions: readonly SortOption<AlbumSort>[] = [
  { value: "title", label: "按标题" },
  { value: "added_at_desc", label: "最近新增" },
];

/** 超长的筛选词不静默丢弃：说明现状，不写进地址。 */
function validate(text: string): string | null {
  return utf8ByteLength(text.trim()) > MAX_SEARCH_BYTES ? "筛选词太长了，请短一些。" : null;
}

/**
 * 专辑列表的工具条：左边“全部 | 需要关注”两个单选胶囊（同艺人列表的角色胶囊），右边筛选与排序菜单。筛选边输入边出结果，一次输入过程只占一条历史（见 FilterField）；
 * 改任何一项都回到第 1 页，其余条件保留。输入框跟着地址走（后退、前进时回到地址里的值）。
 * 这里只筛当前列表，平时收成放大镜，不和顶栏的全库搜索（专辑、艺人、曲目一起）抢位置。
 */
export function AlbumFilters({ search }: { search: AlbumSearch }) {
  const navigate = useNavigate();
  const go = (next: AlbumSearch, replace = false) => void navigate({ to: "/albums", search: { ...next, page: undefined }, replace });
  return (
    <div role="search" aria-label="筛选专辑" className={styles.toolbar}>
      <div role="group" aria-label="范围" className={styles.chips}>
        <FilterChip pressed={search.attention === undefined} onClick={() => go({ ...search, attention: undefined })}>
          全部
        </FilterChip>
        {/* 再点一次回到全部，与“全部”效果相同。 */}
        <FilterChip pressed={search.attention === "required"} onClick={() => go({ ...search, attention: search.attention === "required" ? undefined : "required" })}>
          需要关注
        </FilterChip>
      </div>
      <div className={styles.end}>
        <FilterField
          label="筛选专辑"
          placeholder="在专辑中筛选"
          value={search.q}
          normalize={parseSearchText}
          validate={validate}
          onCommit={(q, mode) => go({ ...search, q }, mode === "replace")}
          collapsible
        />
        <SortMenu options={sortOptions} value={search.sort ?? "title"} onChange={(sort) => go({ ...search, sort: sort === "added_at_desc" ? sort : undefined })} />
      </div>
    </div>
  );
}
