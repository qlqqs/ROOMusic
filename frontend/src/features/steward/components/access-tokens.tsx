import { KeyRound, Plus } from "lucide-react";
import { useState } from "react";
import { Button, ConfirmDialog, EmptyState, FailureNotice, Notice, QueryContent, SettingsGroup, Skeleton } from "../../../shared/ui";
import { MAX_TOKENS, type TokenDTO } from "../api/decoders";
import { useAccessTokens, useCreateToken, useRevokeToken } from "../hooks/use-access-tokens";
import { CreateTokenDialog } from "./create-token-dialog";
import { tokenFailureMessages } from "./token-messages";
import { TokenTable } from "./token-table";

/**
 * 设置页“访问令牌”的内容：自己的令牌列表、新建（对话框，成功后显示一次明文）与吊销（先确认）。
 * 明文只存在于签发请求的返回值里，关闭对话框时随请求状态一起丢弃。
 * 管理员关闭成员使用管家时，成员仍能看到并吊销已有的令牌，但不能新建。
 */
export function AccessTokens() {
  const tokens = useAccessTokens();
  const create = useCreateToken();
  const revoke = useRevokeToken();
  const [creating, setCreating] = useState(false);
  const [revoking, setRevoking] = useState<TokenDTO | null>(null);
  const available = tokens.data?.available ?? false;
  const full = (tokens.data?.tokens.length ?? 0) >= MAX_TOKENS;

  const closeCreate = () => {
    setCreating(false);
    create.reset();
  };

  return (
    <>
      <SettingsGroup
        title="我的令牌"
        action={
          <Button
            variant="primary"
            icon={<Plus size={16} />}
            disabled={!available || full}
            onClick={() => {
              revoke.reset();
              setCreating(true);
            }}
          >
            新建令牌
          </Button>
        }
      >
        {revoke.isSuccess ? (
          <Notice tone="success" action={<Button onClick={revoke.reset}>关闭</Button>}>
            已吊销令牌“{revoke.variables.name}”，用它的客户端不能再连接。
          </Notice>
        ) : null}
        {revoke.isError && revoking === null ? (
          <FailureNotice error={revoke.error} context="write" messages={tokenFailureMessages} title="没有吊销成功" action={<Button onClick={revoke.reset}>关闭</Button>} />
        ) : null}
        <QueryContent query={tokens} loading={<Skeleton label="正在加载令牌" lines={3} />}>
          {(data) => (
            <>
              {data.available ? null : <Notice tone="warning">管理员已关闭成员使用管家，已有的令牌暂时不能使用，也不能新建。</Notice>}
              {data.available && data.tokens.length >= MAX_TOKENS ? <Notice>令牌数量已到上限（{MAX_TOKENS} 个），吊销不用的令牌后才能新建。</Notice> : null}
              {data.tokens.length === 0 ? (
                <EmptyState icon={<KeyRound size={24} />} title="还没有令牌">
                  新建一个令牌，就可以让 Claude Code 等 MCP 客户端以你的身份查看曲库。
                </EmptyState>
              ) : (
                <TokenTable
                  tokens={data.tokens}
                  // 以列表取回的时刻为准判断是否过期：渲染保持纯函数，刷新列表时随之更新。
                  now={tokens.dataUpdatedAt}
                  busy={revoke.isPending}
                  onRevoke={(token) => {
                    revoke.reset();
                    setRevoking(token);
                  }}
                />
              )}
            </>
          )}
        </QueryContent>
      </SettingsGroup>
      <CreateTokenDialog
        open={creating}
        pending={create.isPending}
        error={create.isError ? create.error : null}
        created={create.data ?? null}
        onSubmit={(input) => create.mutate(input)}
        onClose={closeCreate}
      />
      <ConfirmDialog
        open={revoking !== null}
        title={`吊销令牌“${revoking?.name ?? ""}”？`}
        confirmLabel="吊销令牌"
        pending={revoke.isPending}
        onConfirm={() => {
          if (revoking === null) return;
          const done = () => setRevoking(null);
          revoke.mutate({ id: revoking.id, name: revoking.name }, { onSuccess: done, onError: done });
        }}
        onCancel={() => setRevoking(null)}
      >
        <p>用这个令牌的客户端会马上连不上，吊销后不能恢复，需要时只能新建一个。</p>
      </ConfirmDialog>
    </>
  );
}
