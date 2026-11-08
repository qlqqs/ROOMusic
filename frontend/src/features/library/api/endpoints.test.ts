import { describe, expect, it } from "vitest";
import { decodeRootChanged, decodeScan } from "./decoders";
import { disableRoot, relocateRoot, restoreRoot, startScan } from "./endpoints";
import { loadApiSample } from "../../../test/api-samples";
import { sampleResponse, stubFetch } from "../../../test/http";

const rootId = "00000000-0000-4000-8000-000000000001";
const idempotencyKey = "00000000-0000-4000-8000-000000000002";

// 三个写端点共用四字段命令收据；完整目录（含 excluded_paths）由成功后的列表刷新读取。
describe("目录写端点的真实响应样本", () => {
  it.each([
    { name: "disable", method: "PATCH", path: `/api/v1/library-roots/${rootId}`, send: () => disableRoot(rootId, 1, idempotencyKey) },
    { name: "restore", method: "POST", path: `/api/v1/library-roots/${rootId}/restore`, send: () => restoreRoot(rootId, 2, idempotencyKey) },
    { name: "relocate", method: "POST", path: `/api/v1/library-roots/${rootId}/relocate`, send: () => relocateRoot(rootId, "/music/new-location", 3, idempotencyKey) },
  ])("$name 的真实响应可经端点 decodeRootChanged 解码", async ({ name, method, path, send }) => {
    const sample = loadApiSample(`library-roots/${name}`);
    const requests = stubFetch(() => sampleResponse(`library-roots/${name}`));
    const response = await send();

    expect(response.status).toBe(sample.status);
    expect(response.data).toEqual(decodeRootChanged(sample.body));
    expect(response.data).toMatchObject({ id: expect.any(String), path: expect.any(String), status: expect.any(String), revision: expect.any(Number) });
    expect(requests[0]?.method).toBe(method);
    expect(requests[0]?.path).toBe(path);
  });
});

const scanRequests = [
  { name: "默认增量", send: () => startScan(), body: undefined },
  { name: "显式增量", send: () => startScan("incremental"), body: undefined },
  { name: "完整重扫", send: () => startScan("full"), body: { mode: "full" } },
] as const;

describe.each(scanRequests)("开始扫描端点：$name", ({ send, body }) => {
  it.each(["scans/start-created", "scans/start-existing"])("发送约定请求并保留 %s 的状态码与响应模式", async (sampleName) => {
    const sample = loadApiSample(sampleName);
    const requests = stubFetch(() => sampleResponse(sampleName));
    const response = await send();

    expect(response).toEqual({ status: sample.status, data: decodeScan(sample.body) });
    expect(requests).toHaveLength(1);
    const [request] = requests;
    expect(request?.method).toBe("POST");
    expect(request?.path).toBe("/api/v1/scans");
    expect(request?.body).toEqual(body);
    expect(request?.init.body).toBe(body === undefined ? undefined : JSON.stringify(body));
    expect(request?.headers.get("Content-Type")).toBe(body === undefined ? null : "application/json");
  });
});
