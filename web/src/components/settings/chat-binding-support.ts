import { useCallback, useEffect, useRef, useState } from "react";

import { confirmAction } from "@/components/ui/action-dialog-service";
import { useI18n } from "@/i18n/i18n";
import { chatErrorMessage, fetchChatIntegration } from "@/lib/chat-integrations-api";
import type { ChatBinding, ChatIntegrationEvent, ChatIntegrationProvider, ChatIntegrationSettings } from "@/types/chat-integrations";

export const CHAT_BINDING_EVENTS: { value: ChatIntegrationEvent; label: string }[] = [
  { value: "issue.created", label: "Issue created" },
  { value: "issue.status_changed", label: "Status changed" },
  { value: "issue.assignee_changed", label: "Assignee changed" },
  { value: "comment.created", label: "Comment added" },
  { value: "project.update_created", label: "Project update published" },
  { value: "initiative.update_created", label: "Initiative update published" },
];

/** What a team's group shares by default when connected from the team's notifications page. */
export const TEAM_CHAT_DEFAULT_EVENTS: ChatIntegrationEvent[] = ["issue.created", "issue.status_changed", "comment.created", "project.update_created"];

/** Asks before a binding is revoked; shared by the integration, team and project pages. */
export async function confirmRevokeBinding(binding: ChatBinding, t: (source: string) => string) {
  return confirmAction(t("Revoke group binding?"), {
    description: t("{group} stops receiving updates and its group actions stop working. The binding stays listed as revoked.").replace("{group}", binding.chatName),
    confirmLabel: t("Revoke"), danger: true,
  });
}

export async function confirmRemoveBinding(binding: ChatBinding, t: (source: string) => string) {
  return confirmAction(t("Remove group binding?"), { description: binding.chatName, confirmLabel: t("Remove"), danger: true });
}

/** Loads a chat integration's settings for a page outside the integration settings. */
export function useChatIntegrationSettings(provider: ChatIntegrationProvider | undefined) {
  const { t } = useI18n();
  const [settings, setSettings] = useState<ChatIntegrationSettings | null>(null);
  const [loading, setLoading] = useState(Boolean(provider));
  const [error, setError] = useState("");
  const mounted = useRef(true);
  const load = useCallback(async () => {
    if (!provider) return;
    setLoading(true);
    setError("");
    try {
      const next = await fetchChatIntegration(provider);
      if (mounted.current) setSettings(next);
    } catch (cause) {
      if (mounted.current) setError(chatErrorMessage(cause, t, "Could not load integration"));
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, [provider, t]);
  useEffect(() => {
    mounted.current = true;
    queueMicrotask(() => { if (mounted.current) void load(); });
    return () => { mounted.current = false; };
  }, [load]);
  return { settings, loading, error, setError, reload: load };
}
