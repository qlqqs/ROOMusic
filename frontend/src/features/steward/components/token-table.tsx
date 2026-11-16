import { formatDate, formatDateTime } from "../../../shared/lib/time";
import { Button, DataTable, StatusTag, VisuallyHidden, type Column } from "../../../shared/ui";
import type { TokenDTO } from "../api/decoders";
import { scopeLabels } from "./token-messages";

type TokenTableProps = {
  tokens: readonly TokenDTO[];
  /** 当前时间（毫秒），用来判断是否已过期。 */
  now: number;
  /** 有吊销在进行中：各行的“吊销”暂时停用。 */
  busy: boolean;
  onRevoke: (token: TokenDTO) => void;
};

/** 令牌列表：名称、权限、末四位、创建与过期时间、上次使用时间，行尾是“吊销”。已过期的令牌仍列出，方便清理。 */
export function TokenTable({ tokens, now, busy, onRevoke }: TokenTableProps) {
  const columns: Column<TokenDTO>[] = [
    { id: "name", header: "名称", rowHeader: true, cell: (token) => token.name },
    { id: "scope", header: "权限", cell: (token) => <StatusTag>{scopeLabels[token.scope]}</StatusTag> },
    { id: "tail", header: "末四位", cell: (token) => <code>{token.tail}</code> },
    { id: "created", header: "创建时间", cell: (token) => formatDate(token.created_at) },
    {
      id: "expires",
      header: "过期时间",
      cell: (token) => {
        if (token.expires_at === null) return "不过期";
        return Date.parse(token.expires_at) <= now ? <StatusTag tone="outline">已过期</StatusTag> : formatDate(token.expires_at);
      },
    },
    { id: "used", header: "上次使用", cell: (token) => (token.last_used_at === null ? "从未使用" : formatDateTime(token.last_used_at)) },
    {
      id: "actions",
      header: "操作",
      headerContent: <VisuallyHidden>操作</VisuallyHidden>,
      hideLabelWhenStacked: true,
      cell: (token) => (
        <Button disabled={busy} aria-label={`吊销令牌 ${token.name}`} onClick={() => onRevoke(token)}>
          吊销…
        </Button>
      ),
    },
  ];
  return <DataTable caption="访问令牌" columns={columns} rows={tokens} rowKey={(token) => token.id} />;
}
