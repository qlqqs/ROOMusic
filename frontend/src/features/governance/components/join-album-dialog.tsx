import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { isHttpFailure } from "../../../shared/api";
import { Button, FailureNotice, ModalDialog, Notice, Skeleton, TextField } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { albumArtistOf, albumMeta, Cover, MAX_SEARCH_BYTES, parseSearchText, useAlbumSearchResults, type ReleaseGroupSummaryDTO } from "../../catalog";
import type { ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { governanceFailureMessages } from "./governance-messages";
import styles from "./join-album-dialog.module.css";

/** 要移动的版本：revision 是页面读到的人工修改版本号，提交时作为预期版本。 */
type MovingRelease = ReleaseTarget & { revision: number };

type JoinAlbumDialogProps = {
  open: boolean;
  release: MovingRelease;
  /** 版本现在所在的组：搜索结果里不列它。 */
  currentGroupId: string;
  commands: ReleaseCommands;
  onClose: () => void;
};

function versionsText(group: ReleaseGroupSummaryDTO): string {
  return group.release_count > 1 ? ` · ${group.release_count} 个版本` : "";
}

type ResultsProps = {
  query: string;
  currentGroupId: string;
  selectedId: string | null;
  disabled: boolean;
  onSelect: (group: ReleaseGroupSummaryDTO) => void;
};

/** 搜索结果：不含版本现在所在的组。只给最相关的前几张，结果更多时提示输入更精确的搜索词。 */
function SearchResults({ query, currentGroupId, selectedId, disabled, onSelect }: ResultsProps) {
  const results = useAlbumSearchResults(query);
  if (results.data === undefined) {
    return results.isError ? (
      <FailureNotice error={results.error} action={<Button onClick={() => void results.refetch()}>重试</Button>} />
    ) : (
      <Skeleton label="正在搜索专辑" lines={3} />
    );
  }
  const groups = results.data.items.filter((group) => group.id !== currentGroupId);
  const more = results.data.pagination.total > results.data.items.length;
  if (groups.length === 0) {
    return <p className={styles.hint}>{more ? "前几张匹配的专辑里没有可以归入的，请输入更精确的搜索词。" : "没有找到可以归入的专辑。可以换个搜索词再试。"}</p>;
  }
  return (
    <fieldset className={styles.results} disabled={disabled}>
      <legend className={styles.legend}>选择要归入的专辑</legend>
      <ul className={styles.list}>
        {groups.map((group) => (
          <li key={group.id}>
            <label className={styles.option}>
              <input type="radio" name="join-target" checked={selectedId === group.id} onChange={() => onSelect(group)} />
              <span className={styles.cover}>
                <Cover artwork={group.representative.artwork} title={group.representative.title} variant="card" />
              </span>
              <span className={styles.text}>
                <span className={styles.optionTitle}>{group.representative.title}</span>{" "}
                <span className={styles.optionMeta}>
                  {albumArtistOf(group.representative)} · {albumMeta(group.representative)}
                  {versionsText(group)}
                </span>
              </span>
            </label>
          </li>
        ))}
      </ul>
      {more ? <p className={styles.hint}>只显示最相关的 {results.data.items.length} 张专辑；找不到的话，请输入更精确的搜索词。</p> : null}
    </fieldset>
  );
}

type FormProps = {
  titleId: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
  release: MovingRelease;
  currentGroupId: string;
  commands: ReleaseCommands;
  onClose: () => void;
};

function JoinAlbumForm({ titleId, headingRef, release, currentGroupId, commands, onClose }: FormProps) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState<string | undefined>(undefined);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // 选中的专辑从当前结果里取：搜索词变了、结果刷新后它不在了，就等于没有选中（不会提交一个看不见的专辑）。
  const results = useAlbumSearchResults(query);
  const selected = results.data?.items.find((group) => group.id === selectedId && group.id !== currentGroupId) ?? null;

  const commandState = commands.state;
  const pending = commandState.status === "pending" && commandState.command.kind === "join";
  const failed = commandState.status === "failed" && commandState.command.kind === "join" && commandState.command.release.id === release.id ? commandState : null;

  const search = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const bytes = utf8ByteLength(draft.trim());
    if (bytes > MAX_SEARCH_BYTES) {
      setSearchError(`搜索词最多 ${MAX_SEARCH_BYTES} 个字节（约 ${Math.floor(MAX_SEARCH_BYTES / 3)} 个汉字），现在是 ${bytes} 个字节。`);
      return;
    }
    const text = parseSearchText(draft);
    if (text === undefined) {
      setSearchError("请输入标题、艺人或曲目名。");
      return;
    }
    setSearchError(null);
    setQuery(text);
    setSelectedId(null);
    commands.dismiss();
  };

  const confirm = () => {
    // 成功之后页面已经带到新的专辑（命令 hook 的 onMoved），这里只关闭对话框。
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    if (selected === null) return;
    commands.submit({ kind: "join", release, revision: release.revision, group: { id: selected.id, title: selected.representative.title } }, { onSucceeded: onClose });
  };

  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        把这个版本归入其他专辑
      </h2>
      <p className={styles.lead}>
        《{release.title}》会和你选择的专辑放在一起，作为同一张专辑的版本显示。不会改动音乐文件，之后可以撤销。
      </p>
      <form role="search" aria-label="搜索要归入的专辑" className={styles.search} onSubmit={search}>
        <div className={styles.searchRow}>
          <div className={styles.searchField}>
            <TextField
              label="搜索专辑"
              description="按标题、艺人或曲目名查找。"
              type="text"
              inputMode="search"
              enterKeyHint="search"
              autoComplete="off"
              value={draft}
              error={searchError}
              disabled={pending}
              onChange={(event) => setDraft(event.target.value)}
            />
          </div>
          <div className={styles.searchButton}>
            <Button type="submit" disabled={pending}>
              搜索
            </Button>
          </div>
        </div>
      </form>
      {query === undefined ? <p className={styles.hint}>输入标题或艺人，找到要归入的专辑。</p> : <SearchResults query={query} currentGroupId={currentGroupId} selectedId={selected?.id ?? null} disabled={pending} onSelect={(group) => setSelectedId(group.id)} />}
      <div className={styles.footer}>
        {selected === null ? null : (
          <p className={styles.preview} role="status">
            归入后，这个版本会和《{selected.representative.title}》的 {selected.release_count} 个版本放在一起，同属一张专辑。
          </p>
        )}
        {failed === null ? null : failed.uncertain ? (
          <FailureNotice error={failed.error} context="write" messages={governanceFailureMessages} title="结果未确认" />
        ) : isHttpFailure(failed.failure, 409, "revision_conflict") ? (
          <Notice tone="warning" title="这个版本刚被修改过">
            页面已刷新。请再确认一次要归入的专辑。
          </Notice>
        ) : (
          <FailureNotice error={failed.error} context="write" messages={governanceFailureMessages} title="没有归入" />
        )}
        <div className={styles.actions}>
          <Button
            onClick={() => {
              commands.dismiss();
              onClose();
            }}
            disabled={pending}
          >
            取消
          </Button>
          <Button variant="primary" busy={pending} disabled={selected === null && !failed?.uncertain} onClick={confirm}>
            {pending ? "正在归入…" : failed?.uncertain ? "重试归入" : "归入这张专辑"}
          </Button>
        </div>
      </div>
    </div>
  );
}

/**
 * 把这个版本归入其他专辑（管理员）：按标题、艺人或曲目名搜索，选中后预览会和哪张专辑放在一起，确认后提交。
 * 搜索结果不含这个版本现在所在的专辑；409 与目标专辑已不存在时对话框保持打开并说明；结果不确定时“归入”变成用同一个幂等键“重试归入”。
 */
export function JoinAlbumDialog({ open, release, currentGroupId, commands, onClose }: JoinAlbumDialogProps) {
  const titleId = useId();
  // 初始焦点在标题上：对话框里的内容随搜索变化，标题是稳定的落点。
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "join";
  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      initialFocus={headingRef}
      className={styles.dialog}
      onRequestClose={() => {
        if (!pending) onClose();
      }}
    >
      <JoinAlbumForm titleId={titleId} headingRef={headingRef} release={release} currentGroupId={currentGroupId} commands={commands} onClose={onClose} />
    </ModalDialog>
  );
}
