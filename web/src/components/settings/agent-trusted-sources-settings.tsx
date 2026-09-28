import { useMemo, useState } from "react";
import { toast } from "sonner";
import { AutomationTrustedSourceEditor } from "@/components/automation/automation-trusted-source-editor";
import { updateWorkspacePreferences } from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, WorkspaceSettings } from "@/types/flow";
import "./agent-trusted-sources-settings.css";

type Props = {
  data: BootstrapData;
  settings?: WorkspaceSettings;
  disabled?: boolean;
  onReload?: () => Promise<void>;
};

/** LS-0387 — workspace allowlist for agent/automation trusted sources. */
export function AgentTrustedSourcesSettings({
  data,
  settings,
  disabled,
  onReload,
}: Props) {
  const { t } = useI18n();
  const workspaceSettings = settings ?? data.workspaceSettings;
  const [mode, setMode] = useState(workspaceSettings.trustedSourcesMode ?? "none");
  const [allowlist, setAllowlist] = useState(
    workspaceSettings.trustedSourcesAllowlist ?? [],
  );
  const [busy, setBusy] = useState(false);
  const integrationServices = useMemo(
    () =>
      [
        ...new Set(
          data.integrationConnections
            .filter((item) => item.status === "connected")
            .map((item) => item.provider),
        ),
      ],
    [data.integrationConnections],
  );

  const persist = async (next: {
    trustedSourcesMode: "none" | "allowlist";
    trustedSourcesAllowlist: string[];
  }) => {
    setMode(next.trustedSourcesMode);
    setAllowlist(next.trustedSourcesAllowlist);
    setBusy(true);
    try {
      await updateWorkspacePreferences(
        {
          trustedSourcesMode: next.trustedSourcesMode,
          trustedSourcesAllowlist: next.trustedSourcesAllowlist,
        },
        data.workspace.urlKey,
      );
      await onReload?.();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not update trusted sources"),
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <section
      className="agent-trusted-sources-settings"
      data-testid="agent-trusted-sources-settings"
      aria-labelledby="agent-trusted-sources-title"
    >
      <header>
        <h3 id="agent-trusted-sources-title">{t("Trusted sources")}</h3>
        <p>
          {t(
            "Choose which external sources agents and loops may treat as trusted when processing inbound content.",
          )}
        </p>
      </header>
      <AutomationTrustedSourceEditor
        users={data.users}
        integrationServices={integrationServices}
        trustedSourcesMode={mode}
        trustedSourcesAllowlist={allowlist}
        disabled={disabled || busy}
        onAllowlistChange={(next) => {
          void persist(next);
        }}
      />
    </section>
  );
}
