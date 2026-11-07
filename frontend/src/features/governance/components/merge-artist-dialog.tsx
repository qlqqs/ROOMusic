import { useId, useRef, useState, type FormEvent } from "react";
import { Button, FailureNotice, ModalDialog, Skeleton, TextField } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { artistMetaText, MAX_SEARCH_BYTES, parseSearchText, type ArtistSummaryDTO } from "../../catalog";
import { useArtist, useArtistsPage } from "../../artists";
import type { ArtistCommands, ArtistTarget } from "../hooks/use-artist-commands";
import { ArtistDialogFailure } from "./artist-command-notice";
import styles from "./artist-dialog.module.css";

/** 合并后不能撤销：只用一句话说清楚，以及反向的办法。 */
export const MERGE_IRREVERSIBLE_TEXT = "合并不能撤销，需要时可以再拆开。";

type MergeTarget = ArtistTarget & { revision: number };

const PICKER_PAGE_SIZE = 20;

/** 选中的艺人：被并入的一方要带它的版本号，从详情读（列表摘要里没有）。 */
function useSourceRevision(sourceId: string | null) {
  const detail = useArtist(sourceId);
  return { revision: detail.data?.revision ?? null, error: detail.isError ? detail.error : null, retry: () => void detail.refetch() };
}

function ArtistOption({ artist, checked, disabled, onSelect, name }: { artist: ArtistSummaryDTO; checked: boolean; disabled: boolean; onSelect: () => void; name: string }) {
  const meta = artistMetaText(artist);
  return (
    <li>
      <label className={styles.option}>
        <input type="radio" name={name} checked={checked} disabled={disabled} onChange={onSelect} />
        <span className={styles.optionText}>
          <span className={styles.optionTitle}>{artist.name}</span>
          {meta === "" ? null : <span className={styles.optionMeta}>{meta}</span>}
        </span>
      </label>
    </li>
  );
}

function SearchResults({ query, target, selectedId, disabled, onSelect }: { query: string; target: MergeTarget; selectedId: string | null; disabled: boolean; onSelect: (artist: ArtistSummaryDTO) => void }) {
  const results = useArtistsPage({ q: query, sort: "name", page: 1, pageSize: PICKER_PAGE_SIZE });
  const radioName = useId();
  if (results.data === undefined) {
    return results.isError ? <FailureNotice error={results.error} action={<Button onClick={() => void results.refetch()}>重试</Button>} /> : <Skeleton label="正在查找艺人" lines={3} />;
  }
  const artists = results.data.items.filter((artist) => artist.id !== target.id);
  if (artists.length === 0) return <p className={styles.hint}>没有找到可以并入的艺人。可以换个名字再试。</p>;
  const more = results.data.pagination.total > results.data.items.length;
  return (
    <fieldset className={styles.results} disabled={disabled}>
      <legend className={styles.legend}>选择要并入的艺人</legend>
      <ul className={styles.list}>
        {artists.map((artist) => (
          <ArtistOption key={artist.id} artist={artist} name={radioName} checked={selectedId === artist.id} disabled={disabled} onSelect={() => onSelect(artist)} />
        ))}
      </ul>
      {more ? <p className={styles.hint}>只显示最相关的 {results.data.items.length} 位，找不到可以换个更准确的名字。</p> : null}
    </fieldset>
  );
}

type MergeArtistDialogProps = { open: boolean; target: MergeTarget; commands: ArtistCommands; onClose: () => void };

/**
 * 合并艺人（管理员，在艺人页上）：搜到另一位并入当前艺人，对方的名字变成别名。确认前说明谁并入谁，并用一句话说明不能撤销。
 * 409 时对话框保持打开并说明；结果不确定时主按钮变成用同一个幂等键“重试合并”。
 */
export function MergeArtistDialog({ open, target, commands, onClose }: MergeArtistDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "merge";
  return (
    <ModalDialog open={open} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={() => (pending ? undefined : onClose())}>
      <MergeForm titleId={titleId} headingRef={headingRef} target={target} commands={commands} onClose={onClose} />
    </ModalDialog>
  );
}

