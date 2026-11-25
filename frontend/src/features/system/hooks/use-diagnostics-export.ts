import { useMutation } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { fetchDiagnostics } from "../api/endpoints";
import type { DiagnosticsReport } from "../api/decoders";

/** 能写剪贴板时复制（粘进 issue 的代码块）；经 http 局域网地址打开时浏览器不给剪贴板，改为下载文件。 */
export type DiagnosticsDelivery = "copy" | "download";

export type DiagnosticsExport = {
  delivery: DiagnosticsDelivery;
  /** 刚复制或下载完成时是实际的方式（图标换成对勾），两秒后复原为 null。 */
  done: DiagnosticsDelivery | null;
  pending: boolean;
  error: unknown;
  run: () => void;
};

const DONE_MS = 2000;

function canCopy(): boolean {
  return typeof navigator !== "undefined" && navigator.clipboard !== undefined && window.isSecureContext;
}

const fenced = (report: DiagnosticsReport) => `\`\`\`json\n${report.text}\n\`\`\`\n`;

function download(report: DiagnosticsReport) {
  const url = URL.createObjectURL(new Blob([report.text], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = `roomusic-diagnostics-${report.generatedAt.replaceAll(":", "-")}.json`;
  link.click();
  URL.revokeObjectURL(url);
}

/**
 * 复制诊断信息时要在点击的同一刻调用剪贴板：Safari 在 await 之后不再允许写入，所以把“读取报告”作为 ClipboardItem 的 Promise 交出去。
 * 浏览器没有 ClipboardItem 时先读再写。写入被拒（权限、焦点离开页面）时退回下载，不让这一步白做。
 */
async function copy(): Promise<DiagnosticsDelivery> {
  const report = fetchDiagnostics().then((response) => response.data);
  try {
    if (typeof ClipboardItem === "function") {
      const blob = report.then((value) => new Blob([fenced(value)], { type: "text/plain" }));
      // 剪贴板可能先拒绝、不再等 blob；读取失败由下面 await report 报告，这里只标记已处理。
      blob.catch(() => undefined);
      await navigator.clipboard.write([new ClipboardItem({ "text/plain": blob })]);
    } else {
      await navigator.clipboard.writeText(fenced(await report));
    }
    return "copy";
  } catch (error) {
    // 读取失败照常报错；只有剪贴板本身拒绝时才改为下载。
    const value = await report;
    if (!(error instanceof DOMException)) throw error;
    download(value);
    return "download";
  }
}

export function useDiagnosticsExport(): DiagnosticsExport {
  const delivery: DiagnosticsDelivery = canCopy() ? "copy" : "download";
  // 实际完成的方式：剪贴板被拒时虽然准备复制，结果是下载，提示要照实说。
  const [done, setDone] = useState<DiagnosticsDelivery | null>(null);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const mutation = useMutation({
    mutationFn: async (): Promise<DiagnosticsDelivery> => {
      if (delivery === "copy") return copy();
      download((await fetchDiagnostics()).data);
      return "download";
    },
    onSuccess: (result) => {
      setDone(result);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setDone(null), DONE_MS);
    },
  });
  return {
    delivery,
    done,
    pending: mutation.isPending,
    error: mutation.error,
    run: () => {
      if (!mutation.isPending) mutation.mutate();
    },
  };
}
