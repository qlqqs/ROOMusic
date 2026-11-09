import { ChevronLeft, X } from "lucide-react";
import { useId, useState } from "react";
import { Button, FailureNotice, ModalDialog, TextField } from "../../../shared/ui";
import opraLogo from "../../../assets/opra/opra-logo.png";
import type { PeqDocument } from "./document";
import { useOpraProduct, useOpraSearch } from "./hooks";
import styles from "./peq.module.css";

const OPRA_REPOSITORY = "https://github.com/opra-project/OPRA";

/** OPRA 的署名：标识、一句简介与仓库链接（OPRA 的使用要求）。 */
function OpraAttribution() {
  return (
    <footer className={styles.attribution}>
      <img src={opraLogo} alt="OPRA" width={60} height={24} />
      <p>
        OPRA 是社区维护的耳机校正曲线目录，数据按 CC BY-SA 4.0 使用。<a href={OPRA_REPOSITORY} target="_blank" rel="noreferrer">GitHub</a>
      </p>
    </footer>
  );
}

function ProductEqs({ productId, onBack, onApply }: { productId: string; onBack: () => void; onApply: (peq: PeqDocument) => void }) {
  const product = useOpraProduct(productId);
  return (
    <div className={styles.panelBody}>
      <div className={styles.productHeader}>
        <Button variant="icon" icon={<ChevronLeft size={18} />} label="返回" onClick={onBack} />
        {product.data === undefined ? null : <span className={styles.productName}>{product.data.product.vendor} {product.data.product.name}</span>}
      </div>
      {product.isError ? <FailureNotice error={product.error} /> : null}
      <ul className={styles.list}>
        {product.data?.eqs.map((eq) => (
          <li key={eq.eqId} className={styles.eq}>
            <div className={styles.eqText}>
              <strong className={styles.author}>{eq.author}</strong>
              {eq.details === "" ? null : <span className={styles.meta}>{eq.details}</span>}
              {eq.link === "" ? null : <a className={styles.meta} href={eq.link} target="_blank" rel="noreferrer">来源</a>}
            </div>
            <Button variant="primary" onClick={() => onApply(eq.peq)}>套用</Button>
          </li>
        ))}
      </ul>
    </div>
  );
}

function OpraBrowser({ onApply }: { onApply: (peq: PeqDocument) => void }) {
  const [query, setQuery] = useState("");
  const [productId, setProductId] = useState<string | null>(null);
  const search = useOpraSearch(query);
  if (productId !== null) return <ProductEqs productId={productId} onBack={() => setProductId(null)} onApply={onApply} />;
  return (
    <div className={styles.panelBody}>
      <TextField label="耳机型号" type="search" value={query} autoComplete="off" onChange={(event) => setQuery(event.target.value)} />
      {search.isError ? <FailureNotice error={search.error} /> : null}
      {query.trim() !== "" && search.data?.length === 0 ? <p className={styles.meta}>没有找到</p> : null}
      <ul className={styles.list}>
        {query.trim() === "" ? null : search.data?.map((product) => (
          <li key={product.productId}>
            <button type="button" className={styles.option} onClick={() => setProductId(product.productId)}>
              <span>{product.vendor} {product.name}</span>
              <span className={styles.meta}>{product.eqCount} 个配置</span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** 耳机校正：搜索型号，选一条 OPRA 配置套用。 */
export function OpraDialog({ open, onApply, onClose }: { open: boolean; onApply: (peq: PeqDocument) => void; onClose: () => void }) {
  const titleId = useId();
  return (
    <ModalDialog open={open} titleId={titleId} className={styles.subDialog} onRequestClose={onClose} closeOnBackdrop>
      <div className={styles.panel}>
        <div className={styles.header}>
          <h2 id={titleId} className={styles.title}>耳机校正</h2>
          <div className={styles.toolbar}><Button variant="icon" icon={<X size={18} />} label="关闭" onClick={onClose} /></div>
        </div>
        <OpraBrowser onApply={onApply} />
        <OpraAttribution />
      </div>
    </ModalDialog>
  );
}
