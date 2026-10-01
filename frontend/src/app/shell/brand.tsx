import { Link } from "@tanstack/react-router";
import styles from "./brand.module.css";

export function Brand({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <Link to="/" className={styles.brand} onClick={onNavigate} aria-label="ROOMusic 概览">
      ROOMusic
    </Link>
  );
}
