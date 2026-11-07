import { ArrowUp, Folder } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button, Skeleton, StatusTag } from "../../../shared/ui";
import { useFolderListing } from "../hooks/use-folder-listing";
import styles from "./folder-browser.module.css";

type FolderBrowserProps = {
  id: string;
  /** 选中一个文件夹：把它的路径填进路径框。 */
  onSelect: (path: string) => void;
};

/**
 * 添加目录时的文件夹浏览器：从允许的音乐目录开始，点文件夹进入下一级，点“选择”选中。
 * 行内都是普通按钮（不是 listbox），Tab 顺序自然；进入新文件夹后焦点移到顶部的当前路径，读屏读出新位置。
 */
export function FolderBrowser({ id, onSelect }: FolderBrowserProps) {
  const [current, setCurrent] = useState<string | null>(null);
  const listing = useFolderListing(current);
  const data = listing.data;
  const locationRef = useRef<HTMLParagraphElement>(null);
  // 只在用户换了文件夹之后移动焦点；刚打开时焦点留在路径框里。
  const navigated = useRef(false);
  useEffect(() => {
    if (navigated.current && data !== undefined) locationRef.current?.focus();
  }, [data]);
  const go = (path: string | null) => {
    navigated.current = true;
    setCurrent(path);
  };

  return (
    <div id={id} role="dialog" aria-label="选择文件夹" className={styles.browser}>
      <div className={styles.header}>
        {data?.can_go_up === true ? <Button variant="icon" icon={<ArrowUp size={16} />} label="上一级" onClick={() => go(data.parent)} /> : null}
        <p ref={locationRef} tabIndex={-1} className={styles.location}>
          {data === undefined ? (current ?? "") : (data.path ?? "音乐目录")}
        </p>
        {data?.path != null && data.registered ? <StatusTag>已添加</StatusTag> : null}
        {data?.path != null ? (
          <Button aria-label={`选择 ${data.path}`} onClick={() => onSelect(data.path ?? "")}>
            选择
          </Button>
        ) : null}
      </div>
      <div className={styles.body}>
        {listing.isError ? (
          <div className={styles.message}>
            <p>打不开这个文件夹。</p>
            {current === null ? null : <Button onClick={() => go(null)}>回到开头</Button>}
          </div>
        ) : data === undefined ? (
          <Skeleton label="正在读取文件夹" lines={3} />
        ) : data.items.length === 0 ? (
          <p className={styles.message}>这里没有子文件夹。</p>
        ) : (
          <>
            <ul className={styles.list}>
              {data.items.map((item) => (
                <li key={item.path} className={styles.row}>
                  <button type="button" className={styles.folder} onClick={() => go(item.path)}>
                    <Folder size={16} aria-hidden="true" />
                    <span className={styles.name}>{item.name}</span>
                  </button>
                  {item.registered ? <StatusTag>已添加</StatusTag> : null}
                  <Button aria-label={`选择 ${item.name}`} onClick={() => onSelect(item.path)}>
                    选择
                  </Button>
                </li>
              ))}
            </ul>
            {data.truncated ? <p className={styles.message}>文件夹太多，没有全部列出，可以直接输入路径。</p> : null}
          </>
        )}
      </div>
    </div>
  );
}
