import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";

// 后端集成测试 TestPostgreSQLAPIResponseSamples 用真实 handler 写出的响应样本；这里是前端记录该路径的唯一位置。
// 样本是 {"status": 状态码, "body": 响应体}，UUID 与时间已规范化为固定值。
const samplesDirectory = resolve(import.meta.dirname, "../../../backend/internal/app/testdata/api-samples");

export type ApiSample = { name: string; status: number; body: unknown };

/** 全部样本的名字（如 "auth/login"），已排序。 */
export function listApiSampleNames(): string[] {
  return readdirSync(samplesDirectory, { recursive: true, encoding: "utf8" })
    .filter((entry) => entry.endsWith(".json"))
    .map((entry) => entry.slice(0, -".json".length).split("\\").join("/"))
    .sort();
}

/** 原始样本也包括请求与 SSE 事件，不强制为 HTTP JSON 响应结构。 */
export function loadRawApiSample(name: string): unknown {
  return JSON.parse(readFileSync(`${samplesDirectory}/${name}.json`, "utf8"));
}

export function loadApiSample(name: string): ApiSample {
  const parsed = loadRawApiSample(name);
  if (typeof parsed !== "object" || parsed === null || !("status" in parsed) || !("body" in parsed) || typeof parsed.status !== "number") {
    throw new Error(`样本 ${name} 不是 {status, body} 结构`);
  }
  return { name, status: parsed.status, body: parsed.body };
}

/** 取样本的响应体，作为 decoder 测试的正例与反例的基础。 */
export function sampleBody(name: string): unknown {
  return loadApiSample(name).body;
}
