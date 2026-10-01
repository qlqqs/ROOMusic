import { Link } from "@tanstack/react-router";
import type { UserRole } from "../../features/auth";
import { visibleNavGroups } from "./nav-items";
import styles from "./primary-nav.module.css";

/** 主导航：导航用链接，当前页由 aria-current 表达。 */
export function PrimaryNav({ role, onNavigate }: { role: UserRole; onNavigate?: () => void }) {
  return (
    <nav className={styles.nav} aria-label="主导航">
      {visibleNavGroups(role).map((group) => (
        <div key={group.label ?? "default"} className={styles.group}>
          {group.label === null ? null : <p className={styles.groupLabel}>{group.label}</p>}
          <ul className={styles.list}>
            {group.items.map((item) => (
              <li key={item.to}>
                <Link to={item.to} className={styles.link} activeOptions={{ exact: item.to === "/" }} onClick={onNavigate}>
                  <item.icon size={18} aria-hidden="true" />
                  <span>{item.label}</span>
                </Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </nav>
  );
}
