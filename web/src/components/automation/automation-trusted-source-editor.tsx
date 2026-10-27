/**
 * LS-0094 allowlist editor options — mounts sourcesForEditor on automation editors.
 */
import { useEffect, useMemo, useState } from "react";
import { Check, Shield } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import {
  AutomationTrustedSourceEditorOptions,
  type TrustedSourceEditorOption,
} from "@/lib/automation-trusted-source-editor-options";
import {
  EMAIL_TRUSTED_SOURCE_KEY,
  getTrustedSourcesMode,
  type TrustedSourcesMode,
} from "@/lib/automation-source-key";
import type { User } from "@/types/flow";
import "./automation-trust.css";

type Props = {
  users: User[];
  integrationServices: string[];
  policySourceKeys?: string[];
  trustedSourcesMode?: TrustedSourcesMode | string | null;
  trustedSourcesAllowlist?: string[] | null;
  disabled?: boolean;
  /** When true, toggling updates the workspace allowlist via onAllowlistChange. */
  onAllowlistChange?: (next: {
    trustedSourcesMode: TrustedSourcesMode;
    trustedSourcesAllowlist: string[];
  }) => void;
  /** Also persist keys referenced by this automation. */
  onPolicyKeysChange?: (keys: string[]) => void;
  /** Hide the built-in title when the host already renders a section heading (workspace settings). */
  hideHeader?: boolean;
};

export function AutomationTrustedSourceEditor({
  users,
  integrationServices,
  policySourceKeys,
  trustedSourcesMode,
  trustedSourcesAllowlist,
  disabled,
  onAllowlistChange,
  onPolicyKeysChange,
  hideHeader = false,
}: Props) {
  const { t } = useI18n();
  const [options, setOptions] = useState<TrustedSourceEditorOption[]>([]);
  // The context the shown options were hydrated for; a new context is "hydrating" until its sources load.
  const [hydratedCtx, setHydratedCtx] = useState<object>();

  const settings = useMemo(
    () => ({
      trustedSourcesMode: trustedSourcesMode ?? "none",
      trustedSourcesAllowlist: trustedSourcesAllowlist ?? [],
    }),
    [trustedSourcesMode, trustedSourcesAllowlist],
  );
  const hydrationCtx = useMemo(
    () => ({ users, integrationServices, policySourceKeys, settings }),
    [users, integrationServices, policySourceKeys, settings],
  );
  const hydrating = hydratedCtx !== hydrationCtx;

  useEffect(() => {
    let cancelled = false;
    void AutomationTrustedSourceEditorOptions.hydrateSources(hydrationCtx).then(() => {
      if (cancelled) return;
      setOptions(AutomationTrustedSourceEditorOptions.sourcesForEditor(hydrationCtx));
      setHydratedCtx(hydrationCtx);
    });
    return () => {
      cancelled = true;
    };
  }, [hydrationCtx]);

  const mode = getTrustedSourcesMode(settings);
  const editable = Boolean(onAllowlistChange) && !disabled;

  const setAllowed = (id: string, allowed: boolean) => {
    if (!onAllowlistChange) return;
    const current = new Set(
      AutomationTrustedSourceEditorOptions.trustedSourcesAllowlist(settings),
    );
    if (allowed) current.add(id);
    else current.delete(id);
    const nextKeys = [...current];
    onAllowlistChange({
      trustedSourcesMode: "allowlist",
      trustedSourcesAllowlist: nextKeys,
    });
    if (onPolicyKeysChange && policySourceKeys) {
      const policy = new Set(policySourceKeys);
      if (allowed) policy.add(id);
      onPolicyKeysChange([...policy]);
    }
  };

  const enableAllowlist = () => {
    onAllowlistChange?.({
      trustedSourcesMode: "allowlist",
      trustedSourcesAllowlist:
        settings.trustedSourcesAllowlist?.length
          ? [...settings.trustedSourcesAllowlist]
          : [EMAIL_TRUSTED_SOURCE_KEY],
    });
  };

  const disableAllowlist = () => {
    onAllowlistChange?.({
      trustedSourcesMode: "none",
      trustedSourcesAllowlist: [],
    });
  };

  return (
    <section
      className="automation-trusted-editor"
      data-testid="automation-trusted-source-editor"
      aria-label={t("Trusted sources")}
    >
      {!hideHeader && <header className="automation-trusted-editor-header">
        <div>
          <h3>{t("Trusted sources")}</h3>
          <p>
            {t(
              "Only content from allowed sources can trigger this automation when trust mode is on.",
            )}
          </p>
        </div>
        <Shield size={16} aria-hidden />
      </header>}

      <div className="automation-trusted-mode">
        <button
          type="button"
          className={mode === "none" ? "is-active" : undefined}
          disabled={!editable}
          onClick={disableAllowlist}
        >
          {t("No trusted sources")}
        </button>
        <button
          type="button"
          className={mode === "allowlist" ? "is-active" : undefined}
          disabled={!editable}
          onClick={enableAllowlist}
        >
          {t("Allowlist")}
        </button>
      </div>

      {mode === "allowlist" && (
        <ul className="automation-trusted-list" role="list">
          {hydrating && (
            <li className="automation-trusted-empty">{t("Loading…")}</li>
          )}
          {!hydrating &&
            options.map((source) => (
              <li key={source.id} className="automation-trusted-row">
                <div className="automation-trusted-identity">
                  {source.iconUrl ? (
                    <img src={source.iconUrl} alt="" />
                  ) : (
                    <span className="automation-trusted-glyph" aria-hidden />
                  )}
                  <span data-i18n-ignore>{source.name}</span>
                </div>
                <button
                  type="button"
                  className={`automation-trusted-toggle${source.allowed ? " is-on" : ""}`}
                  aria-pressed={source.allowed}
                  disabled={!editable}
                  onClick={() => setAllowed(source.id, !source.allowed)}
                  title={
                    source.allowed ? t("Remove from allowlist") : t("Allow")
                  }
                >
                  {source.allowed && <Check size={12} aria-hidden />}
                  {source.allowed ? t("Allowed") : t("Allow")}
                </button>
              </li>
            ))}
          {!hydrating && !options.length && (
            <li className="automation-trusted-empty">
              {t("No external sources available yet.")}
            </li>
          )}
        </ul>
      )}
    </section>
  );
}
