import { Link } from "@tanstack/react-router";
import { FolderPlus, Library } from "lucide-react";
import { EmptyState } from "../../../shared/ui";
import { useCatalogStats } from "../hooks/use-catalog-stats";
import styles from "./library-empty-state.module.css";

/**
 * 概览主栏的空曲库说明：资料库里还没有专辑时出现，其余时候不渲染（统计还没读到时也不渲染，不闪一下）。
 * 管理员直接看到“添加音乐目录”（第一步就在眼前，不用到右栏的小字里找）；成员说明要等管理员添加。
 */
export function LibraryEmptyState({ isAdmin }: { isAdmin: boolean }) {
  const stats = useCatalogStats();
  if (stats.data === undefined || stats.data.albums > 0) return null;
  return (
    <EmptyState
      icon={<Library size={22} />}
      title="资料库还是空的"
      action={
        isAdmin ? (
          <Link to="/settings/library" className={styles.action}>
            <FolderPlus size={16} aria-hidden="true" />
            添加音乐目录
          </Link>
        ) : undefined
      }
    >
      {isAdmin ? "添加放音乐的目录，扫描完成后专辑就会出现在这里。" : "管理员添加音乐并扫描之后，就能在这里浏览和播放。"}
    </EmptyState>
  );
}
