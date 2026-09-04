/* global URL, URLSearchParams */
export type ReleaseFilterState = {
  query: string;
  attentionRequired: boolean;
  page: number;
  release: string | null;
};

function readPage(value: string | null): number {
  if (value === null || !/^\d+$/.test(value)) return 1;
  const page = Number(value);
  return Number.isSafeInteger(page) && page >= 1 ? page : 1;
}

export function readReleaseFilters(search: string): ReleaseFilterState {
  const parameters = new URLSearchParams(search);
  const release = parameters.get("release")?.trim() ?? "";
  return {
    query: parameters.get("q")?.trim() ?? "",
    attentionRequired: parameters.get("attention") === "required",
    page: readPage(parameters.get("page")),
    release: release === "" ? null : release,
  };
}

export function clampReleasePage(page: number, total: number, pageSize: number): number {
  if (!Number.isSafeInteger(page) || page < 1) return 1;
  if (!Number.isSafeInteger(total) || total < 0) return 1;
  if (!Number.isSafeInteger(pageSize) || pageSize < 1) return 1;
  return Math.min(page, Math.max(1, Math.ceil(total / pageSize)));
}

export function releaseFilterURL(currentURL: string, filters: ReleaseFilterState): string {
  const url = new URL(currentURL);
  const query = filters.query.trim();
  if (query) url.searchParams.set("q", query);
  else url.searchParams.delete("q");
  if (filters.attentionRequired) url.searchParams.set("attention", "required");
  else url.searchParams.delete("attention");
  if (Number.isSafeInteger(filters.page) && filters.page > 1) url.searchParams.set("page", String(filters.page));
  else url.searchParams.delete("page");
  const release = filters.release?.trim() ?? "";
  if (release) url.searchParams.set("release", release);
  else url.searchParams.delete("release");
  return url.toString();
}
