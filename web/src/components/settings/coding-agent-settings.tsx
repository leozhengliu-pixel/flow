import { useMemo, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { updateWorkspacePreferences } from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import type {
  BootstrapData,
  CodingAgentEnvironment,
  CodingAgentSettings,
  WorkspaceSettings,
} from "@/types/flow";
import { SettingsToggle } from "./settings-primitives";
import "./feature-settings.css";
import "./coding-agent-settings.css";

type Props = {
  data: BootstrapData;
  settings?: WorkspaceSettings;
  mode?: "agent" | "environments";
  disabled?: boolean;
  onReload?: () => Promise<void>;
  onOpenEnvironments?: () => void;
  /** Turns the coding-sessions feature flag on or off; the row is hidden without it. */
  onToggleEnabled?: (value: boolean) => void;
};

const HARNESS_OPTIONS = [
  { value: "auto", label: "Auto" },
  { value: "claude", label: "Claude" },
  { value: "codex", label: "Codex" },
] as const;

const MODEL_OPTIONS = [
  { value: "auto", label: "Auto", group: "Default" },
  { value: "claude-sonnet", label: "Claude Sonnet", group: "Frontier" },
  { value: "claude-opus", label: "Claude Opus", group: "Frontier" },
  { value: "gpt-codex", label: "Codex", group: "Other" },
] as const;

function emptyEnvironment(): CodingAgentEnvironment {
  return {
    id: "env_" + crypto.randomUUID().slice(0, 8),
    name: "",
    repository: "",
    setupCommand: "",
    archived: false,
  };
}

/** LS-0116 / LS-0117 — coding agent + environment settings shells (no billing). */
export function CodingAgentSettingsPage({
  data,
  settings,
  mode = "agent",
  disabled,
  onReload,
  onOpenEnvironments,
  onToggleEnabled,
}: Props) {
  const { t } = useI18n();
  const workspaceSettings = settings ?? data.workspaceSettings;
  const codingEnabled = workspaceSettings.featureFlags["coding-sessions"] ?? true;
  const initial = workspaceSettings.codingAgentSettings ?? {};
  const [draft, setDraft] = useState<CodingAgentSettings>({
    commitSigningEnabled: initial.commitSigningEnabled ?? false,
    harness: initial.harness ?? "auto",
    model: initial.model ?? "auto",
    environments: initial.environments ?? [],
  });
  const [busy, setBusy] = useState(false);
  const environments = useMemo(
    () => (draft.environments ?? []).filter((item) => !item.archived),
    [draft.environments],
  );

  const save = async (next: CodingAgentSettings) => {
    setDraft(next);
    setBusy(true);
    try {
      await updateWorkspacePreferences(
        { codingAgentSettings: next },
        data.workspace.urlKey,
      );
      await onReload?.();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : t("Could not update coding agent settings"),
      );
    } finally {
      setBusy(false);
    }
  };

  const controlsDisabled = disabled || busy || !codingEnabled;

  if (mode === "environments") {
    return (
      <div
        className="feature-settings coding-agent-settings"
        data-testid="coding-environment-settings"
      >
        <header className="feature-header coding-agent-settings__header">
          <div>
            <h1>{t("Environments")}</h1>
            <p>{t("Create reusable environments for coding sessions.")}</p>
          </div>
          <button
            type="button"
            className="coding-agent-settings__action"
            disabled={controlsDisabled}
            onClick={() => {
              void save({
                ...draft,
                environments: [...(draft.environments ?? []), emptyEnvironment()],
              });
            }}
          >
            <Plus size={14} />
            {t("New environment")}
          </button>
        </header>
        {!codingEnabled && (
          <p className="coding-agent-settings__hint">
            {t("Enable coding sessions to configure environments.")}
          </p>
        )}
        <ul className="coding-agent-settings__list">
          {environments.map((environment) => (
            <li key={environment.id}>
              <EnvironmentEditor
                environment={environment}
                disabled={controlsDisabled}
                onChange={(nextEnv) => {
                  void save({
                    ...draft,
                    environments: (draft.environments ?? []).map((item) =>
                      item.id === nextEnv.id ? nextEnv : item,
                    ),
                  });
                }}
                onArchive={() => {
                  void save({
                    ...draft,
                    environments: (draft.environments ?? []).map((item) =>
                      item.id === environment.id
                        ? { ...item, archived: true }
                        : item,
                    ),
                  });
                }}
              />
            </li>
          ))}
          {!environments.length && (
            <li className="coding-agent-settings__empty">
              {t("No environments configured")}
            </li>
          )}
        </ul>
      </div>
    );
  }

  return (
    <div
      className="feature-settings coding-agent-settings"
      data-testid="coding-agent-settings"
    >
      <header className="feature-header">
        <h1>{t("Coding sessions")}</h1>
        <p>
          {t(
            "Let Flow Agent write code and open pull requests when assigned or asked to implement an issue.",
          )}
        </p>
      </header>

      <div className="feature-card">
        {onToggleEnabled && (
          <div className="feature-row">
            <div>
              <strong>{t("Enable coding sessions")}</strong>
              <span>{t("Assign or ask Flow to make code changes")}</span>
            </div>
            <aside>
              <SettingsToggle
                checked={codingEnabled}
                disabled={disabled || busy}
                label={t("Enable coding sessions")}
                onChange={onToggleEnabled}
              />
            </aside>
          </div>
        )}
        <label className="feature-row">
          <div>
            <strong>{t("Agent")}</strong>
            <span>{t("Default harness for coding sessions")}</span>
          </div>
          <aside>
            <select
              className="coding-agent-settings__select"
              aria-label={t("Agent")}
              disabled={controlsDisabled}
              value={draft.harness ?? "auto"}
              onChange={(event) =>
                void save({
                  ...draft,
                  harness: event.target.value as CodingAgentSettings["harness"],
                })
              }
            >
              {HARNESS_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {t(option.label)}
                </option>
              ))}
            </select>
          </aside>
        </label>

        <label className="feature-row">
          <div>
            <strong>{t("Model")}</strong>
            <span>{t("Preferred model for coding sessions")}</span>
          </div>
          <aside>
            <select
              className="coding-agent-settings__select"
              aria-label={t("Model")}
              disabled={controlsDisabled}
              value={draft.model ?? "auto"}
              onChange={(event) =>
                void save({ ...draft, model: event.target.value })
              }
            >
              {MODEL_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {t(option.group)}: {t(option.label)}
                </option>
              ))}
            </select>
          </aside>
        </label>

        <div className="feature-row">
          <div>
            <strong>{t("Require signed commits")}</strong>
            <span>
              {t(
                "Users must upload a signing key before starting a coding session",
              )}
            </span>
          </div>
          <aside>
            <SettingsToggle
              checked={Boolean(draft.commitSigningEnabled)}
              disabled={controlsDisabled}
              label={t("Require signed commits")}
              onChange={(value) =>
                void save({ ...draft, commitSigningEnabled: value })
              }
            />
          </aside>
        </div>
      </div>

      <section className="feature-section">
        <header>
          <h2>{t("Environments")}</h2>
          <p>{t("Create reusable environments for coding sessions.")}</p>
        </header>
        <div className="feature-card">
          <div className="feature-row">
            <div>
              <strong>{t("Environments")}</strong>
              <span>
                {environments.length
                  ? t("{count} configured").replace(
                      "{count}",
                      String(environments.length),
                    )
                  : t("No environments configured")}
              </span>
            </div>
            <aside>
              <button
                type="button"
                className="coding-agent-settings__action"
                disabled={disabled || !codingEnabled}
                onClick={() => onOpenEnvironments?.()}
              >
                {t("Manage")}
              </button>
            </aside>
          </div>
        </div>
      </section>
    </div>
  );
}

