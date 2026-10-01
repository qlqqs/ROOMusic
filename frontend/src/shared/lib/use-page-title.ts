import { useEffect } from "react";

const SITE_NAME = "ROOMusic";

/** 页面标题统一为“页面名 · ROOMusic”。 */
export function usePageTitle(title: string): void {
  useEffect(() => {
    document.title = `${title} · ${SITE_NAME}`;
  }, [title]);
}
