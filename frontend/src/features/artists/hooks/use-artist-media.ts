import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useEffectEvent, useRef, useState } from "react";
import { describeFailure, failureOf, isOutcomeUncertain, type ApiFailure } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import { useViewerScope } from "../../auth";
import type { ArtistLinkPreviewDTO, ArtistWebSearchDTO } from "../api/decoders";
import { decideArtistCandidate, fetchArtistWebSearch, putArtistBio, putArtistLink, rejectArtistMedia, startArtistWebSearch, uploadArtistImage } from "../api/endpoints";
import { artistsKeys } from "../query-keys";

export type ArtistMediaField = "image" | "bio";

/** 标记不对之后的等待期：这段时间里可以撤回，过了才真正提交。 */
export const REJECT_UNDO_MS = 6000;

/** 上传图片的进度：本地预览在上传完成前就显示，失败时退回原来的圆章。 */
export type ArtistImageUpload = { status: "idle" } | { status: "uploading"; progress: number | null; preview: string };

export type PendingReject = { field: ArtistMediaField; since: number };

export type ArtistMediaControls = {
  upload: ArtistImageUpload;
  uploadImage: (file: File) => void;
  saveBio: (text: string) => Promise<boolean>;
  savingBio: boolean;
  reject: (field: ArtistMediaField) => void;
  undoReject: () => void;
  pendingReject: PendingReject | null;
  /** 正在等待提交或已经提交、还没刷新回来的标记：这一项先不显示。 */
  hidden: Readonly<Record<ArtistMediaField, boolean>>;
  applyLink: (preview: ArtistLinkPreviewDTO) => Promise<boolean>;
  /** 网络搜索的状态与待确认的候选；读到之前或读取失败时为 null（不显示任何网络搜索的提示）。 */
  webSearch: ArtistWebSearchDTO | null;
  /** 采纳一个候选，或不采纳（decline）它；“都不是”就是把全部候选都不采纳。正在处理的候选在 deciding 里。 */
  decideCandidates: (exceptionIds: readonly string[], decision: "accept" | "decline") => Promise<boolean>;
  deciding: boolean;
  /** 联网查找这一位（只在 webSearch.configured 且 can_start 时可用）；受理后用返回的状态更新显示。 */
  startWebSearch: () => Promise<boolean>;
  startingWebSearch: boolean;
  /** 最近一次失败的说明（就近显示在页头下方）；新的操作开始时清掉。 */
  error: string | null;
  dismissError: () => void;
};

const MAX_IMAGE_BYTES = 16 * 1024 * 1024;
const IMAGE_TYPES = ["image/jpeg", "image/png", "image/webp"];

const failureMessages: Readonly<Record<string, string>> = {
  revision_conflict: "艺人刚被修改过，页面已刷新，请再试一次。",
  nothing_to_apply: "这项资料已经不在了。",
  revert_conflict: "这项资料刚被换过，页面已刷新，请再看一下。",
  artist_catalog_not_ready: "艺人信息正在整理，请稍后再试。",
  model_not_configured: "还没有配好整理用的模型，到设置里配好后再查。",
  search_unconfigured: "管家设置里还没有配网页搜索，配好后再查。",
  source_disabled: "MusicBrainz 已关闭，联网查找要用它核对。",
};

function messageOf(failure: ApiFailure | null, fallback: string): string {
  if (failure === null) return fallback;
  if (failure.kind === "http" && failure.status === 413) return "图片太大了：请选择 16 MB 以内的图片。";
  return describeFailure(failure, { context: "write", messages: failureMessages }).message;
}

/** 简介里有汉字就当作中文，否则当作英文。 */
export function bioLangOf(text: string): "zh" | "en" {
  return /\p{Script=Han}/u.test(text) ? "zh" : "en";
}

/**
 * 管理员对一位艺人的资料写入（上传图片、编辑简介、标记不对、指定对应）。悲观更新：后端确认并刷新之后才换上新资料；
 * 唯一的例外是“标记不对”——先把那一项藏起来，等 6 秒撤回期过了再提交（离开页面、再标记另一项时立即提交，页面卸载时用 keepalive）。
 * 每次写入一个幂等键；无论成败都刷新（refresh 由 app 提供：艺人与搜索的缓存）。
 */
