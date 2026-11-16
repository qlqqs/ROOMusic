import { useId, useRef, useState, type FormEvent, type RefObject } from "react";
import { Button, FailureNotice, ModalDialog, Segmented, TextField, type SegmentedOption } from "../../../shared/ui";
import type { CreatedTokenDTO, TokenScope } from "../api/decoders";
import type { NewToken } from "../api/endpoints";
import { CreatedToken } from "./created-token";
import styles from "./token-dialog.module.css";
import { scopeLabels, tokenFailureMessages } from "./token-messages";

type CreateTokenDialogProps = {
  open: boolean;
  pending: boolean;
  /** 上一次签发的错误；没有时是 null。 */
  error: unknown;
  /** 刚签发的令牌（含明文）；有它时对话框显示明文而不是表单。 */
  created: CreatedTokenDTO | null;
  onSubmit: (input: NewToken) => void;
  /** 关闭对话框；调用方同时丢弃明文和错误。 */
  onClose: () => void;
};

const scopeOptions: readonly SegmentedOption<TokenScope>[] = [
  { value: "read", label: scopeLabels.read },
  { value: "read_write", label: scopeLabels.read_write },
];

/** 有效期按天数选；0 表示不过期。 */
const lifetimeOptions: readonly SegmentedOption<number>[] = [
  { value: 30, label: "30 天" },
  { value: 90, label: "90 天" },
  { value: 365, label: "1 年" },
  { value: 0, label: "不过期" },
];

const DAY = 24 * 60 * 60 * 1000;
const MAX_NAME = 64;

/**
 * 新建令牌的对话框：先填名称、权限与有效期；签发成功后同一个对话框改为显示明文和接入命令，明文只显示这一次。
 * 提交中 Esc 与“取消”暂时无效；显示明文时只能点“完成”关闭，避免误按 Esc 丢掉还没复制的令牌。
 */
export function CreateTokenDialog({ open, pending, error, created, onSubmit, onClose }: CreateTokenDialogProps) {
  const titleId = useId();
  const headingRef = useRef<HTMLHeadingElement>(null);
  return (
    <ModalDialog
      open={open}
      titleId={titleId}
      initialFocus={headingRef}
      className={styles.dialog}
      onRequestClose={() => {
        if (!pending && created === null) onClose();
      }}
    >
      {created === null ? (
        <CreateTokenForm titleId={titleId} headingRef={headingRef} pending={pending} error={error} onSubmit={onSubmit} onCancel={onClose} />
      ) : (
        <CreatedToken titleId={titleId} token={created} onDone={onClose} />
      )}
    </ModalDialog>
  );
}

type CreateTokenFormProps = {
  titleId: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
  pending: boolean;
  error: unknown;
  onSubmit: (input: NewToken) => void;
  onCancel: () => void;
};

function CreateTokenForm({ titleId, headingRef, pending, error, onSubmit, onCancel }: CreateTokenFormProps) {
  const [name, setName] = useState("");
  const [scope, setScope] = useState<TokenScope>("read");
  const [days, setDays] = useState(90);
  const [nameError, setNameError] = useState<string | null>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const scopeLabelId = useId();
  const scopeHintId = useId();
  const lifetimeLabelId = useId();

  function submit(event: FormEvent) {
    event.preventDefault();
    if (pending) return;
    const trimmed = name.trim();
    if (trimmed === "" || [...trimmed].length > MAX_NAME) {
      setNameError(tokenFailureMessages.invalid_input);
      nameRef.current?.focus();
      return;
    }
    onSubmit({ name: trimmed, scope, expires_at: days === 0 ? null : new Date(Date.now() + days * DAY).toISOString() });
  }

  return (
    <form className={styles.content} onSubmit={submit} noValidate>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        新建访问令牌
      </h2>
      <div className={styles.fields}>
        <TextField
          ref={nameRef}
          label="名称"
          autoComplete="off"
          description="用来认出这个令牌用在哪里，例如“书房电脑上的 Claude Code”。"
          value={name}
          error={nameError}
          onChange={(event) => {
            setName(event.target.value);
            setNameError(null);
          }}
        />
        <div className={styles.choice}>
          <span id={scopeLabelId} className={styles.choiceLabel}>
            权限
          </span>
          <Segmented options={scopeOptions} value={scope} onChange={setScope} disabled={pending} labelId={scopeLabelId} descriptionId={scopeHintId} />
          <p id={scopeHintId} className={styles.hint}>
            {scope === "read"
              ? "只能查看曲库和你的个人数据。"
              : "还可以执行修改操作，修改直接生效，ROOMusic 不会再向你确认。目前提供的工具都是只读的。"}
          </p>
        </div>
        <div className={styles.choice}>
          <span id={lifetimeLabelId} className={styles.choiceLabel}>
            有效期
          </span>
          <Segmented options={lifetimeOptions} value={days} onChange={setDays} disabled={pending} labelId={lifetimeLabelId} />
        </div>
      </div>
      {error === null ? null : <FailureNotice error={error} context="write" messages={tokenFailureMessages} title="没有创建成功" />}
      <div className={styles.actions}>
        <Button onClick={onCancel} disabled={pending}>
          取消
        </Button>
        <Button type="submit" variant="primary" busy={pending}>
          {pending ? "正在创建…" : "创建"}
        </Button>
      </div>
    </form>
  );
}
