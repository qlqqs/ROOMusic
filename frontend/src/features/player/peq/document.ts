import { decode as d } from "../../../shared/api";

/** 段的类型（PEQ 设计 §1）。 */
export const peqBandTypes = ["peaking", "low_shelf", "high_shelf", "low_pass", "high_pass", "band_pass", "notch", "all_pass"] as const;
export type PeqBandType = (typeof peqBandTypes)[number];

/** 低通、高通的斜率（1–6 阶）。 */
export const peqSlopes = [6, 12, 18, 24, 30, 36] as const;

export const PEQ_MAX_BANDS = 32;
export const peqLimits = {
  freqHz: { min: 10, max: 40000 },
  gainDb: { min: -30, max: 30 },
  q: { min: 0.1, max: 30 },
  preampDb: { min: -30, max: 30 },
} as const;

/** 带增益的类型；带通、陷波、全通、低通、高通没有增益。 */
export function bandHasGain(type: PeqBandType): boolean {
  return type === "peaking" || type === "low_shelf" || type === "high_shelf";
}

/** 低通、高通用斜率，其余用 Q。 */
export function bandHasSlope(type: PeqBandType): boolean {
  return type === "low_pass" || type === "high_pass";
}

export type PeqChannels = "all" | readonly number[];

export type PeqBand = {
  type: PeqBandType;
  freqHz: number;
  gainDb: number | null;
  q: number | null;
  slopeDb: number | null;
  channels: PeqChannels;
  enabled: boolean;
};

export type PeqSource =
  | { kind: "opra"; eqId: string; vendor: string; product: string; author: string; details: string; link: string }
  | { kind: "preset"; presetId: string; name: string }
  | { kind: "import" };

export type PeqDocument = {
  enabled: boolean;
  preampDb: number;
  bands: readonly PeqBand[];
  source: PeqSource | null;
};

export const defaultPeq: PeqDocument = { enabled: false, preampDb: 0, bands: [], source: null };

/** 新加的一段：1 kHz 峰值，0 dB。 */
export const newBand: PeqBand = { type: "peaking", freqHz: 1000, gainDb: 0, q: 0.71, slopeDb: null, channels: "all", enabled: true };

/** 换类型时补上新类型要用的参数，原有的值保留。 */
export function withType(band: PeqBand, type: PeqBandType): PeqBand {
  return {
    ...band,
    type,
    gainDb: bandHasGain(type) ? (band.gainDb ?? 0) : band.gainDb,
    q: bandHasSlope(type) ? band.q : (band.q ?? 0.71),
    slopeDb: bandHasSlope(type) ? (band.slopeDb ?? 12) : band.slopeDb,
  };
}

/** 套用 OPRA 配置时的显示名：产品名 · 作者。 */
export function sourceLabel(source: PeqSource | null): string | null {
  if (source === null) return null;
  switch (source.kind) {
    case "opra":
      return `${source.product} · ${source.author}`;
    case "preset":
      return source.name;
    case "import":
      return "导入";
  }
}

const bandTypeDecoder = d.oneOf(peqBandTypes);
const channelsDecoder: d.Decoder<PeqChannels> = (input, path) => (input === "all" ? "all" : d.array(d.integer({ min: 0, max: 31 }), { max: 32 })(input, path));

const sourceDecoder: d.Decoder<PeqSource | null> = (input, path) => {
  const { kind } = d.object({ kind: d.string({ max: 64 }) })(input, path);
  switch (kind) {
    case "opra": {
      const raw = d.object({ eq_id: d.string({ max: 256 }), vendor: d.string({ max: 256 }), product: d.string({ max: 256 }), author: d.string({ max: 256 }), details: d.string({ max: 4096 }), link: d.string({ max: 2048 }) })(input, path);
      return { kind, eqId: raw.eq_id, vendor: raw.vendor, product: raw.product, author: raw.author, details: raw.details, link: raw.link };
    }
    case "preset": {
      const raw = d.object({ preset_id: d.uuid(), name: d.string({ max: 64 }) })(input, path);
      return { kind, presetId: raw.preset_id, name: raw.name };
    }
    case "import":
      return { kind };
    default:
      return null;
  }
};

/** 服务端的 PEQ 文档（snake_case）转成界面用的形状。 */
export const peqDocumentDecoder: d.Decoder<PeqDocument> = (input, path) => {
  const raw = d.object({
    enabled: d.boolean(),
    preamp_db: d.number(),
    bands: d.array(
      d.object({
        type: bandTypeDecoder,
        freq_hz: d.number(),
        gain_db: d.optional(d.number()),
        q: d.optional(d.number()),
        slope_db: d.optional(d.integer()),
        channels: channelsDecoder,
        enabled: d.boolean(),
      }),
      { max: PEQ_MAX_BANDS },
    ),
    source: d.optional(sourceDecoder),
  })(input, path);
  return {
    enabled: raw.enabled,
    preampDb: raw.preamp_db,
    bands: raw.bands.map((band) => ({ type: band.type, freqHz: band.freq_hz, gainDb: band.gain_db ?? null, q: band.q ?? null, slopeDb: band.slope_db ?? null, channels: band.channels, enabled: band.enabled })),
    source: raw.source ?? null,
  };
};

export type PeqWire = {
  enabled: boolean;
  preamp_db: number;
  bands: { type: PeqBandType; freq_hz: number; gain_db?: number; q?: number; slope_db?: number; channels: PeqChannels; enabled: boolean }[];
  source?: Record<string, string>;
};

/** 界面的 PEQ 文档转成请求体；只带当前类型用得到的参数。 */
export function peqToWire(peq: PeqDocument): PeqWire {
  return {
    enabled: peq.enabled,
    preamp_db: peq.preampDb,
    bands: peq.bands.map((band) => ({
      type: band.type,
      freq_hz: band.freqHz,
      ...(bandHasGain(band.type) && band.gainDb !== null ? { gain_db: band.gainDb } : {}),
      ...(band.q !== null && (!bandHasSlope(band.type) || band.slopeDb === 12) ? { q: band.q } : {}),
      ...(bandHasSlope(band.type) && band.slopeDb !== null ? { slope_db: band.slopeDb } : {}),
      channels: band.channels,
      enabled: band.enabled,
    })),
    ...(peq.source === null ? {} : { source: sourceToWire(peq.source) }),
  };
}

function sourceToWire(source: PeqSource): Record<string, string> {
  switch (source.kind) {
    case "opra":
      return { kind: "opra", eq_id: source.eqId, vendor: source.vendor, product: source.product, author: source.author, details: source.details, link: source.link };
    case "preset":
      return { kind: "preset", preset_id: source.presetId, name: source.name };
    case "import":
      return { kind: "import" };
  }
}

/** 两份文档内容相同（比较请求体）。 */
export function samePeq(a: PeqDocument, b: PeqDocument): boolean {
  return JSON.stringify(peqToWire(a)) === JSON.stringify(peqToWire(b));
}
