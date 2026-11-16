import type { StewardReadiness } from "../hooks/use-steward-settings";
import { AgentSettingsLink } from "./settings-link";
import styles from "./thread.module.css";

const texts: Record<Exclude<StewardReadiness, "ready">, { title: string; body: string }> = {
  not_configured: { title: "先接上一个模型", body: "用你自己的模型接口回答问题、查曲库。地址和密钥只给你一个人用。" },
  needs_key: { title: "密钥需要重新填写", body: "已保存的密钥在这台服务器上解不开了（服务器的密钥文件换过）。重新填写后就能继续用，其余设置都还在。" },
  no_tool_support: { title: "这个模型不能调用工具", body: "要靠调用工具才能查曲库。换一个支持工具调用的模型，或者在确定它支持时到设置里修改检测结果。" },
};

/** 未配置模型时代替输入框的引导：说明、两步做法与“去设置”。compact 用在已有对话的下方。 */
export function SetupGuide({ readiness, name, compact = false }: { readiness: Exclude<StewardReadiness, "ready">; name: string; compact?: boolean }) {
  const text = texts[readiness];
  return (
    <div className={styles.guide} data-compact={compact ? "" : undefined}>
      <h3 className={styles.guideTitle}>{text.title}</h3>
      <p className={styles.guideText}>{readiness === "not_configured" ? `${name}${text.body}` : text.body}</p>
      {readiness === "not_configured" ? (
        <ol className={styles.guideSteps}>
          <li>填接口地址和密钥</li>
          <li>检测通过后选一个对话模型</li>
        </ol>
      ) : null}
      <AgentSettingsLink className={styles.primaryLink}>去设置</AgentSettingsLink>
    </div>
  );
}
