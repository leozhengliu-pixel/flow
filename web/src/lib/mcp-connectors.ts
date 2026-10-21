import type { WorkspaceSettings } from "@/types/flow";

/**
 * Whether Flow Agent may use MCP connectors: the admin's explicit choice when there is one, otherwise on. HIPAA mode
 * always turns them off. Mirrors WorkspaceSettings.MCPConnectorsActive on the server.
 */
export function mcpConnectorsEnabled(settings?: Pick<WorkspaceSettings, "mcpConnectors" | "hipaaCompliance"> | null): boolean {
  if (!settings || settings.hipaaCompliance) return false;
  return settings.mcpConnectors ?? true;
}
