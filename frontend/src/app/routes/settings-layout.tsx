import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { useId } from "react";
import { useViewerScope } from "../../features/auth";
import { groupSettingsSections, settingsSectionAt, visibleSettingsSections } from "./settings-sections";
import styles from "./settings-layout.module.css";

/**
 * 设置页：≥1024px 左侧分栏（栏首“设置”，管理员栏目在“管理”小标题下），更窄时内容上方一行可横向滑动的胶囊标签（不显示小标题）。
 * 分栏是链接加 aria-current，选择写进地址；扫描详情也算在“扫描目录”下。内容区限宽，扫描目录更宽一些。
 * 布局不渲染 h1：每一栏的内容自带 Page，仍是该页唯一的 h1 与文档标题。权限由各栏路由的 AdminGate 把关，这里只决定列出哪些栏。
 */
export function SettingsLayout() {
  const { viewer } = useViewerScope();
  const groupId = useId();
  const wide = useLocation({ select: (location) => settingsSectionAt(location.pathname)?.wide === true });
  return (
    <div className={styles.settings}>
      <nav aria-label="设置" className={styles.nav}>
        <p className={styles.navTitle}>设置</p>
        <div className={styles.tabs}>
          {groupSettingsSections(visibleSettingsSections(viewer.role)).map(({ group, sections }, index) => (
            <div key={group ?? ""} className={styles.section}>
              {group === undefined ? null : (
                <p id={`${groupId}-${index}`} className={styles.groupTitle}>
                  {group}
                </p>
              )}
              <ul className={styles.list} aria-labelledby={group === undefined ? undefined : `${groupId}-${index}`}>
                {sections.map((section) => (
                  <li key={section.to}>
                    <Link to={section.to} search={{}} activeOptions={{ includeSearch: false }} className={styles.link}>
                      <section.icon className={styles.icon} size={18} aria-hidden="true" />
                      <span>{section.label}</span>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </nav>
      <div className={styles.content} data-wide={wide ? "" : undefined}>
        <Outlet />
      </div>
    </div>
  );
}
