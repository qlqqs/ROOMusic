import { decode as d } from "../../../shared/api";
import type { ApiResponse } from "../../../shared/api";
import { apiRequest } from "../../../shared/api";
import { peqDocumentDecoder, peqToWire, type PeqChannels, type PeqDocument } from "./document";

// 本人的 PEQ 资源（PEQ 设计 §8）：OPRA 耳机校正、文本导入、频响曲线与预设。播放区里的 PEQ 走 set_settings。

const base = "/api/v1/me/dsp";

export type OpraProductSummary = { productId: string; vendor: string; name: string; subtype: string; eqCount: number };
export type OpraEq = { eqId: string; author: string; details: string; link: string; peq: PeqDocument };
export type OpraProduct = { product: { productId: string; vendor: string; name: string; subtype: string }; eqs: OpraEq[] };
export type PeqImport = { peq: PeqDocument; unrecognizedLines: number[] };
export type PeqCurve = { channels: PeqChannels; points: readonly (readonly [number, number])[] };
export type PeqPreset = { presetId: string; name: string; peq: PeqDocument; updatedAt: string };

const productDecoder = d.object({ product_id: d.string({ max: 256 }), vendor: d.string({ max: 256 }), name: d.string({ max: 256 }), subtype: d.string({ max: 64 }) });

export const decodeSearch = d.defineDecoder((input, path): OpraProductSummary[] =>
  d.object({ items: d.array(d.object({ product_id: d.string({ max: 256 }), vendor: d.string({ max: 256 }), name: d.string({ max: 256 }), subtype: d.string({ max: 64 }), eq_count: d.integer({ min: 0 }) }), { max: 30 }) })(input, path).items.map((item) => ({
    productId: item.product_id,
    vendor: item.vendor,
    name: item.name,
    subtype: item.subtype,
    eqCount: item.eq_count,
  })),
);

export const decodeProduct = d.defineDecoder((input, path): OpraProduct => {
  const raw = d.object({ product: productDecoder, eqs: d.array(d.object({ eq_id: d.string({ max: 256 }), author: d.string({ max: 256 }), details: d.string({ max: 4096 }), link: d.string({ max: 2048 }), peq: peqDocumentDecoder }), { max: 1000 }) })(input, path);
  return {
    product: { productId: raw.product.product_id, vendor: raw.product.vendor, name: raw.product.name, subtype: raw.product.subtype },
    eqs: raw.eqs.map((eq) => ({ eqId: eq.eq_id, author: eq.author, details: eq.details, link: eq.link, peq: eq.peq })),
  };
});

export const decodeImport = d.defineDecoder((input, path): PeqImport => {
  const raw = d.object({ peq: peqDocumentDecoder, unrecognized_lines: d.array(d.integer({ min: 1 }), { max: d.unbounded }) })(input, path);
  return { peq: raw.peq, unrecognizedLines: raw.unrecognized_lines };
});

const pointDecoder: d.Decoder<readonly [number, number]> = (input, path) => {
  const [hz, db] = d.array(d.number(), { min: 2, max: 2 })(input, path);
  return [hz ?? 0, db ?? 0];
};

export const decodeCurves = d.defineDecoder((input, path): PeqCurve[] =>
  d.object({ curves: d.array(d.object({ channels: (value, at) => (value === "all" ? "all" : d.array(d.integer({ min: 0, max: 31 }), { max: 32 })(value, at)), points: d.array(pointDecoder, { max: 1024 }) }), { max: 33 }) })(input, path).curves,
);

const presetDecoder: d.Decoder<PeqPreset> = (input, path) => {
  const raw = d.object({ preset_id: d.uuid(), name: d.string({ min: 1, max: 64 }), peq: peqDocumentDecoder, updated_at: d.timestamp() })(input, path);
  return { presetId: raw.preset_id, name: raw.name, peq: raw.peq, updatedAt: raw.updated_at };
};
export const decodePreset = d.defineDecoder(presetDecoder);
export const decodePresets = d.defineDecoder((input, path): PeqPreset[] => d.object({ items: d.array(presetDecoder, { max: 200 }) })(input, path).items);

export function searchOpra(query: string, signal?: AbortSignal): Promise<ApiResponse<OpraProductSummary[]>> {
  return apiRequest({ method: "GET", path: `${base}/opra/search?q=${encodeURIComponent(query)}`, signal }, decodeSearch);
}

export function fetchOpraProduct(productId: string, signal?: AbortSignal): Promise<ApiResponse<OpraProduct>> {
  return apiRequest({ method: "GET", path: `${base}/opra/products/${encodeURIComponent(productId)}`, signal }, decodeProduct);
}

/** 只解析，不保存；没有任何 Filter 行时是 400 peq_import_empty。 */
export function importPeq(text: string, signal?: AbortSignal): Promise<ApiResponse<PeqImport>> {
  return apiRequest({ method: "POST", path: `${base}/peq/import`, body: { text }, signal }, decodeImport);
}

/** 频响曲线：只预览，不写播放区。 */
export function fetchPeqResponse(peq: PeqDocument, signal?: AbortSignal): Promise<ApiResponse<PeqCurve[]>> {
  return apiRequest({ method: "POST", path: `${base}/peq/response`, body: { peq: peqToWire(peq) }, signal }, decodeCurves);
}

export function fetchPeqPresets(signal?: AbortSignal): Promise<ApiResponse<PeqPreset[]>> {
  return apiRequest({ method: "GET", path: `${base}/peq-presets`, signal }, decodePresets);
}

export function createPeqPreset(name: string, peq: PeqDocument): Promise<ApiResponse<PeqPreset>> {
  return apiRequest({ method: "POST", path: `${base}/peq-presets`, body: { name, peq: peqToWire(peq) } }, decodePreset);
}

export function renamePeqPreset(presetId: string, name: string): Promise<ApiResponse<PeqPreset>> {
  return apiRequest({ method: "PATCH", path: `${base}/peq-presets/${encodeURIComponent(presetId)}`, body: { name } }, decodePreset);
}

export function deletePeqPreset(presetId: string): Promise<ApiResponse<null>> {
  return apiRequest({ method: "DELETE", path: `${base}/peq-presets/${encodeURIComponent(presetId)}` }, () => null);
}
