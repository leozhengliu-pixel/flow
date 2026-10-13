import type { WorkspaceSettings } from "@/types/flow";

/**
 * Whether Flow Agent may use the web: the admin's explicit choice when there is one, otherwise on (unless an older
 * client stored the disabled flag). Mirrors WorkspaceSettings.AgentWebAccessEnabled on the server, without HIPAA mode,
 * which the server applies on top.
 */
export function agentWebAccessEnabled(settings: Pick<WorkspaceSettings, "agentWebAccess" | "agentWebSearchDisabled">): boolean {
  return settings.agentWebAccess ?? !settings.agentWebSearchDisabled;
}
