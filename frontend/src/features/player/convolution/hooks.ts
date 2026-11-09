import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useViewerScope } from "../../auth";
import { playerKeys } from "../query-keys";
import { deleteIrFile, fetchIrFiles, renameIrFile, uploadIrFile } from "./api";

export function useIrFiles() {
  const { generation } = useViewerScope();
  return useQuery({
    queryKey: playerKeys.irFiles(generation),
    queryFn: ({ signal }) => fetchIrFiles(signal),
    select: (response) => response.data,
  });
}

/** IR 文件的上传、改名、删除：完成后重读列表。上传带进度，可以取消。 */
export function useIrFileActions() {
  const { generation } = useViewerScope();
  const client = useQueryClient();
  const onSuccess = () => client.invalidateQueries({ queryKey: playerKeys.irFiles(generation) });
  const [progress, setProgress] = useState(0);
  const controller = useRef<AbortController | null>(null);
  const upload = useMutation({
    mutationFn: ({ name, file }: { name: string; file: File }) => {
      controller.current = new AbortController();
      setProgress(0);
      return uploadIrFile(name, file, { signal: controller.current.signal, onProgress: setProgress });
    },
    onSuccess,
  });
  return {
    upload,
    progress,
    cancelUpload: () => controller.current?.abort(),
    rename: useMutation({ mutationFn: ({ irId, name }: { irId: string; name: string }) => renameIrFile(irId, name), onSuccess }),
    remove: useMutation({ mutationFn: (irId: string) => deleteIrFile(irId), onSuccess }),
  };
}
