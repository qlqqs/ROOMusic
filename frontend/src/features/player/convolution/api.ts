import { decode as d } from "../../../shared/api";
import type { ApiResponse } from "../../../shared/api";
import { apiRequest, apiUpload } from "../../../shared/api";

// 本人的卷积 IR 文件（卷积设计 §6）。播放区里选哪个 IR 走 set_settings。

const base = "/api/v1/me/dsp/ir-files";

export const irContainers = ["zip", "wav", "flac", "aiff", "m4a"] as const;
export type IrContainer = (typeof irContainers)[number];

export type IrFile = {
  irId: string;
  name: string;
  fileName: string;
  container: IrContainer;
  sampleRate: number;
  channels: number;
  paths: number;
  taps: number;
  createdAt: string;
  updatedAt: string;
};

/** 上传接受的文件类型。 */
export const IR_ACCEPT = ".zip,.wav,.flac,.aif,.aiff,.m4a";

const irFileDecoder: d.Decoder<IrFile> = (input, path) => {
  const raw = d.object({
    ir_id: d.uuid(),
    name: d.string({ min: 1, max: 64 }),
    file_name: d.string({ max: 512 }),
    container: d.oneOf(irContainers),
    sample_rate: d.integer({ min: 1 }),
    channels: d.integer({ min: 1 }),
    paths: d.integer({ min: 1 }),
    taps: d.integer({ min: 1 }),
    created_at: d.timestamp(),
    updated_at: d.timestamp(),
  })(input, path);
  return {
    irId: raw.ir_id,
    name: raw.name,
    fileName: raw.file_name,
    container: raw.container,
    sampleRate: raw.sample_rate,
    channels: raw.channels,
    paths: raw.paths,
    taps: raw.taps,
    createdAt: raw.created_at,
    updatedAt: raw.updated_at,
  };
};
export const decodeIrFile = d.defineDecoder(irFileDecoder);
export const decodeIrFiles = d.defineDecoder((input, path): IrFile[] => d.object({ items: d.array(irFileDecoder, { max: 50 }) })(input, path).items);

export function fetchIrFiles(signal?: AbortSignal): Promise<ApiResponse<IrFile[]>> {
  return apiRequest({ method: "GET", path: base, signal }, decodeIrFiles);
}

export function uploadIrFile(name: string, file: File, options: { signal?: AbortSignal; onProgress?: (fraction: number) => void }): Promise<ApiResponse<IrFile>> {
  const form = new FormData();
  form.append("name", name);
  form.append("file", file);
  return apiUpload({ path: base, form, ...options }, decodeIrFile);
}

export function renameIrFile(irId: string, name: string): Promise<ApiResponse<IrFile>> {
  return apiRequest({ method: "PATCH", path: `${base}/${encodeURIComponent(irId)}`, body: { name } }, decodeIrFile);
}

export function deleteIrFile(irId: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: `${base}/${encodeURIComponent(irId)}` }, () => null);
}