function MergeForm({ titleId, headingRef, target, commands, onClose }: Omit<MergeArtistDialogProps, "open"> & { titleId: string; headingRef: React.RefObject<HTMLHeadingElement | null> }) {
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState<string | undefined>(undefined);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [selected, setSelected] = useState<ArtistSummaryDTO | null>(null);
  const source = useSourceRevision(selected?.id ?? null);
  const state = commands.state;
  const pending = state.status === "pending" && state.command.kind === "merge";
  const failed = state.status === "failed" && state.command.kind === "merge" && state.command.artist.id === target.id ? state : null;

  const search = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (utf8ByteLength(draft.trim()) > MAX_SEARCH_BYTES) {
      setSearchError("搜索词太长了，请短一些。");
      return;
    }
    const text = parseSearchText(draft);
    if (text === undefined) {
      setSearchError("请输入搜索词。");
      return;
    }
    setSearchError(null);
    setQuery(text);
    setSelected(null);
    commands.dismiss();
  };

  const confirm = () => {
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    if (selected === null || source.revision === null) return;
    commands.submit(
      { kind: "merge", artist: { id: target.id, name: target.name }, revision: target.revision, source: { id: selected.id, name: selected.name }, sourceRevision: source.revision },
      { onSucceeded: onClose },
    );
  };

  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        合并艺人
      </h2>
      <form role="search" aria-label="查找要并入的艺人" className={styles.search} onSubmit={search}>
        <div className={styles.searchRow}>
          <div className={styles.searchField}>
            <TextField label="查找艺人" type="text" inputMode="search" enterKeyHint="search" autoComplete="off" value={draft} error={searchError} disabled={pending} onChange={(event) => setDraft(event.target.value)} />
          </div>
          <div className={styles.searchButton}>
            <Button type="submit" disabled={pending}>
              查找
            </Button>
          </div>
        </div>
      </form>
      {query === undefined ? null : <SearchResults query={query} target={target} selectedId={selected?.id ?? null} disabled={pending} onSelect={setSelected} />}
      <div className={styles.footer}>
        {selected === null ? null : (
          <p className={styles.preview} role="status">
            “{selected.name}”会并入“{target.name}”，名字成为“{target.name}”的别名。{MERGE_IRREVERSIBLE_TEXT}
          </p>
        )}
        {source.error === null ? null : <FailureNotice error={source.error} action={<Button onClick={source.retry}>重试</Button>} />}
        <ArtistDialogFailure state={state} kind="merge" />
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
          <Button variant="primary" busy={pending} disabled={!failed?.uncertain && (selected === null || source.revision === null)} onClick={confirm}>
            {pending ? "正在合并…" : failed?.uncertain ? "重试合并" : "合并"}
          </Button>
        </div>
      </div>
    </div>
  );
}

type CandidatePair = { left: ArtistSummaryDTO; right: ArtistSummaryDTO };

/**
 * 从“重复艺人”合并：先选留下哪一位（默认是参与更多的那位，它的主名保留），另一位并入它。两边的版本号从详情读。
 */
export function MergeCandidateDialog({ pair, commands, onClose }: { pair: CandidatePair | null; commands: ArtistCommands; onClose: () => void }) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "merge";
  return (
    <ModalDialog open={pair !== null} titleId={titleId} initialFocus={headingRef} className={styles.dialog} onRequestClose={() => (pending ? undefined : onClose())}>
      {pair === null ? null : <CandidateMergeForm titleId={titleId} headingRef={headingRef} pair={pair} commands={commands} onClose={onClose} />}
    </ModalDialog>
  );
}

/** 默认留下参与更多的一位（曲目数，再看专辑数）；一样多时留下左边的。 */
function defaultKept(pair: CandidatePair): "left" | "right" {
  const score = (artist: ArtistSummaryDTO) => artist.track_count * 1_000_000 + artist.album_count;
  return score(pair.right) > score(pair.left) ? "right" : "left";
}

function CandidateMergeForm({ titleId, headingRef, pair, commands, onClose }: { titleId: string; headingRef: React.RefObject<HTMLHeadingElement | null>; pair: CandidatePair; commands: ArtistCommands; onClose: () => void }) {
  const [kept, setKept] = useState<"left" | "right">(() => defaultKept(pair));
  const radioName = useId();
  const keep = kept === "left" ? pair.left : pair.right;
  const merged = kept === "left" ? pair.right : pair.left;
  const keepDetail = useArtist(keep.id);
  const mergedDetail = useArtist(merged.id);
  const state = commands.state;
  const pending = state.status === "pending" && state.command.kind === "merge";
  const failed = state.status === "failed" && state.command.kind === "merge" ? state : null;
  const ready = keepDetail.data !== undefined && mergedDetail.data !== undefined;
  const confirm = () => {
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    if (keepDetail.data === undefined || mergedDetail.data === undefined) return;
    commands.submit(
      { kind: "merge", artist: { id: keep.id, name: keep.name }, revision: keepDetail.data.revision, source: { id: merged.id, name: merged.name }, sourceRevision: mergedDetail.data.revision },
      { onSucceeded: onClose },
    );
  };
  const loadError = keepDetail.isError ? keepDetail.error : mergedDetail.isError ? mergedDetail.error : null;
  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        合并为同一位艺人
      </h2>
      <fieldset className={styles.results} disabled={pending}>
        <legend className={styles.legend}>保留哪一位的主名</legend>
        <ul className={styles.list}>
          <ArtistOption artist={pair.left} name={radioName} checked={kept === "left"} disabled={pending} onSelect={() => setKept("left")} />
          <ArtistOption artist={pair.right} name={radioName} checked={kept === "right"} disabled={pending} onSelect={() => setKept("right")} />
        </ul>
      </fieldset>
      <div className={styles.footer}>
        <p className={styles.preview} role="status">
          “{merged.name}”会并入“{keep.name}”，名字成为别名。{MERGE_IRREVERSIBLE_TEXT}
        </p>
        {loadError === null ? null : (
          <FailureNotice
            error={loadError}
            action={
              <Button
                onClick={() => {
                  void keepDetail.refetch();
                  void mergedDetail.refetch();
                }}
              >
                重试
              </Button>
            }
          />
        )}
        <ArtistDialogFailure state={state} kind="merge" />
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
          <Button variant="primary" busy={pending} disabled={!failed?.uncertain && !ready} onClick={confirm}>
            {pending ? "正在合并…" : failed?.uncertain ? "重试合并" : "合并"}
          </Button>
        </div>
      </div>
    </div>
  );
}
