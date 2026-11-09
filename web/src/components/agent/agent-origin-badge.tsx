import { useI18n } from "@/i18n/i18n";
import type { AgentSession } from "@/types/flow";
import styles from "./agent-badge.module.css";

/**
 * Marks an Agent chat started in the workspace's Lark or Feishu bot: "Lark" / "飞书" for a direct message, with the
 * group's name ("Lark · Release crew") for a mention in a group.
 */
export function AgentOriginBadge({ origin, chat }: { origin?: AgentSession["origin"]; chat?: AgentSession["originChat"] }) {
  const { t } = useI18n();
  if (origin !== "lark" && origin !== "feishu") return null;
  const lark = origin === "lark";
  const platform = lark ? "Lark" : t("Feishu");
  const title = chat
    ? lark ? t("Started in a Lark group") : t("Started in a Feishu group")
    : lark ? t("Started in a Lark direct message") : t("Started in a Feishu direct message");
  return (
    <span className={chat ? `${styles.badge} ${styles.originChat}` : styles.badge} data-agent-origin={origin} data-i18n-ignore title={title}>
      {chat ? `${platform} · ${chat}` : platform}
    </span>
  );
}
