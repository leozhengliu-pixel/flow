import { useI18n } from "@/i18n/i18n";
import type { AgentSession } from "@/types/flow";
import styles from "./agent-badge.module.css";

/** Marks an Agent chat started in a direct message with the workspace's Lark or Feishu bot ("Lark" / "飞书"). */
export function AgentOriginBadge({ origin }: { origin?: AgentSession["origin"] }) {
  const { t } = useI18n();
  if (origin !== "lark" && origin !== "feishu") return null;
  const lark = origin === "lark";
  return (
    <span
      className={styles.badge}
      data-agent-origin={origin}
      data-i18n-ignore
      title={lark ? t("Started in a Lark direct message") : t("Started in a Feishu direct message")}
    >
      {lark ? "Lark" : t("Feishu")}
    </span>
  );
}
