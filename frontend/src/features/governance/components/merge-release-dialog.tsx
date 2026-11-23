import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { Button, FailureNotice, ModalDialog, Notice, Skeleton, TextField } from "../../../shared/ui";
import { utf8ByteLength } from "../../auth";
import { albumArtistOf, albumMeta, Cover, MAX_SEARCH_BYTES, parseSearchText, useAlbum, useAlbumSearchResults, type ReleaseGroupSummaryDTO } from "../../catalog";
import type { MergePreviewDTO } from "../api/decoders";
import { useMergePreview } from "../hooks/use-merge-preview";
import type { ReleaseCommands, ReleaseTarget } from "../hooks/use-release-commands";
import { governanceFailureMessages } from "./governance-messages";
import styles from "./join-album-dialog.module.css";
import own from "./merge-release-dialog.module.css";

type MergeReleaseDialogProps = {
  open: boolean;
  /** 目标：页面正在看的版本，别的版本的碟接在它后面。 */
  release: ReleaseTarget;
  /** 目标所在的专辑：先列出这张专辑的其他版本（被扫描拆开的两半最常见的样子）。 */
  groupId: string;
  commands: ReleaseCommands;
  onClose: () => void;
};

type Version = { id: string; title: string; artist: string | null; album_artist: string | null; year: number | null; medium_count: number; track_count: number };
type VersionChoice = { id: string; title: string };

function VersionOptions({ versions, name, selectedId, disabled, legend, onSelect }: { versions: readonly Version[]; name: string; selectedId: string | null; disabled: boolean; legend: string; onSelect: (version: VersionChoice) => void }) {
  return (
    <fieldset className={styles.results} disabled={disabled}>
      <legend className={styles.legend}>{legend}</legend>
      <ul className={styles.list}>
        {versions.map((version) => (
          <li key={version.id}>
            <label className={own.version}>
              <input type="radio" name={name} checked={selectedId === version.id} onChange={() => onSelect({ id: version.id, title: version.title })} />
              <span className={styles.text}>
                <span className={styles.optionTitle}>{version.title}</span>{" "}
                <span className={styles.optionMeta}>
                  {albumArtistOf({ album_artist: version.album_artist, artist: version.artist ?? "" })} · {albumMeta(version)}
                </span>
              </span>
            </label>
          </li>
        ))}
      </ul>
    </fieldset>
  );
}

/** 这张专辑的其他版本；只有当前版本时什么也不显示。 */
function SiblingVersions({ groupId, releaseId, selectedId, disabled, onSelect }: { groupId: string; releaseId: string; selectedId: string | null; disabled: boolean; onSelect: (version: VersionChoice) => void }) {
  const album = useAlbum(groupId);
  const siblings = album.data?.releases.filter((version) => version.id !== releaseId) ?? [];
  if (siblings.length === 0) return null;
  return <VersionOptions versions={siblings} name="merge-sibling" legend="这张专辑的其他版本" selectedId={selectedId} disabled={disabled} onSelect={onSelect} />;
}

/** 搜到的专辑里选一个之后，列出它的版本（不含目标本身）。 */
function AlbumVersions({ groupId, releaseId, selectedId, disabled, onSelect }: { groupId: string; releaseId: string; selectedId: string | null; disabled: boolean; onSelect: (version: VersionChoice) => void }) {
  const album = useAlbum(groupId);
  if (album.data === undefined) {
    return album.isError ? <FailureNotice error={album.error} action={<Button onClick={() => void album.refetch()}>重试</Button>} /> : <Skeleton label="正在读取专辑的版本" lines={2} />;
  }
  const versions = album.data.releases.filter((version) => version.id !== releaseId);
  if (versions.length === 0) return <p className={styles.hint}>这张专辑没有别的版本可以并入。</p>;
  return <VersionOptions versions={versions} name="merge-found" legend={`《${album.data.representative.title}》的版本`} selectedId={selectedId} disabled={disabled} onSelect={onSelect} />;
}

