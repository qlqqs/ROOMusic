import { decode as d } from "../../../shared/api";

// 发行字段修正（M3）的响应解码。可修正的字段与后端 backend/internal/catalog/corrections.go 的 correctableFields 一致，
// 顺序也是接口返回的顺序。后端新增可修正字段时，样本契约测试先失败，在这里补上才算确认前端已经知道。
export const CORRECTABLE_FIELDS = ["title", "album_artist", "year", "genre", "label", "catalog_number", "edition", "barcode"] as const;
export type CorrectableField = (typeof CORRECTABLE_FIELDS)[number];

const MAX_FIELD_TEXT = 4096; // 修正值的字节上限（后端 maxCorrectionTextBytes）；扫描值同样有界

// 年份是 1 到 9999 的整数，其余字段是字符串；按值的运行时类型选择解码器，字段与类型的对应由后端保证。
const fieldValueDecoder: d.Decoder<string | number> = (input, path) =>
  typeof input === "number" ? d.integer({ min: 1, max: 9999 })(input, path) : d.string({ max: MAX_FIELD_TEXT })(input, path);

// 当前的修正意图：set 带值，null 表示明确设为空（没有 value）。没有修正时整个 correction 为 null。
const correctionDecoder = d.object({
  mode: d.oneOf(["set", "null"] as const),
  value: d.optional(fieldValueDecoder),
});

const correctionFieldDecoder = d.object({
  field: d.oneOf(CORRECTABLE_FIELDS),
  scan_value: d.nullable(fieldValueDecoder),
  correction: d.nullable(correctionDecoder),
  effective_value: d.nullable(fieldValueDecoder),
  uncertain: d.boolean(),
});

const correctionStateShape = {
  release_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  fields: d.array(correctionFieldDecoder, { max: CORRECTABLE_FIELDS.length }),
};

const correctionStateDecoder = d.object(correctionStateShape);
// 修正成功的响应：与读取相同的状态，加上这次操作的 ID（撤销用）。
const correctionResultDecoder = d.object({ ...correctionStateShape, operation_id: d.uuid() });

// 人工归组成功的响应：发行现在所在的组、原来的组、新的人工修改版本号与这次操作的 ID（撤销用）。
const groupChangeResultDecoder = d.object({
  release_id: d.uuid(),
  group_id: d.uuid(),
  previous_group_id: d.uuid(),
  revision: d.integer({ min: 1 }),
  operation_id: d.uuid(),
});

export const decodeCorrectionState = d.defineDecoder(correctionStateDecoder);
export const decodeCorrectionResult = d.defineDecoder(correctionResultDecoder);
export const decodeGroupChangeResult = d.defineDecoder(groupChangeResultDecoder);

export type CorrectionIntentDTO = d.Infer<typeof correctionDecoder>;
export type CorrectionFieldDTO = d.Infer<typeof correctionFieldDecoder>;
export type CorrectionStateDTO = d.Infer<typeof correctionStateDecoder>;
export type CorrectionResultDTO = d.Infer<typeof correctionResultDecoder>;
export type GroupChangeResultDTO = d.Infer<typeof groupChangeResultDecoder>;
