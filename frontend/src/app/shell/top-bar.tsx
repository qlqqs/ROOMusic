import { Link, useRouterState } from "@tanstack/react-router";
import { useId } from "react";
import { GlobalSearchBox } from "../../features/search";
import { toggleDock, useDockState, useStewardEntry } from "../../features/steward";
import { useTooltip } from "../../shared/ui";
import { StewardIcon } from "./nav-items";
import { stewardUnreadText } from "./primary-nav";
import { UserMenu } from "./user-menu";
import styles from "./top-bar.module.css";

/** 管家卡片的元素 ID：顶栏管家按钮的 aria-controls 指向它。 */
export const STEWARD_DOCK_ID = "steward-dock";
/** 顶栏的管家按钮：卡片收起后焦点回到这里。 */
export const STEWARD_ENTRY_ID = "steward-entry";

/**
 * 宽屏（≥768px）主内容上方的顶栏：左边全局搜索，右边管家按钮与当前用户的头像菜单。窄屏不渲染（外壳另有顶栏与标签栏）。
 */
export function TopBar({ searchQuery }: { searchQuery: string | undefined }) {
  const steward = useStewardEntry();
  return (
    <div className={styles.bar}>
      <GlobalSearchBox currentQuery={searchQuery} className={styles.search} />
      <div className={styles.actions}>
        {steward.available ? <StewardButton name={steward.name} unread={steward.unread} /> : null}
        <UserMenu />
      </div>
    </div>
  );
}

/**
 * 管家按钮：名字取自设置，在提示与读屏名里。在别的页面上点它开合浮起的管家卡片（aria-expanded 表达开合）；
 * 在整页对话里它是当前项（链接）。有运行结束后还没打开看过的会话时右上角有提示点，数量经 aria-describedby 说明。
 */
function StewardButton({ name, unread }: { name: string; unread: number }) {
  const onPage = useRouterState({ select: (state) => state.location.pathname === "/steward" || state.location.pathname.startsWith("/steward/") });
  const dock = useDockState();
  const descriptionId = useId();
  const tip = useTooltip(name);
  const described = unread > 0 ? descriptionId : undefined;
  const content = (
    <>
      <StewardIcon size={18} aria-hidden="true" />
      {unread > 0 ? <span className={styles.dot} aria-hidden="true" /> : null}
    </>
  );
  return (
    <>
      {onPage ? (
        <Link to="/steward" className={styles.iconButton} aria-label={name} aria-current="page" aria-describedby={described} {...tip.triggerProps}>
          {content}
        </Link>
      ) : (
        <button
          type="button"
          id={STEWARD_ENTRY_ID}
          className={styles.iconButton}
          aria-label={name}
          aria-expanded={dock.open}
          aria-controls={dock.open ? STEWARD_DOCK_ID : undefined}
          aria-describedby={described}
          onClick={toggleDock}
          {...tip.triggerProps}
        >
          {content}
        </button>
      )}
      {tip.tooltip}
      {unread > 0 ? (
        <span id={descriptionId} hidden>
          {stewardUnreadText(unread)}
        </span>
      ) : null}
    </>
  );
}
