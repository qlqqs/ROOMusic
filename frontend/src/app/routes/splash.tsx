import { BrandMark } from "../brand/brand-mark";
import { VisuallyHidden } from "../../shared/ui";
import styles from "./splash.module.css";

/** 启动时确认会话期间的占位：不画成空界面，也不闪出登录页。 */
export function Splash() {
  return (
    <div className={styles.splash} role="status">
      <p className={styles.brand}><BrandMark /></p>
      <VisuallyHidden>正在加载</VisuallyHidden>
    </div>
  );
}