function SearchResults({ query, currentGroupId, selectedId, disabled, onSelect }: { query: string; currentGroupId: string; selectedId: string | null; disabled: boolean; onSelect: (group: ReleaseGroupSummaryDTO) => void }) {
  const results = useAlbumSearchResults(query);
  if (results.data === undefined) {
    return results.isError ? <FailureNotice error={results.error} action={<Button onClick={() => void results.refetch()}>重试</Button>} /> : <Skeleton label="正在搜索专辑" lines={3} />;
  }
  const groups = results.data.items.filter((group) => group.id !== currentGroupId);
  if (groups.length === 0) return <p className={styles.hint}>没有找到别的专辑。可以换个搜索词再试。</p>;
  return (
    <fieldset className={styles.results} disabled={disabled}>
      <legend className={styles.legend}>选择专辑</legend>
      <ul className={styles.list}>
        {groups.map((group) => (
          <li key={group.id}>
            <label className={styles.option}>
              <input type="radio" name="merge-album" checked={selectedId === group.id} onChange={() => onSelect(group)} />
              <span className={styles.cover}>
                <Cover artwork={group.representative.artwork} title={group.representative.title} variant="card" />
              </span>
              <span className={styles.text}>
                <span className={styles.optionTitle}>{group.representative.title}</span>{" "}
                <span className={styles.optionMeta}>
                  {albumArtistOf(group.representative)} · {albumMeta(group.representative)}
                  {group.release_count > 1 ? ` · ${group.release_count} 个版本` : ""}
                </span>
              </span>
            </label>
          </li>
        ))}
      </ul>
    </fieldset>
  );
}

/** 合并期间不生效、撤销之后恢复的来源上的内容；没有时是 null。 */
function hiddenText(preview: MergePreviewDTO): string | null {
  const { hidden } = preview;
  const parts = [
    hidden.corrections === 0 ? null : `${formatCount(hidden.corrections)} 项信息修正`,
    hidden.reviews === 0 ? null : `${formatCount(hidden.reviews)} 项问题核对`,
    hidden.default_version ? "默认版本的设定" : null,
    hidden.external_artwork ? "自动补的封面" : null,
    hidden.links === 0 ? null : "外部资料的对应",
    hidden.artist_overrides === 0 ? null : "按专辑指定的艺人",
  ].filter((part) => part !== null);
  return parts.length === 0 ? null : `《${preview.source.title}》上的${parts.join("、")}在合并期间不生效，撤销之后恢复。`;
}

/** 一碟的说明：“第 2 碟 · 来自《…》 · 12 首”；碟有自己的标题时写上（扫描给没有标题的碟记的是“Medium”）。 */
function mediumText(medium: MergePreviewDTO["media"][number], preview: MergePreviewDTO): string {
  const from = medium.from_release === preview.source.id ? preview.source.title : preview.target.title;
  const title = medium.title === "" || medium.title === "Medium" ? "" : ` · ${medium.title}`;
  return `第 ${medium.position_after} 碟${title} · 来自《${from}》 · ${formatCount(medium.track_count)} 首`;
}

