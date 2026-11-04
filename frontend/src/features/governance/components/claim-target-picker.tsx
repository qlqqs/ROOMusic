import { useState, type FormEvent } from "react";
import { Button, FailureNotice, Skeleton, TextField } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { albumArtistOf, albumMeta, Cover, MAX_SEARCH_BYTES, parseSearchText, useAlbum, useAlbumSearchResults, type ReleaseGroupSummaryDTO, type ReleaseSummaryDTO } from "../../catalog";
import type { ClaimSuggestionDTO } from "../api/decoders";
import { suggestionReasonText } from "../claim-text";
import styles from "./claim-target-picker.module.css";

type PickerProps = {
  suggestions: readonly ClaimSuggestionDTO[];
  /** 选中的新版本（地址里的 target）；没有时为 undefined。 */
  selectedId: string | undefined;
  onSelect: (releaseId: string) => void;
};

/** 单选项：封面、标题与说明；整块是一个 label，点哪里都选中。 */
function Option({ name, checked, onChange, release, extra }: { name: string; checked: boolean; onChange: () => void; release: ReleaseSummaryDTO; extra: string }) {
  return (
    <label className={styles.option}>
      <input type="radio" name={name} checked={checked} onChange={onChange} />
      <span className={styles.cover}>
        <Cover artwork={release.artwork} title={release.title} variant="card" />
      </span>
      <span className={styles.text}>
        <span className={styles.optionTitle}>{release.title}</span>
        <span className={styles.optionMeta}>
          {albumArtistOf(release)} · {albumMeta(release)}
          {extra === "" ? "" : ` · ${extra}`}
        </span>
      </span>
    </label>
  );
}

/** 推荐的版本：按旧版本最后的文件路径推荐，每项写明理由。 */
function Suggestions({ suggestions, selectedId, onSelect }: PickerProps) {
  if (suggestions.length === 0) return <p className={styles.hint}>没有找到文件或目录相同的版本，可以在下面搜索。</p>;
  return (
    <fieldset className={styles.group}>
      <legend className={styles.legend}>推荐的版本</legend>
      <ul className={styles.list}>
        {suggestions.map((suggestion) => (
          <li key={suggestion.release.id}>
            <Option
              name="claim-target"
              checked={selectedId === suggestion.release.id}
              onChange={() => onSelect(suggestion.release.id)}
              release={suggestion.release}
              extra={[suggestion.edition, suggestionReasonText(suggestion)].filter((part) => part !== null && part !== "").join(" · ")}
            />
          </li>
        ))}
      </ul>
    </fieldset>
  );
}

/** 专辑里的版本：专辑只有一个版本时不用再选，直接选中它；有多个版本时让管理员选其中一个。 */
function GroupVersions({ group, selectedId, onSelect }: { group: ReleaseGroupSummaryDTO; selectedId: string | undefined; onSelect: (releaseId: string) => void }) {
  const detail = useAlbum(group.id);
  if (detail.status === "error") return <FailureNotice error={detail.error} action={<Button onClick={() => void detail.refetch()}>重试</Button>} />;
  if (detail.data === undefined) return <Skeleton label="正在读取这张专辑的版本" lines={2} />;
  return (
    <fieldset className={styles.group}>
      <legend className={styles.legend}>选择《{group.representative.title}》的版本</legend>
      <ul className={styles.list}>
        {detail.data.releases.map((release) => (
          <li key={release.id}>
            <Option name="claim-target-version" checked={selectedId === release.id} onChange={() => onSelect(release.id)} release={release} extra="" />
          </li>
        ))}
      </ul>
    </fieldset>
  );
}

/** 搜索结果：只给最相关的前几张；选中一张专辑后再选它的版本。 */
function SearchResults({ query, selectedId, onSelect }: { query: string; selectedId: string | undefined; onSelect: (releaseId: string) => void }) {
  const results = useAlbumSearchResults(query);
  const [groupId, setGroupId] = useState<string | null>(null);
  if (results.data === undefined) {
    return results.isError ? <FailureNotice error={results.error} action={<Button onClick={() => void results.refetch()}>重试</Button>} /> : <Skeleton label="正在搜索专辑" lines={3} />;
  }
  const groups = results.data.items;
  const more = results.data.pagination.total > groups.length;
  if (groups.length === 0) return <p className={styles.hint}>没有找到匹配的专辑。可以换个搜索词再试。</p>;
  const chosen = groups.find((group) => group.id === groupId) ?? null;
  return (
    <>
      <fieldset className={styles.group}>
        <legend className={styles.legend}>选择专辑</legend>
        <ul className={styles.list}>
          {groups.map((group) => (
            <li key={group.id}>
              <Option
                name="claim-search-album"
                checked={groupId === group.id}
                onChange={() => {
                  setGroupId(group.id);
                  // 专辑只有一个版本：不用再选，直接选中它。
                  if (group.release_count === 1) onSelect(group.representative.id);
                }}
                release={group.representative}
                extra={group.release_count > 1 ? `${group.release_count} 个版本` : ""}
              />
            </li>
          ))}
        </ul>
        {more ? <p className={styles.hint}>只显示最相关的 {groups.length} 张专辑，找不到可以换个更准确的词。</p> : null}
      </fieldset>
      {chosen !== null && chosen.release_count > 1 ? <GroupVersions group={chosen} selectedId={selectedId} onSelect={onSelect} /> : null}
    </>
  );
}

/**
 * 选择接替旧版本的新版本：先是按文件路径与目录推荐的版本（单选），然后是“搜索其他版本”——按标题、艺人或曲目名搜专辑（与“归入其他专辑”同一个搜索），
 * 选中专辑后再选它的版本（专辑只有一个版本就直接选中）。别的目录、别的扫描目录里的版本都能这样选到。选择写进地址，由调用方持有。
 */
export function ClaimTargetPicker({ suggestions, selectedId, onSelect }: PickerProps) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState<string | undefined>(undefined);
  const [searchError, setSearchError] = useState<string | null>(null);

  const search = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const bytes = utf8ByteLength(draft.trim());
    if (bytes > MAX_SEARCH_BYTES) {
      setSearchError("搜索词太长了，请短一些。");
      return;
    }
    const text = parseSearchText(draft);
    if (text === undefined) {
      setSearchError("请输入标题、艺人或曲目名。");
      return;
    }
    setSearchError(null);
    setQuery(text);
  };

  return (
    <div className={styles.picker}>
      <Suggestions suggestions={suggestions} selectedId={selectedId} onSelect={onSelect} />
      <form role="search" aria-label="搜索其他版本" className={styles.search} onSubmit={search}>
        <div className={styles.searchRow}>
          <div className={styles.searchField}>
            <TextField
              label="搜索其他版本"
              description="按标题、艺人或曲目名查找。"
              type="text"
              inputMode="search"
              enterKeyHint="search"
              autoComplete="off"
              value={draft}
              error={searchError}
              onChange={(event) => setDraft(event.target.value)}
            />
          </div>
          <div className={styles.searchButton}>
            <Button type="submit">搜索</Button>
          </div>
        </div>
      </form>
      {query === undefined ? null : <SearchResults key={query} query={query} selectedId={selectedId} onSelect={onSelect} />}
    </div>
  );
}
