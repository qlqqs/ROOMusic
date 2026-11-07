import { useQuery } from "@tanstack/react-query";
import { useId, useMemo, useRef, useState, type FormEvent } from "react";
import { isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { Button, FailureNotice, ModalDialog, Notice, Skeleton, TextField } from "../../../shared/ui";
import { useViewerScope } from "../../auth";
import { pageCountOf } from "../../../shared/lib/pagination";
import { utf8ByteLength } from "../../auth";
import { artistRolesText, MAX_SEARCH_BYTES, parseSearchText } from "../../catalog";
import { useArtistNames, useArtistParticipationAlbums, type ArtistNameDTO } from "../../artists";
import type { ArtistMergedSourceDTO } from "../api/decoders";
import { fetchArtistMergedSources, previewArtistSplit, type ArtistSplitScope, type ArtistSplitTarget } from "../api/endpoints";
import type { ArtistCommands, ArtistTarget } from "../hooks/use-artist-commands";
import { governanceKeys } from "../query-keys";
import { ArtistDialogFailure } from "./artist-command-notice";
import styles from "./artist-dialog.module.css";

/** 拆开不能撤销：一句话说清楚，以及反向的办法。 */
export const SPLIT_IRREVERSIBLE_TEXT = "拆开不能撤销，需要时可以再合并。";

type SplitSource = ArtistTarget & { revision: number };

type SplitArtistDialogProps = { open: boolean; artist: SplitSource; commands: ArtistCommands; onClose: () => void };

/**
 * 拆开（管理员）：两种方式——
 * - 别名：把选中的名字（同一个名字键的繁简、大小写写法一起）拆成另一位艺人；
 * - 按专辑：在选中的专辑上，把这些名字的署名分给另一位同名的人（记住当时的版本，这些版本以后新增的曲目跟着走）。
 * 最前面列出之前并入这里的艺人，“拆回 X”一键选好那次合并带过来的名字并恢复这位（收藏和原来的链接随之回来）；手动选名字时，
 * 所选名字全部来自同一位已并入的艺人就默认恢复这位，否则默认新艺人，管理员都可以改。先预览影响范围，再确认；预览过时（409 artist_scope_changed）就重新预览。
 */
export function SplitArtistDialog({ open, artist, commands, onClose }: SplitArtistDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "split";
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={() => (pending ? undefined : onClose())}>
      <SplitForm titleId={titleId} headingRef={headingRef} artist={artist} commands={commands} onClose={onClose} />
    </ModalDialog>
  );
}

/** 名字按名字键归在一起：同一个键的写法（繁简、大小写）一起拆，不能只拆其中一种写法。 */
function nameGroupsOf(names: readonly ArtistNameDTO[]) {
  const groups = new Map<string, { keyId: string; names: string[]; display: boolean }>();
  for (const name of names) {
    const group = groups.get(name.key_id) ?? { keyId: name.key_id, names: [], display: false };
    group.names.push(name.name);
    group.display ||= name.is_display_name;
    groups.set(name.key_id, group);
  }
  return [...groups.values()];
}

/** 之前并入这里、现在可以拆回原身份的艺人（管理员接口；前端不从操作记录推断）。 */
function useMergedSources(artistId: string) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: governanceKeys.artistMergedSources(generation, artistId),
    queryFn: ({ signal }) => fetchArtistMergedSources(artistId, signal),
    select: (response) => response.data.items,
    staleTime: 0,
  });
}

/** 所选名字键全部来自同一位已并入的艺人时就是这位，否则 null（默认拆给新艺人）。 */
function sourceOwning(keys: readonly string[], sources: readonly ArtistMergedSourceDTO[]): ArtistMergedSourceDTO | null {
  if (keys.length === 0) return null;
  return sources.find((source) => keys.every((key) => source.name_key_ids.includes(key))) ?? null;
}

const toggle = (list: readonly string[], id: string) => (list.includes(id) ? list.filter((value) => value !== id) : [...list, id]);

