import { useNavigate } from "@tanstack/react-router";
import { useRef, useState, type FormEvent } from "react";
import { Button, TextField } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { artistFilterRoles, artistRoleLabels, MAX_SEARCH_BYTES, parseSearchText, type ArtistRole } from "../../catalog";
import type { ArtistsSearch } from "../artist-search";
import styles from "./artist-filters.module.css";

/**
 * 艺人列表的筛选：搜索词显式提交（按钮或回车），角色与排序立即生效；任何一项改变都回到第 1 页。
 * 换成按专辑数排序时首字母不再适用（列表不分组），一起去掉。
 */
export function ArtistFilters({ search }: { search: ArtistsSearch }) {
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const [draft, setDraft] = useState(search.q ?? "");
  const [syncedQuery, setSyncedQuery] = useState(search.q);
  const [error, setError] = useState<string | null>(null);
  // 地址里的搜索词变了（后退、前进、清除）：输入框回到地址里的值。
  if (syncedQuery !== search.q) {
    setSyncedQuery(search.q);
    setDraft(search.q ?? "");
    setError(null);
  }
  const go = (next: ArtistsSearch) => void navigate({ to: "/artists", search: { ...next, page: undefined } });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (utf8ByteLength(draft.trim()) > MAX_SEARCH_BYTES) {
      setError("搜索词太长了，请短一些。");
      return;
    }
    setError(null);
    go({ ...search, q: parseSearchText(draft) });
  };

  return (
    <form role="search" aria-label="查找艺人" className={styles.form} onSubmit={submit}>
      <div className={styles.input}>
        <TextField
          ref={inputRef}
          label="查找艺人"
          name="q"
          type="text"
          inputMode="search"
          enterKeyHint="search"
          autoComplete="off"
          value={draft}
          error={error}
          onChange={(event) => setDraft(event.target.value)}
        />
      </div>
      <div className={styles.actions}>
        <Button type="submit" variant="primary">
          查找
        </Button>
        {search.q === undefined ? null : (
          <Button
            onClick={() => {
              setDraft("");
              setError(null);
              inputRef.current?.focus();
              go({ ...search, q: undefined });
            }}
          >
            清除
          </Button>
        )}
        <label className={styles.select}>
          <span>角色</span>
          {/* 地址里的 release_artist（只匹配发行艺人）没有单独的选项，显示在“专辑艺人”下。 */}
          <select value={search.role === "release_artist" ? "album_artist" : (search.role ?? "")} onChange={(event) => go({ ...search, role: artistFilterRoles.find((role) => role === event.target.value) })}>
            <option value="">全部</option>
            {artistFilterRoles.map((role: ArtistRole) => (
              <option key={role} value={role}>
                {artistRoleLabels[role]}
              </option>
            ))}
          </select>
        </label>
        <label className={styles.select}>
          <span>排序</span>
          <select
            value={search.sort ?? "name"}
            onChange={(event) => {
              const sort = event.target.value === "album_count" ? "album_count" : undefined;
              go({ ...search, sort, initial: sort === undefined ? search.initial : undefined });
            }}
          >
            <option value="name">按名字</option>
            <option value="album_count">按专辑数</option>
          </select>
        </label>
      </div>
    </form>
  );
}
