import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useViewerScope } from "../../auth";
import { playerKeys } from "../query-keys";
import { createPeqPreset, deletePeqPreset, fetchOpraProduct, fetchPeqPresets, fetchPeqResponse, importPeq, renamePeqPreset, searchOpra } from "./api";
import { peqToWire, type PeqDocument } from "./document";

/** 停止变化 delay 毫秒后才跟上的值：输入时不每个字都发请求。 */
export function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

export function useOpraSearch(query: string) {
  const { generation } = useViewerScope();
  const q = useDebounced(query.trim(), 250);
  return useQuery({
    queryKey: playerKeys.opraSearch(generation, q),
    queryFn: ({ signal }) => searchOpra(q, signal),
    select: (response) => response.data,
    enabled: q !== "",
    placeholderData: keepPreviousData,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export function useOpraProduct(productId: string | null) {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: playerKeys.opraProduct(generation, productId ?? ""),
    queryFn: ({ signal }) => fetchOpraProduct(productId ?? "", signal),
    select: (response) => response.data,
    enabled: productId !== null,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

/** 频响曲线：编辑停下 150 ms 后再读，读到新的之前保留旧曲线。 */
export function usePeqResponse(peq: PeqDocument) {
  const { generation } = useViewerScope();
  const settled = useDebounced(peq, 150);
  return useQuery({
    queryKey: playerKeys.peqResponse(generation, JSON.stringify(peqToWire(settled))),
    queryFn: ({ signal }) => fetchPeqResponse(settled, signal),
    select: (response) => response.data,
    placeholderData: keepPreviousData,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

/** 粘贴的文本：停下 400 ms 后解析预览。 */
export function usePeqImport(text: string) {
  const { generation } = useViewerScope();
  const settled = useDebounced(text.trim(), 400);
  return useQuery({
    queryKey: playerKeys.peqImport(generation, settled),
    queryFn: ({ signal }) => importPeq(settled, signal),
    select: (response) => response.data,
    enabled: settled !== "",
    retry: false,
    staleTime: Number.POSITIVE_INFINITY,
  });
}

export function usePeqPresets() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: playerKeys.peqPresets(generation),
    queryFn: ({ signal }) => fetchPeqPresets(signal),
    select: (response) => response.data,
  });
}

/** 预设的新建、改名、删除：完成后重读列表。 */
export function usePeqPresetActions() {
  const { generation } = useViewerScope();
  const client = useQueryClient();
  const onSuccess = () => client.invalidateQueries({ queryKey: playerKeys.peqPresets(generation) });
  return {
    create: useMutation({ mutationFn: ({ name, peq }: { name: string; peq: PeqDocument }) => createPeqPreset(name, peq), onSuccess }),
    rename: useMutation({ mutationFn: ({ presetId, name }: { presetId: string; name: string }) => renamePeqPreset(presetId, name), onSuccess }),
    remove: useMutation({ mutationFn: (presetId: string) => deletePeqPreset(presetId), onSuccess }),
  };
}
