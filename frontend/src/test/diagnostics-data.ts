import { loadApiSample } from "./api-samples";

function clientErrorInputError(body: unknown): string | null {
  if (typeof body !== "object" || body === null || Array.isArray(body)) return "errors/personal-invalid-json";
  if (Object.keys(body).some((key) => !["kind", "name", "message", "route"].includes(key))) return "errors/personal-invalid-json";
  if (
    !("kind" in body) || !["error", "unhandledrejection", "render"].some((kind) => kind === body.kind) ||
    !("name" in body) || typeof body.name !== "string" || !/^[A-Za-z][A-Za-z0-9_$]{0,63}$/.test(body.name) ||
    !("message" in body) || typeof body.message !== "string" || [...body.message].length > 500 ||
    !("route" in body) || typeof body.route !== "string" || !/^\/[A-Za-z0-9_$./-]{0,199}$/.test(body.route)
  ) return "diagnostics/invalid-client-error";
  return null;
}

/** Vitest 与 Playwright 共用的上报替身。浏览器来源由 Playwright 适配器校验。 */
export function clientErrorResponder() {
  const acceptedByUser = new Map<string, number[]>();
  return (userId: string | null, body: unknown, invalidJSON: boolean) => {
    if (userId === null) return loadApiSample("errors/unauthorized");
    const inputError = invalidJSON ? "errors/personal-invalid-json" : clientErrorInputError(body);
    if (inputError !== null) return loadApiSample(inputError);

    const now = Date.now();
    const recent = (acceptedByUser.get(userId) ?? []).filter((time) => time > now - 10 * 60_000);
    if (recent.length >= 20) return loadApiSample("diagnostics/client-errors-throttled");
    acceptedByUser.set(userId, [...recent, now]);
    return { status: 204, body: null };
  };
}
