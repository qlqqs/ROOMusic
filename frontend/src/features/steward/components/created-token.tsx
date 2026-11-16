import { useEffect, useId, useRef, useState } from "react";
import { Button, Notice } from "../../../shared/ui";
import type { CreatedTokenDTO } from "../api/decoders";
import styles from "./token-dialog.module.css";

type CreatedTokenProps = {
  titleId: string;
  token: CreatedTokenDTO;
  onDone: () => void;
};

/** Claude Code 添加这个 MCP 服务的命令；地址取当前页面的来源。 */
export function connectCommand(origin: string, token: string): string {
  return `claude mcp add --transport http roomusic ${origin}/api/v1/mcp --header "Authorization: Bearer ${token}"`;
}

/**
 * 签发成功后显示一次明文与接入命令。两段文字都放在只读文本框里，点进去就全选，方便在不能用剪贴板接口的环境
 * （经 http 访问局域网里的实例时浏览器不提供）手动复制；能用时另有“复制”按钮。
 */
export function CreatedToken({ titleId, token, onDone }: CreatedTokenProps) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => headingRef.current?.focus(), []);
  return (
    <div className={styles.content}>
      <h2 id={titleId} ref={headingRef} tabIndex={-1} className={styles.title}>
        令牌“{token.name}”已创建
      </h2>
      <Notice tone="warning">令牌只显示这一次，关闭后不能再查看。现在复制并保存好。</Notice>
      <CopyField label="令牌" value={token.token} rows={2} />
      <CopyField label="在 Claude Code 里接入" value={connectCommand(window.location.origin, token.token)} rows={4} />
      <div className={styles.actions}>
        <Button variant="primary" onClick={onDone}>
          完成
        </Button>
      </div>
    </div>
  );
}

function CopyField({ label, value, rows }: { label: string; value: string; rows: number }) {
  const id = useId();
  const [copied, setCopied] = useState(false);
  const canCopy = typeof navigator !== "undefined" && navigator.clipboard !== undefined;
  return (
    <div className={styles.secretRow}>
      <label htmlFor={id} className={styles.secretLabel}>
        {label}
      </label>
      <textarea id={id} className={styles.secret} readOnly rows={rows} value={value} spellCheck={false} onFocus={(event) => event.target.select()} />
      {canCopy ? (
        <Button
          onClick={() => {
            void navigator.clipboard.writeText(value).then(() => setCopied(true));
          }}
        >
          {copied ? "已复制" : `复制${label === "令牌" ? "令牌" : "命令"}`}
        </Button>
      ) : null}
    </div>
  );
}
