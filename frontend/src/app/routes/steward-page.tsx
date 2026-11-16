import { StewardPage } from "../../features/steward/chat";

/** 管家的整页对话（按需加载的代码块入口）。 */
export function StewardRoutePage({ conversationId }: { conversationId: string | null }) {
  return <StewardPage conversationId={conversationId} />;
}