function EnvironmentEditor({
  environment,
  disabled,
  onChange,
  onArchive,
}: {
  environment: CodingAgentEnvironment;
  disabled?: boolean;
  onChange: (next: CodingAgentEnvironment) => void;
  onArchive: () => void;
}) {
  const { t } = useI18n();
  const [local, setLocal] = useState(environment);
  return (
    <form
      className="coding-agent-environment"
      onSubmit={(event) => {
        event.preventDefault();
        onChange({
          ...local,
          name: local.name.trim(),
          repository: local.repository.trim(),
        });
      }}
    >
      <input
        aria-label={t("Environment name")}
        placeholder={t("Environment name")}
        disabled={disabled}
        value={local.name}
        onChange={(event) => setLocal({ ...local, name: event.target.value })}
      />
      <input
        aria-label={t("Repository")}
        placeholder={t("owner/repo")}
        disabled={disabled}
        value={local.repository}
        onChange={(event) =>
          setLocal({ ...local, repository: event.target.value })
        }
      />
      <input
        aria-label={t("Setup command")}
        placeholder={t("Setup command")}
        disabled={disabled}
        value={local.setupCommand ?? ""}
        onChange={(event) =>
          setLocal({ ...local, setupCommand: event.target.value })
        }
      />
      <footer>
        <button type="submit" disabled={disabled || !local.name.trim()}>
          {t("Save")}
        </button>
        <button
          type="button"
          className="coding-agent-settings__danger"
          disabled={disabled}
          onClick={onArchive}
          aria-label={t("Archive environment")}
        >
          <Trash2 size={14} />
        </button>
      </footer>
    </form>
  );
}
