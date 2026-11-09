import { X } from "lucide-react";
import { useId, useState } from "react";
import { failureOf } from "../../../shared/api";
import { Button, FailureNotice, ModalDialog } from "../../../shared/ui";
import type { PeqDocument } from "./document";
import { usePeqImport, usePeqResponse } from "./hooks";
import { PeqCurve } from "./peq-curve";
import styles from "./peq.module.css";

function Preview({ peq }: { peq: PeqDocument }) {
  const response = usePeqResponse(peq);
  return <PeqCurve curves={response.data ?? []} />;
}

/** 粘贴导入：AutoEq / Equalizer APO 的文本，预览曲线后应用。 */
export function PeqImportDialog({ open, onApply, onClose }: { open: boolean; onApply: (peq: PeqDocument) => void; onClose: () => void }) {
  const titleId = useId();
  const textId = useId();
  const [text, setText] = useState("");
  const parsed = usePeqImport(text);
  const failure = parsed.isError ? failureOf(parsed.error) : null;
  const empty = failure?.kind === "http" && failure.code === "peq_import_empty";
  const result = text.trim() === "" ? undefined : parsed.data;
  return (
    <ModalDialog open={open} titleId={titleId} className={styles.subDialog} onRequestClose={onClose}>
      <div className={styles.panel}>
        <div className={styles.header}>
          <h2 id={titleId} className={styles.title}>粘贴导入</h2>
          <div className={styles.toolbar}><Button variant="icon" icon={<X size={18} />} label="关闭" onClick={onClose} /></div>
        </div>
        <div className={styles.panelBody}>
          <label htmlFor={textId} className={styles.label}>参数</label>
          <textarea id={textId} className={styles.textarea} rows={8} spellCheck={false} placeholder={"Preamp: -6.2 dB\nFilter 1: ON PK Fc 105 Hz Gain 6.5 dB Q 0.70"} value={text} onChange={(event) => setText(event.target.value)} />
          {text.trim() !== "" && empty ? <p className={styles.meta}>没有识别出滤波器</p> : null}
          {parsed.isError && !empty ? <FailureNotice error={parsed.error} /> : null}
          {result === undefined ? null : (
            <>
              <Preview peq={result.peq} />
              <p className={styles.meta}>
                {result.peq.bands.length} 段{result.unrecognizedLines.length === 0 ? "" : `，有 ${result.unrecognizedLines.length} 行没有识别`}
              </p>
            </>
          )}
          <div className={styles.actions}>
            <Button variant="primary" disabled={result === undefined} onClick={() => result !== undefined && onApply(result.peq)}>应用</Button>
          </div>
        </div>
      </div>
    </ModalDialog>
  );
}