function MergePreview({ release, source, commands, onClose }: { release: ReleaseTarget; source: VersionChoice; commands: ReleaseCommands; onClose: () => void }) {
  const preview = useMergePreview(release.id, source.id);
  const state = commands.state;
  const pending = state.status === "pending" && state.command.kind === "merge";
  const failed = state.status === "failed" && state.command.kind === "merge" ? state : null;
  if (preview.status === "error") {
    return <FailureNotice error={preview.error} messages={governanceFailureMessages} title="不能并入这个版本" action={<Button onClick={() => void preview.refetch()}>重试</Button>} />;
  }
  if (preview.data === undefined) return <Skeleton label="正在生成合并预览" lines={4} />;
  const data = preview.data;
  const hidden = hiddenText(data);
  const confirm = () => {
    if (failed?.uncertain) {
      commands.retry({ onSucceeded: onClose });
      return;
    }
    commands.submit({ kind: "merge", release, source: { id: data.source.id, title: data.source.title }, token: data.token }, { onSucceeded: onClose });
  };
  return (
    <section className={own.preview} aria-labelledby="merge-preview-title">
      <h3 id="merge-preview-title" className={own.previewTitle}>
        并入之后的《{data.target.title}》
      </h3>
      <ol className={own.media} aria-label="并入之后的碟">
        {data.media.map((medium) => (
          <li key={medium.medium_id} data-source={medium.from_release === data.source.id ? "" : undefined}>
            {mediumText(medium, data)}
          </li>
        ))}
      </ol>
      <ul className={own.notes}>
        <li>《{data.source.title}》不再单独出现；重新扫描之后合并仍然保持。</li>
        {hidden === null ? null : <li>{hidden}</li>}
        {data.favorites_moved === 0 ? null : <li>《{data.source.title}》所在的专辑没有别的版本了，{formatCount(data.favorites_moved)} 个对它的收藏会转到这张专辑，撤销时转回。</li>}
        <li>之后可以从这个版本的“管理”或操作记录撤销。</li>
      </ul>
      {failed === null ? null : failed.uncertain ? (
        <FailureNotice error={failed.error} context="write" messages={governanceFailureMessages} title="结果未确认" />
      ) : isHttpFailure(failed.failure, 409, "merge_changed") ? (
        <Notice tone="warning" title="两个版本刚被改动过">
          预览已重新读取，请看过之后再并入。
        </Notice>
      ) : (
        <FailureNotice error={failed.error} context="write" messages={governanceFailureMessages} title="没有并入" />
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
        <Button variant="primary" busy={pending} onClick={confirm}>
          {pending ? "正在并入…" : failed?.uncertain ? "重试并入" : `把《${data.source.title}》并入这里`}
        </Button>
      </div>
    </section>
  );
}

function MergeReleaseForm({ titleId, headingRef, release, groupId, commands, onClose }: Omit<MergeReleaseDialogProps, "open"> & { titleId: string; headingRef: RefObject<HTMLHeadingElement | null> }) {
  const [source, setSource] = useState<VersionChoice | null>(null);
  const [draft, setDraft] = useState("");
  const [query, setQuery] = useState<string | undefined>(undefined);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [albumId, setAlbumId] = useState<string | null>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "merge";
  const choose = (version: VersionChoice) => {
    commands.dismiss();
    setSource(version);
  };

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
    setAlbumId(null);
  };

  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        把另一个版本并入这里
      </h2>
      <p className={styles.lead}>扫描把一张专辑拆成了几个版本时（例如每张碟各成一个），选一个版本，它的全部碟会接在《{release.title}》后面，合成一个发行。</p>
      <SiblingVersions groupId={groupId} releaseId={release.id} selectedId={source?.id ?? null} disabled={pending} onSelect={choose} />
      <form role="search" aria-label="搜索其他专辑的版本" className={styles.search} onSubmit={search}>
        <div className={styles.searchRow}>
          <div className={styles.searchField}>
            <TextField
              label="搜索其他专辑"
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
      {query === undefined ? null : <SearchResults query={query} currentGroupId={groupId} selectedId={albumId} disabled={pending} onSelect={(group) => setAlbumId(group.id)} />}
      {albumId === null ? null : <AlbumVersions groupId={albumId} releaseId={release.id} selectedId={source?.id ?? null} disabled={pending} onSelect={choose} />}
      {source === null ? (
        <div className={styles.actions}>
          <Button onClick={onClose}>取消</Button>
        </div>
      ) : (
        <MergePreview release={release} source={source} commands={commands} onClose={onClose} />
      )}
    </div>
  );
}

/**
 * 把另一个版本整张并入当前版本（M7 H，管理员）：先列出这张专辑的其他版本，也可以搜索别的专辑再选版本；选中后显示并入之后的碟序、
 * 合并期间不生效的内容与收藏的去向，确认后提交。预览之后有扫描或修改（409 merge_changed）时预览随刷新重新读取；结果不确定时按钮变成用同一个键重试。
 */
export function MergeReleaseDialog({ open, release, groupId, commands, onClose }: MergeReleaseDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "merge";
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
      <MergeReleaseForm titleId={titleId} headingRef={headingRef} release={release} groupId={groupId} commands={commands} onClose={onClose} />
    </ModalDialog>
  );
}
