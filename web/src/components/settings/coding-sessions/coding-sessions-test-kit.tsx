import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { vi } from "vitest";
import { ActionDialogHost } from "@/components/ui/action-dialogs";
import { I18nProvider } from "@/i18n/i18n";
import { makeBootstrap } from "@/test/fixtures";
import type { BootstrapData, CodingEnvironment, CodingSettings, CodingStatus, WorkspaceSettings } from "@/types/flow";
import type { SettingsPageId } from "@/lib/app-routes";
import { CodingSessionsSettings } from "./coding-sessions-settings";
import { LocationProbe } from "./location-probe";

export const ROOT = "/workspace/settings/ai/coding-sessions";

export function makeStatus(overrides: Partial<CodingStatus> = {}): CodingStatus {
  return {
    enabled: true,
    provider: "fake",
    snapshotStrategy: "archive",
    regions: [],
    sizes: { small: { cpu: "1", memory: "8Gi" }, medium: { cpu: "2", memory: "16Gi" }, large: { cpu: "4", memory: "32Gi" } },
    codeHosts: [{ codeHost: "github", hostName: "github.com", connected: true }],
    debugTerminal: true,
    browserTesting: true,
    preflight: [],
    ...overrides,
  };
}

export function makeSettings(overrides: Partial<CodingSettings> = {}): CodingSettings {
  return { enabled: true, harness: "codex", model: "auto", requireSignedCommits: false, trustedExternalSources: [], loopRepositoryAccess: "none", loopRepositories: [], ...overrides };
}

export function makeEnvironment(overrides: Partial<CodingEnvironment> = {}): CodingEnvironment {
  return {
    id: "env1",
    name: "Web",
    codeHost: "github",
    hostName: "github.com",
    repository: "acme/web",
    mise: { tools: { node: "22" }, files: [], environmentVariables: [{ name: "NODE_ENV", value: "test" }] },
    prepareScript: "npm ci",
    dependencyManagement: "custom-and-generated",
    sandboxSize: "medium",
    network: "default",
    secrets: [{ name: "NPM_TOKEN", createdAt: "2026-10-01T00:00:00Z" }],
    version: 3,
    createdAt: "2026-10-01T00:00:00Z",
    updatedAt: "2026-10-01T00:00:00Z",
    ...overrides,
  };
}

export function renderCodingSettings({ path = ROOT, page, viewerRole = "admin", flags = { "coding-sessions": true }, onToggleEnabled = vi.fn(), onNavigateSettings = vi.fn(), onOpenIntegration = vi.fn(), data: dataOverrides = {} }: { path?: string; page?: "coding-sessions" | "coding-environments"; viewerRole?: string; flags?: Record<string, boolean>; onToggleEnabled?: (value: boolean) => void; onNavigateSettings?: (page: SettingsPageId) => void; onOpenIntegration?: (provider: string) => void; data?: Partial<BootstrapData> } = {}) {
  const settings = { featureFlags: flags, featureSettings: {}, codingAgentSettings: { harness: "codex" } } as unknown as WorkspaceSettings;
  const data = makeBootstrap({ viewerRole: viewerRole as never, workspaceSettings: settings, ...dataOverrides });
  const resolvedPage = page ?? (path.includes("/environments") ? "coding-environments" : "coding-sessions");
  const view = render(
    <I18nProvider>
      <MemoryRouter initialEntries={[path]}>
        <CodingSessionsSettings page={resolvedPage} data={data} settings={settings} disabled={!["admin", "owner"].includes(viewerRole)} onToggleEnabled={onToggleEnabled} onNavigateSettings={onNavigateSettings} onOpenIntegration={onOpenIntegration} />
        <LocationProbe />
      </MemoryRouter>
      <ActionDialogHost />
    </I18nProvider>,
  );
  return { ...view, onToggleEnabled, onNavigateSettings, onOpenIntegration };
}
