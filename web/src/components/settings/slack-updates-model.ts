import type { BootstrapData, IntegrationConnection } from "@/types/flow";

export type SlackUpdatesKind = "project" | "initiative";

export const SCOPE_BY_KIND: Record<SlackUpdatesKind, string> = {
  project: "orgProjectUpdates",
  initiative: "initiative-updates",
};

export function findSlackUpdatesConnection(
  data: BootstrapData,
  kind: SlackUpdatesKind,
): IntegrationConnection | undefined {
  const scope = SCOPE_BY_KIND[kind];
  return (data.integrationConnections ?? []).find((item) => {
    if (item.provider !== "slack") return false;
    const config = item.config ?? {};
    if (kind === "project") {
      return (
        config.scope === scope ||
        config.source === "project-updates" ||
        config.scope === "project-updates"
      );
    }
    return config.scope === scope;
  });
}
