import { Plus } from "lucide-react";
import { useId, useRef, useState, type FormEvent } from "react";
import { formatDate } from "../../../shared/lib/time";
import { Button, ConfirmDialog, DataTable, FailureNotice, ModalDialog, QueryContent, SettingRow, SettingsGroup, Skeleton, Switch, TextField, VisuallyHidden, type Column } from "../../../shared/ui";
import type { AllowlistEntryDTO } from "../api/decoders";
import { useAddAllowlistEntry, useAllowlist, useInstanceSettings, useRemoveAllowlistEntry, useSetMembersEnabled } from "../hooks/use-agent-settings";
import styles from "./settings.module.css";

/** 管理员的“成员”一段：成员开关与成员可以连接的内网地址。后端接口本身要求管理员，改动立即生效。 */
export function MembersSection() {
  return (
    <>
      <MembersSwitch />
      <Allowlist />
    </>
  );
}

function MembersSwitch() {
  const instance = useInstanceSettings();
  const setEnabled = useSetMembersEnabled();
  return (
    <SettingsGroup title="成员">
      <QueryContent query={instance} loading={<Skeleton label="正在读取成员开关" lines={1} />}>
        {(data) => (
          <SettingRow label="允许普通成员使用 Agent" description="成员用自己的密钥，只能做自己有权限的事。关闭后成员的对话和访问令牌都暂停。">
            {({ controlId, descriptionId }) => (
              <Switch
                id={controlId}
                aria-describedby={descriptionId}
                checked={setEnabled.isPending ? setEnabled.variables : data.members_enabled}
                disabled={setEnabled.isPending}
                onChange={(checked) => setEnabled.mutate(checked)}
              />
            )}
          </SettingRow>
        )}
      </QueryContent>
      {setEnabled.isError ? <FailureNotice error={setEnabled.error} context="write" title="成员开关没有改成" /> : null}
    </SettingsGroup>
  );
}

/** 放行名单：主机按域名或 IP（不带端口与方括号），端口 1–65535。 */
function validHost(host: string): boolean {
  // 只有一个冒号是“主机:端口”的写法；IPv6 至少有两个冒号。
  return host !== "" && !/[\s/[\]]/.test(host) && host.split(":").length !== 2;
}

function Allowlist() {
  const list = useAllowlist();
  const add = useAddAllowlistEntry();
  const remove = useRemoveAllowlistEntry();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<AllowlistEntryDTO | null>(null);
  const columns: Column<AllowlistEntryDTO>[] = [
    { id: "host", header: "主机", rowHeader: true, cell: (entry) => entry.host },
    { id: "port", header: "端口", cell: (entry) => String(entry.port) },
    { id: "added", header: "添加时间", cell: (entry) => formatDate(entry.created_at) },
    {
      id: "actions",
      header: "操作",
      headerContent: <VisuallyHidden>操作</VisuallyHidden>,
      hideLabelWhenStacked: true,
      cell: (entry) => (
        <Button
          aria-label={`移除 ${entry.host}:${entry.port}`}
          disabled={remove.isPending}
          onClick={() => {
            remove.reset();
            setRemoving(entry);
          }}
        >
          移除…
        </Button>
      ),
    },
  ];
  return (
    <>
      <SettingsGroup
        title="成员可以连接的内网地址"
        action={
          <Button
            variant="primary"
            icon={<Plus size={16} />}
            onClick={() => {
              add.reset();
              setAdding(true);
            }}
          >
            添加地址
          </Button>
        }
      >
        <p className={styles.note}>成员的模型接口默认只能是公网地址。家里自己部署的模型在这里放行，管理员自己不受这个限制。</p>
        <QueryContent query={list} loading={<Skeleton label="正在读取地址" lines={2} />}>
          {(items) =>
            items.length === 0 ? (
              <p className={styles.muted}>还没有放行的地址。</p>
            ) : (
              <DataTable caption="成员可以连接的内网地址" columns={columns} rows={items} rowKey={(entry) => `${entry.host}:${entry.port}`} />
            )
          }
        </QueryContent>
        {remove.isError && removing === null ? <FailureNotice error={remove.error} context="write" title="没有移除" /> : null}
      </SettingsGroup>
      {/* 每次打开都是新的表单。 */}
      {adding ? (
        <AddAddressDialog pending={add.isPending} error={add.isError ? add.error : null} onClose={() => setAdding(false)} onSubmit={(entry) => add.mutate(entry, { onSuccess: () => setAdding(false) })} />
      ) : null}
      <ConfirmDialog
        open={removing !== null}
        title={`移除 ${removing?.host ?? ""}:${removing?.port ?? ""}？`}
        confirmLabel="移除"
        pending={remove.isPending}
        onCancel={() => setRemoving(null)}
        onConfirm={() => {
          if (removing === null) return;
          const done = () => setRemoving(null);
          remove.mutate({ host: removing.host, port: removing.port }, { onSuccess: done, onError: done });
        }}
      >
        <p>移除后，成员不能再连接这个地址上的模型接口；正在用它的成员下一次发消息会被拒绝。</p>
      </ConfirmDialog>
    </>
  );
}

function AddAddressDialog({
  pending,
  error,
  onClose,
  onSubmit,
}: {
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSubmit: (entry: { host: string; port: number }) => void;
}) {
  const titleId = useId();
  const hostRef = useRef<HTMLInputElement>(null);
  const [host, setHost] = useState("");
  const [port, setPort] = useState("");
  const [touched, setTouched] = useState(false);
  const hostValue = host.trim();
  const portValue = Number(port);
  const hostError = touched && !validHost(hostValue) ? "填主机名或 IP，不带端口和方括号，例如 192.168.1.20。" : null;
  const portError = touched && !(Number.isInteger(portValue) && portValue >= 1 && portValue <= 65535) ? "端口是 1 到 65535 之间的整数。" : null;
  const submit = (event: FormEvent) => {
    event.preventDefault();
    setTouched(true);
    if (!validHost(hostValue) || !(Number.isInteger(portValue) && portValue >= 1 && portValue <= 65535)) return;
    onSubmit({ host: hostValue, port: portValue });
  };
  const close = () => {
    if (!pending) onClose();
  };
  return (
    <ModalDialog open titleId={titleId} initialFocus={hostRef} className={styles.dialog} onRequestClose={close}>
      <form className={styles.dialogContent} onSubmit={submit} noValidate>
        <h2 id={titleId} className={styles.dialogTitle}>
          添加成员可以连接的地址
        </h2>
        <TextField ref={hostRef} label="主机" value={host} autoComplete="off" spellCheck={false} error={hostError} onChange={(event) => setHost(event.target.value)} />
        <TextField label="端口" value={port} inputMode="numeric" autoComplete="off" error={portError} onChange={(event) => setPort(event.target.value)} />
        {error === null ? null : (
          <FailureNotice error={error} context="write" title="没有添加" messages={{ invalid_input: "主机或端口不合要求。" }} />
        )}
        <div className={styles.dialogActions}>
          <Button onClick={close} disabled={pending}>
            取消
          </Button>
          <Button type="submit" variant="primary" busy={pending}>
            添加
          </Button>
        </div>
      </form>
    </ModalDialog>
  );
}
