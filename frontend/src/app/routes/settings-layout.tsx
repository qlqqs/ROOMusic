import { Link, Outlet, useLocation } from "@tanstack/react-router";
import { useEffect, useId, useRef } from "react";
import { useViewerScope } from "../../features/auth";
import { groupSettingsSections, settingsSectionAt, visibleSettingsSections } from "./settings-sections";
import styles from "./settings-layout.module.css";

/**
 * 设置页：≥1024px 左侧分栏（栏首“设置”，管理员栏目在“管理”小标题下），更窄时内容上方一行可横向滑动的胶囊标签（不显示小标题），
 * 两端还有被遮住的标签时那一端渐隐，当前标签滚进视野。
 * 分栏是链接加 aria-current，选择写进地址；扫描详情也算在“扫描目录”下。内容区限宽，扫描目录更宽一些。
 * 布局不渲染 h1：每一栏的内容自带 Page，仍是该页唯一的 h1 与文档标题。权限由各栏路由的 AdminGate 把关，这里只决定列出哪些栏。
 */
export function SettingsLayout() {
  const { viewer } = useViewerScope();
  const groupId = useId();
  const wide = useLocation({ select: (location) => settingsSectionAt(location.pathname)?.wide === true });
  const pathname = useLocation({ select: (location) => location.pathname });
  const tabs = useScrollEdges(pathname);
  return (
    <div className={styles.settings}>
      <nav aria-label="设置" className={styles.nav}>
        <p className={styles.navTitle}>设置</p>
        <div ref={tabs} className={styles.tabs}>
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

/**
 * 胶囊标签行的滚动提示：左边还能往回滑时标上 data-fade-start，右边还有标签时标上 data-fade-end，样式据此在那一端渐隐；
 * 换栏目（pathname 变化）时把当前标签滚进视野，排在末尾的“备份”不会只露出一半。宽屏竖排时不溢出，两个标记都不出现。
 */
function useScrollEdges(pathname: string) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = ref.current;
    if (element === null) return;
    const update = () => {
      const max = element.scrollWidth - element.clientWidth;
      element.toggleAttribute("data-fade-start", element.scrollLeft > 1);
      element.toggleAttribute("data-fade-end", element.scrollLeft < max - 1);
    };
    update();
    element.addEventListener("scroll", update, { passive: true });
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(update);
    observer?.observe(element);
    return () => {
      element.removeEventListener("scroll", update);
      observer?.disconnect();
    };
  }, []);
  useEffect(() => {
    const element = ref.current;
    const current = element?.querySelector<HTMLElement>('[aria-current="page"]');
    if (element === null || current === null || current === undefined || element.scrollWidth <= element.clientWidth) return;
    // 只在这一行里横向滚动，不带动整页。
    const left = current.getBoundingClientRect().left - element.getBoundingClientRect().left + element.scrollLeft;
    const target = Math.min(Math.max(left - (element.clientWidth - current.offsetWidth) / 2, 0), element.scrollWidth - element.clientWidth);
    element.scrollTo({ left: target });
  }, [pathname]);
  return ref;
}
