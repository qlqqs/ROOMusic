import { failureOf, isHttpFailure } from "../../../shared/api";
import { Button, FailureNotice, Notice, Skeleton } from "../../../shared/ui";
import type { ClaimDetailDTO, ClaimPreviewDTO } from "../api/decoders";
import { claimActionLabel, claimItemText } from "../claim-text";
import { useClaimPreview } from "../hooks/use-claims";
import type { ReleaseCommands } from "../hooks/use-release-commands";
import styles from "./claim-preview.module.css";

type ClaimPreviewProps = {
  detail: ClaimDetailDTO;
  /** 选中的新版本；没有时不显示预览。 */
  targetId: string | undefined;
  commands: ReleaseCommands;
};

function PreviewItems({ preview, detail }: { preview: ClaimPreviewDTO; detail: ClaimDetailDTO }) {
  return (
    <ul className={styles.items} aria-label="认领会怎样处理旧版本上的修改">
      {preview.items.map((item, index) => {
        const text = claimItemText(item, detail);
        return (
          <li key={`${item.kind}-${item.field ?? item.issue ?? item.group ?? index}`} className={styles.item} data-action={item.action}>
            <span className={styles.action}>{claimActionLabel(item)}</span>
            <span className={styles.text}>
              <span className={styles.heading}>{text.heading}</span>
              <span>{text.body}</span>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * 认领的预览与提交：选中新版本之后逐项说明旧版本上的修改会怎么处理（迁移、因为新版本已有而保留、默认版本改回自动选择、缺失曲目的核对不迁移，
 * 以及迁过去的核对在新版本上是否立即生效），按钮是“认领到《…》”。预览就是确认，不弹二次确认；结果也可以撤销。
 * 预览带令牌与两个版本的版本号，提交时一并带回：页面打开之后又扫描或修改过，后端拒绝（claim_changed、revision_conflict），预览随之重新读取。
 */
export function ClaimPreview({ detail, targetId, commands }: ClaimPreviewProps) {
  const preview = useClaimPreview(detail.release.id, targetId);
  const pending = commands.state.status === "pending" && commands.state.command.kind === "claim";
  const uncertain = commands.state.status === "failed" && commands.state.command.kind === "claim" && commands.state.uncertain;
  if (targetId === undefined) return null;

  if (preview.status === "error") {
    const failure = failureOf(preview.error);
    if (isHttpFailure(failure, 404, "not_found")) return <Notice tone="warning">选中的版本已经找不到了，请重新选一个。</Notice>;
    if (isHttpFailure(failure, 409, "not_claimable")) return <Notice tone="warning">这个旧版本已经不需要认领了。</Notice>;
    return <FailureNotice error={preview.error} action={<Button onClick={() => void preview.refetch()}>重试</Button>} />;
  }
  if (preview.data === undefined) return <Skeleton label="正在生成认领预览" lines={4} />;

  const data = preview.data;
  const submit = () => {
    if (uncertain) {
      commands.retry();
      return;
    }
    commands.submit({
      kind: "claim",
      release: { id: data.release.id, title: data.release.title },
      revision: data.revision,
      source: { id: data.source.id, title: detail.release.title },
      sourceRevision: data.source_revision,
      preview: data.token,
    });
  };
  return (
    <section className={styles.preview} aria-labelledby="claim-preview-title">
      <h3 id="claim-preview-title" className={styles.title} tabIndex={-1}>
        认领到《{data.release.title}》会怎样
      </h3>
      {data.items.length === 0 ? <p className={styles.empty}>旧版本上没有可以迁移的修改。</p> : <PreviewItems preview={data} detail={detail} />}
      <p className={styles.note}>认领之后可以撤销。</p>
      <div className={styles.submit}>
        <Button variant="primary" busy={pending} onClick={submit}>
          {pending ? "正在认领…" : uncertain ? "重试认领" : `认领到《${data.release.title}》`}
        </Button>
      </div>
    </section>
  );
}
