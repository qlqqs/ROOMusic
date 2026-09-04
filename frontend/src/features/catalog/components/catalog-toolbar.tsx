import { FormEvent } from "react";

export type CatalogViewMode = "grid" | "list";

type CatalogToolbarProps = {
  searchInput: string;
  submittedQuery: string;
  onSearchInput: (value: string) => void;
  onSubmitSearch: () => void;
  onClearSearch: () => void;
  attentionRequired: boolean;
  onToggleAttention: () => void;
  viewMode: CatalogViewMode;
  onViewModeChange: (mode: CatalogViewMode) => void;
  onRefresh: () => void;
  pending: boolean;
  stale: boolean;
  total: number;
  page: number;
  pageCount: number;
  onPageChange: (page: number) => void;
};

// 工具栏只发出意图；搜索提交（回车等效）、清除、attention、视图切换、
// 刷新和分页的实际状态变更由 App 协调。
export function CatalogToolbar(props: CatalogToolbarProps) {
  function submitSearch(event: FormEvent) {
    event.preventDefault();
    props.onSubmitSearch();
  }
  return (
    <div className="catalog-toolbar">
      <form className="searchbar" onSubmit={submitSearch} role="search">
        <span aria-hidden="true">⌕</span>
        <label className="sr-only" htmlFor="library-search">搜索音乐库</label>
        <input
          id="library-search"
          value={props.searchInput}
          onChange={(event) => props.onSearchInput(event.target.value)}
          placeholder="搜索艺术家、专辑或曲目"
        />
        {props.submittedQuery && (
          <button type="button" onClick={props.onClearSearch}>清除</button>
        )}
      </form>
      <div className="toolbar-actions">
        <button
          className="filter-toggle"
          type="button"
          aria-pressed={props.attentionRequired}
          onClick={props.onToggleAttention}
        >
          {props.attentionRequired ? "显示全部" : "只看需要检查"}
        </button>
        <div className="view-toggle" role="group" aria-label="切换列表视图">
          <button
            type="button"
            aria-pressed={props.viewMode === "grid"}
            aria-label="网格视图"
            onClick={() => props.onViewModeChange("grid")}
          >
            ▦
          </button>
          <button
            type="button"
            aria-pressed={props.viewMode === "list"}
            aria-label="列表视图"
            onClick={() => props.onViewModeChange("list")}
          >
            ☰
          </button>
        </div>
        <button
          className="filter-toggle"
          type="button"
          onClick={props.onRefresh}
          disabled={props.pending}
          aria-busy={props.pending}
        >
          {props.pending ? "刷新中…" : "刷新"}
        </button>
        <span className="result-count">{props.total} 个发行版本</span>
        {props.stale && <span className="stale-indicator" role="status">正在刷新…</span>}
      </div>
      {(props.page > 1 || props.pageCount > 1) && (
        <nav className="pagination" aria-label="发行版本分页">
          <button
            type="button"
            onClick={() => props.onPageChange(props.page - 1)}
            disabled={props.pending || props.page <= 1}
          >
            上一页
          </button>
          <span>第 {props.page} / {props.pageCount} 页</span>
          <button
            type="button"
            onClick={() => props.onPageChange(props.page + 1)}
            disabled={props.pending || props.page >= props.pageCount}
          >
            下一页
          </button>
        </nav>
      )}
    </div>
  );
}