export function useArtistMedia({ artistId, revision, refresh }: { artistId: string; revision: number; refresh: () => Promise<unknown> }): ArtistMediaControls {
  const scope = useViewerScope();
  const [upload, setUpload] = useState<ArtistImageUpload>({ status: "idle" });
  const [savingBio, setSavingBio] = useState(false);
  const [pendingReject, setPendingReject] = useState<PendingReject | null>(null);
  const [submitted, setSubmitted] = useState<Readonly<Record<ArtistMediaField, boolean>>>({ image: false, bio: false });
  const [error, setError] = useState<string | null>(null);
  const [deciding, setDeciding] = useState(false);
  const { generation } = scope;
  // 排队与处理中时每 20 秒看一次进展；其余状态不轮询（采纳、标记之后随刷新重读）。
  const webSearch = useQuery({
    queryKey: artistsKeys.webSearch(generation, artistId),
    queryFn: ({ signal }) => fetchArtistWebSearch(artistId, signal),
    select: (response) => response.data,
    refetchInterval: (query) => {
      const state = query.state.data?.data.state;
      return state === "queued" || state === "running" ? 20_000 : false;
    },
  });
  const timer = useRef<number | undefined>(undefined);
  const pending = useRef<{ artistId: string; field: ArtistMediaField; key: string } | null>(null);
  // 等待中的标记在提交时才取版本号：前一项提交并刷新后版本号已经变了。
  const latestRevision = useRef(revision);
  useEffect(() => {
    latestRevision.current = revision;
  }, [revision]);

  const settle = useCallback(async () => {
    await refresh();
    if (scope.isCurrent()) setSubmitted({ image: false, bio: false });
  }, [refresh, scope]);

  const fail = useCallback(
    (cause: unknown, fallback: string) => {
      if (!scope.isCurrent()) return;
      const failure = failureOf(cause);
      setError(isOutcomeUncertain(failure) ? "连接断了，不确定操作有没有完成，页面已刷新。" : messageOf(failure, fallback));
    },
    [scope],
  );

  const commitReject = useCallback(
    (keepalive: boolean) => {
      window.clearTimeout(timer.current);
      timer.current = undefined;
      const job = pending.current;
      if (job === null) return;
      pending.current = null;
      setPendingReject(null);
      setSubmitted((current) => ({ ...current, [job.field]: true }));
      rejectArtistMedia(job.artistId, { expectedRevision: latestRevision.current, field: job.field }, job.key, keepalive)
        .catch((cause: unknown) => fail(cause, "没能标记，请重试。"))
        .finally(() => void settle());
    },
    [fail, settle],
  );

  // 离开页面（换艺人、关标签页）时把等待中的标记提交掉，不悄悄丢弃。
  const flush = useEffectEvent(() => commitReject(true));
  useEffect(() => {
    const onHide = () => flush();
    window.addEventListener("pagehide", onHide);
    return () => {
      window.removeEventListener("pagehide", onHide);
      flush();
    };
  }, [artistId]);

  const reject = useCallback(
    (field: ArtistMediaField) => {
      if (pending.current !== null) commitReject(false);
      setError(null);
      pending.current = { artistId, field, key: newIdempotencyKey() };
      setPendingReject({ field, since: Date.now() });
      timer.current = window.setTimeout(() => commitReject(false), REJECT_UNDO_MS);
    },
    [artistId, commitReject],
  );

  const undoReject = useCallback(() => {
    window.clearTimeout(timer.current);
    timer.current = undefined;
    pending.current = null;
    setPendingReject(null);
  }, []);

  const uploadImage = useCallback(
    (file: File) => {
      setError(null);
      if (!IMAGE_TYPES.includes(file.type)) {
        setError("请选择 JPEG、PNG 或 WebP 图片。");
        return;
      }
      if (file.size > MAX_IMAGE_BYTES) {
        setError("图片太大了：请选择 16 MB 以内的图片。");
        return;
      }
      const preview = URL.createObjectURL(file);
      setUpload({ status: "uploading", progress: null, preview });
      uploadArtistImage(artistId, { expectedRevision: revision, file, onProgress: (progress) => setUpload({ status: "uploading", progress, preview }) }, newIdempotencyKey())
        .catch((cause: unknown) => fail(cause, "图片没能上传，请重试。"))
        .finally(() => {
          void settle().finally(() => {
            URL.revokeObjectURL(preview);
            if (scope.isCurrent()) setUpload({ status: "idle" });
          });
        });
    },
    [artistId, revision, fail, settle, scope],
  );

  const saveBio = useCallback(
    async (text: string) => {
      setError(null);
      setSavingBio(true);
      try {
        await putArtistBio(artistId, { expectedRevision: revision, text, lang: bioLangOf(text) }, newIdempotencyKey());
        await settle();
        return true;
      } catch (cause) {
        fail(cause, "简介没能保存，请重试。");
        void settle();
        return false;
      } finally {
        if (scope.isCurrent()) setSavingBio(false);
      }
    },
    [artistId, revision, fail, settle, scope],
  );

  const applyLink = useCallback(
    async (preview: ArtistLinkPreviewDTO) => {
      setError(null);
      try {
        await putArtistLink(artistId, { expectedRevision: revision, source: preview.source, externalId: preview.external_id }, newIdempotencyKey());
        await settle();
        return true;
      } catch (cause) {
        fail(cause, "没能指定对应，请重试。");
        void settle();
        return false;
      }
    },
    [artistId, revision, fail, settle],
  );

  const decideCandidates = useCallback(
    async (exceptionIds: readonly string[], decision: "accept" | "decline") => {
      setError(null);
      setDeciding(true);
      try {
        for (const id of exceptionIds) await decideArtistCandidate(id, decision);
        await settle();
        return true;
      } catch (cause) {
        fail(cause, decision === "accept" ? "没能采纳这个候选，请重试。" : "没能处理这些候选，请重试。");
        void settle();
        return false;
      } finally {
        if (scope.isCurrent()) setDeciding(false);
      }
    },
    [fail, settle, scope],
  );

  const queryClient = useQueryClient();
  const [startingWebSearch, setStartingWebSearch] = useState(false);
  const startWebSearch = useCallback(async () => {
    setError(null);
    setStartingWebSearch(true);
    try {
      const response = await startArtistWebSearch(artistId);
      if (scope.isCurrent()) queryClient.setQueryData(artistsKeys.webSearch(generation, artistId), response);
      return true;
    } catch (cause) {
      fail(cause, "没能开始联网查找，请重试。");
      return false;
    } finally {
      if (scope.isCurrent()) setStartingWebSearch(false);
    }
  }, [artistId, fail, generation, queryClient, scope]);

  const hidden = {
    image: submitted.image || pendingReject?.field === "image",
    bio: submitted.bio || pendingReject?.field === "bio",
  };
  return {
    upload,
    uploadImage,
    saveBio,
    savingBio,
    reject,
    undoReject,
    pendingReject,
    hidden,
    applyLink,
    webSearch: webSearch.data ?? null,
    decideCandidates,
    deciding,
    startWebSearch,
    startingWebSearch,
    error,
    dismissError: () => setError(null),
  };
}