function SplitForm({ titleId, headingRef, artist, commands, onClose }: Omit<SplitArtistDialogProps, "open"> & { titleId: string; headingRef: React.RefObject<HTMLHeadingElement | null> }) {
  const { generation } = useViewerScope();
  const radioName = useId();
  const targetRadio = useId();
  const names = useArtistNames(artist.id, 1, true);
  const merged = useMergedSources(artist.id);
  const sources = useMemo(() => merged.data ?? [], [merged.data]);
  const [mode, setMode] = useState<"aliases" | "albums">("aliases");
  const [keyIds, setKeyIds] = useState<readonly string[]>([]);
  const [groupIds, setGroupIds] = useState<readonly string[]>([]);
  // 拆给谁：管理员明确选过就按他选的；没选过时按所选名字自动决定（全部来自同一位已并入的艺人就恢复这位，否则新艺人）。
  const [chosenTarget, setChosenTarget] = useState<string | null>(null);
  const [newName, setNewName] = useState("");
  const [previewing, setPreviewing] = useState<ArtistSplitScope | null>(null);

  const groups = useMemo(() => nameGroupsOf(names.data?.items ?? []), [names.data]);
  // 按专辑拆开时默认选中全部名字（同名的人通常用的是同一个名字）；别名拆回时一个也不默认选。
  const chosenKeys = mode === "albums" && keyIds.length === 0 ? groups.map((group) => group.keyId) : keyIds;
  const automatic = sourceOwning(chosenKeys, sources);
  const targetChoice = chosenTarget ?? automatic?.artist_id ?? "new";
  const restore = sources.find((source) => source.artist_id === targetChoice) ?? null;
  const scope: ArtistSplitScope | null =
    mode === "aliases" ? (chosenKeys.length === 0 ? null : { mode, nameKeyIds: chosenKeys }) : chosenKeys.length === 0 || groupIds.length === 0 ? null : { mode, groupIds, nameKeyIds: chosenKeys };

  const preview = useQuery({
    queryKey: governanceKeys.artistSplitPreview(generation, artist.id, previewing),
    queryFn: ({ signal }) => previewArtistSplit(artist.id, previewing ?? { mode: "aliases", nameKeyIds: [] }, signal),
    select: (response) => response.data,
    enabled: previewing !== null,
    staleTime: 0,
  });

  const state = commands.state;
  const pending = state.status === "pending" && state.command.kind === "split";
  const failed = state.status === "failed" && state.command.kind === "split" && state.command.artist.id === artist.id ? state : null;
  const scopeChanged = failed !== null && !failed.uncertain && isHttpFailure(failed.failure, 409, "artist_scope_changed");
  const target: ArtistSplitTarget =
    restore === null ? { kind: "new", displayName: newName.trim() === "" ? undefined : newName.trim() } : { kind: "restore", artistId: restore.artist_id, expectedRevision: restore.revision };
  const firstName = groups.find((group) => chosenKeys.includes(group.keyId))?.names[0];
  const targetName = restore !== null ? restore.name : newName.trim() === "" ? (firstName ?? artist.name) : newName.trim();

  const change = (next: () => void) => {
    next();
    setPreviewing(null);
    commands.dismiss();
  };
  /** “拆回 X”：一键选好别名方式、那次合并带过来的名字，目标恢复这位。 */
  const splitBack = (source: ArtistMergedSourceDTO) =>
    change(() => {
      setMode("aliases");
      setKeyIds(source.name_key_ids);
      // 名字都来自这位时交给自动规则（之后改名字仍会按规则重新判断）；那次带过来的名字已经都不在这里时，直接指定恢复这位。
      setChosenTarget(source.name_key_ids.length === 0 ? source.artist_id : null);
    });

  const confirm = () => {
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    if (preview.data === undefined || previewing === null) return;
    commands.submit(
      { kind: "split", artist: { id: artist.id, name: artist.name }, revision: preview.data.revision, basis: preview.data.basis, scope: previewing, target, targetName },
      {
        onSucceeded: onClose,
        onFailed: (failure) => {
          // 预览过时：重新读一次预览，管理员看过新的范围再确认。
          if (isHttpFailure(failure, 409, "artist_scope_changed")) void preview.refetch();
        },
      },
    );
  };

  const loading = names.data === undefined && !names.isError;
  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        拆开艺人
      </h2>
      {sources.length === 0 ? null : (
        <section className={styles.section} aria-labelledby={`${titleId}-merged`}>
          <h3 id={`${titleId}-merged`} className={styles.legend}>
            之前并入这里的艺人
          </h3>
          <ul className={styles.list}>
            {sources.map((source) => (
              <li key={source.artist_id}>
                <Button aria-pressed={restore?.artist_id === source.artist_id && mode === "aliases" && sameKeys(keyIds, source.name_key_ids)} disabled={pending} onClick={() => splitBack(source)}>
                  拆回 {source.name}
                </Button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {merged.isError ? <FailureNotice error={merged.error} action={<Button onClick={() => void merged.refetch()}>重试</Button>} /> : null}
      <fieldset className={styles.section} disabled={pending}>
        <legend className={styles.legend}>怎样拆开</legend>
        <label className={styles.option}>
          <input type="radio" name={radioName} checked={mode === "aliases"} onChange={() => change(() => setMode("aliases"))} />
          <span className={styles.optionText}>把一些名字拆成另一位艺人</span>
        </label>
        <label className={styles.option}>
          <input type="radio" name={radioName} checked={mode === "albums"} onChange={() => change(() => setMode("albums"))} />
          <span className={styles.optionText}>按专辑分给另一位同名的人</span>
        </label>
      </fieldset>
      {names.isError ? <FailureNotice error={names.error} action={<Button onClick={() => void names.refetch()}>重试</Button>} /> : null}
      {loading ? (
        <Skeleton label="正在读取名字" lines={3} />
      ) : (
        <fieldset className={styles.section} disabled={pending}>
          <legend className={styles.legend}>{mode === "aliases" ? "要拆出去的名字" : "这些专辑上的哪些名字"}</legend>
          <ul className={styles.list}>
            {groups.map((group) => (
              <li key={group.keyId}>
                <label className={styles.option}>
                  <input type="checkbox" checked={chosenKeys.includes(group.keyId)} onChange={() => change(() => setKeyIds(toggle(chosenKeys, group.keyId)))} />
                  <span className={styles.optionText}>
                    <span className={styles.optionTitle}>{group.names.join(" / ")}</span>
                    {group.display ? <span className={styles.optionMeta}>现在的主名</span> : null}
                  </span>
                </label>
              </li>
            ))}
          </ul>
        </fieldset>
      )}
      {mode === "albums" ? <AlbumChoices artistId={artist.id} selected={groupIds} disabled={pending} onToggle={(groupId) => change(() => setGroupIds(toggle(groupIds, groupId)))} /> : null}
      <fieldset className={styles.section} disabled={pending}>
        <legend className={styles.legend}>拆给谁</legend>
        <label className={styles.option}>
          <input type="radio" name={targetRadio} checked={restore === null} onChange={() => change(() => setChosenTarget("new"))} />
          <span className={styles.optionText}>一位新的艺人</span>
        </label>
        {sources.map((source) => (
          <label key={source.artist_id} className={styles.option}>
            <input type="radio" name={targetRadio} checked={restore?.artist_id === source.artist_id} onChange={() => change(() => setChosenTarget(source.artist_id))} />
            <span className={styles.optionText}>
              <span className={styles.optionTitle}>恢复为“{source.name}”</span>
              <span className={styles.optionMeta}>收藏和原来的链接会一起回来</span>
            </span>
          </label>
        ))}
        {restore === null ? (
          <div className={styles.nameField}>
            <TextField label="新艺人的主名（可不填）" type="text" autoComplete="off" value={newName} onChange={(event) => setNewName(event.target.value)} />
          </div>
        ) : null}
      </fieldset>
      <div className={styles.footer}>
        {previewing === null ? null : preview.data === undefined ? (
          preview.isError ? (
            <FailureNotice error={preview.error} action={<Button onClick={() => void preview.refetch()}>重试</Button>} />
          ) : (
            <Skeleton label="正在预览" lines={1} />
          )
        ) : (
          <p className={styles.preview} role="status">
            会把 {formatCount(preview.data.scope.release_count)} 个版本、{formatCount(preview.data.scope.track_count)} 首曲目上的署名分给“{targetName}”。
            {preview.data.scope.excluded_scope_count > 0 ? `已经分给别人的 ${formatCount(preview.data.scope.excluded_scope_count)} 处不动。` : ""}
            {SPLIT_IRREVERSIBLE_TEXT}
          </p>
        )}
        {scopeChanged ? (
          <Notice tone="warning" title="预览已经过时">
            已重新预览，请再确认一次。
          </Notice>
        ) : (
          <ArtistDialogFailure state={state} kind="split" />
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
          {previewing === null || preview.data === undefined ? (
            <Button variant="primary" disabled={scope === null || previewing !== null} onClick={() => setPreviewing(scope)}>
              预览
            </Button>
          ) : (
            <Button variant="primary" busy={pending} onClick={confirm}>
              {pending ? "正在拆开…" : failed?.uncertain ? "重试拆开" : "拆开"}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

function sameKeys(a: readonly string[], b: readonly string[]): boolean {
  return a.length === b.length && a.every((key) => b.includes(key));
}

/**
 * 按专辑拆开时的专辑选择：这位艺人以任何身份参与的专辑，每项标出身份；可以按专辑名筛选、翻页，翻页与筛选时已选的保留。
 */
function AlbumChoices({ artistId, selected, disabled, onToggle }: { artistId: string; selected: readonly string[]; disabled: boolean; onToggle: (groupId: string) => void }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState<string | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const albums = useArtistParticipationAlbums(artistId, query, page);
  const data = albums.data;
  const filter = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (utf8ByteLength(draft.trim()) > MAX_SEARCH_BYTES) {
      setError("搜索词太长了，请短一些。");
      return;
    }
    setError(null);
    setQuery(parseSearchText(draft));
    setPage(1);
  };
  const pageCount = data === undefined ? 0 : pageCountOf(data.pagination.total, data.pagination.page_size);
  return (
    <div className={styles.section}>
      <form role="search" aria-label="筛选专辑" className={styles.search} onSubmit={filter}>
        <div className={styles.searchRow}>
          <div className={styles.searchField}>
            <TextField label="按专辑名筛选" type="text" inputMode="search" enterKeyHint="search" autoComplete="off" value={draft} error={error} disabled={disabled} onChange={(event) => setDraft(event.target.value)} />
          </div>
          <div className={styles.searchButton}>
            <Button type="submit" disabled={disabled}>
              筛选
            </Button>
          </div>
        </div>
      </form>
      {data === undefined ? (
        albums.isError ? <FailureNotice error={albums.error} action={<Button onClick={() => void albums.refetch()}>重试</Button>} /> : <Skeleton label="正在读取专辑" lines={3} />
      ) : data.pagination.total === 0 ? (
        <p className={styles.hint}>{query === undefined ? "这位艺人还没有参与的专辑。" : "没有找到匹配的专辑。"}</p>
      ) : (
        <fieldset className={styles.results} disabled={disabled} aria-busy={albums.isPlaceholderData}>
          <legend className={styles.legend}>分出去的专辑{selected.length === 0 ? "" : `（已选 ${formatCount(selected.length)} 张）`}</legend>
          <ul className={styles.list}>
            {data.items.map((item) => (
              <li key={item.group.id}>
                <label className={styles.option}>
                  <input type="checkbox" checked={selected.includes(item.group.id)} onChange={() => onToggle(item.group.id)} />
                  <span className={styles.optionText}>
                    <span className={styles.optionTitle}>{item.group.representative.title}</span>
                    <span className={styles.optionMeta}>
                      {artistRolesText(item.roles)}
                      {item.group.release_count > 1 ? ` · ${item.group.release_count} 个版本` : ""}
                    </span>
                  </span>
                </label>
              </li>
            ))}
          </ul>
          {pageCount > 1 ? (
            <div className={styles.pager}>
              <Button disabled={page <= 1} onClick={() => setPage(page - 1)}>
                上一页
              </Button>
              <span className={styles.optionMeta}>
                第 {page} / {pageCount} 页
              </span>
              <Button disabled={page >= pageCount} onClick={() => setPage(page + 1)}>
                下一页
              </Button>
            </div>
          ) : null}
        </fieldset>
      )}
    </div>
  );
}
