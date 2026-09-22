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
import "./coding-agent-settings.css";

type Props = {
  data: BootstrapData;
  settings?: WorkspaceSettings;
  mode?: "agent" | "environments";
  disabled?: boolean;
  onReload?: () => Promise<void>;
  onOpenEnvironments?: () => void;
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

  if (mode === "environments") {
    return (
      <section
        className="coding-agent-settings"
        data-testid="coding-environment-settings"
      >
        <header className="coding-agent-settings__header">
          <div>
            <h2>{t("Environments")}</h2>
            <p>{t("Create reusable environments for coding sessions.")}</p>
          </div>
          <button
            type="button"
            className="coding-agent-settings__action"
            disabled={disabled || busy || !codingEnabled}
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
                disabled={disabled || busy || !codingEnabled}
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
      </section>
    );
  }

  return (
    <section
      className="coding-agent-settings"
      data-testid="coding-agent-settings"
    >
      <header className="coding-agent-settings__header">
        <div>
          <h2>{t("Coding sessions")}</h2>
          <p>
            {t(
              "Let Flow Agent write code and open pull requests when assigned or asked to implement an issue.",
            )}
          </p>
        </div>
      </header>

      <div className="coding-agent-settings__card">
        <label className="coding-agent-settings__row">
          <span>
            <strong>{t("Agent")}</strong>
            <small>{t("Default harness for coding sessions")}</small>
          </span>
          <select
            aria-label={t("Agent")}
            disabled={disabled || busy || !codingEnabled}
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
                {option.label}
              </option>
            ))}
          </select>
        </label>

        <label className="coding-agent-settings__row">
          <span>
            <strong>{t("Model")}</strong>
            <small>{t("Preferred model for coding sessions")}</small>
          </span>
          <select
            aria-label={t("Model")}
            disabled={disabled || busy || !codingEnabled}
            value={draft.model ?? "auto"}
            onChange={(event) =>
              void save({ ...draft, model: event.target.value })
            }
          >
            {MODEL_OPTIONS.map((option) => (
              <option key={option.value} value={option.value}>
                {option.group}: {option.label}
              </option>
            ))}
          </select>
        </label>

        <label className="coding-agent-settings__row">
          <span>
            <strong>{t("Require signed commits")}</strong>
            <small>
              {t(
                "Users must upload a signing key before starting a coding session",
              )}
            </small>
          </span>
          <input
            type="checkbox"
            aria-label={t("Require signed commits")}
            checked={Boolean(draft.commitSigningEnabled)}
            disabled={disabled || busy || !codingEnabled}
            onChange={(event) =>
              void save({
                ...draft,
                commitSigningEnabled: event.target.checked,
              })
            }
          />
        </label>
      </div>

      <div className="coding-agent-settings__card">
        <div className="coding-agent-settings__row">
          <span>
            <strong>{t("Environments")}</strong>
            <small>
              {environments.length
                ? environments.length + " configured"
                : t("No environments configured")}
            </small>
          </span>
          <button
            type="button"
            className="coding-agent-settings__action"
            disabled={disabled || !codingEnabled}
            onClick={() => onOpenEnvironments?.()}
          >
            {t("Manage")}
          </button>
        </div>
      </div>
    </section>
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
