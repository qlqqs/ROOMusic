import { Link, Outlet } from "@tanstack/react-router";
import { useViewerScope } from "../../features/auth";
import { visibleSettingsSections } from "./settings-sections";
import styles from "./settings-layout.module.css";

/**
 * 设置页：≥1024px 左侧分栏，更窄时内容上方一行横向链接。分栏是链接加 aria-current，选择写进地址；扫描详情也算在“扫描目录”下。
 * 布局不渲染 h1：每一栏的内容自带 Page，仍是该页唯一的 h1 与文档标题。权限由各栏路由的 AdminGate 把关，这里只决定列出哪些栏。
 */
export function SettingsLayout() {
  const { viewer } = useViewerScope();
  return (
    <div className={styles.settings}>
      <nav aria-label="设置" className={styles.nav}>
        <p className={styles.navTitle}>设置</p>
        <ul className={styles.list}>
          {visibleSettingsSections(viewer.role).map((section) => (
            <li key={section.to}>
              <Link to={section.to} search={{}} activeOptions={{ includeSearch: false }} className={styles.link}>
                <section.icon className={styles.icon} size={18} aria-hidden="true" />
                <span>{section.label}</span>
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      <div className={styles.content}>
        <Outlet />
      </div>
    </div>
  );
}
