import { AgentCodingGlyph } from "@/components/ui/agent-glyph";
import { useI18n } from "@/i18n/i18n";
import type { CodingSettings } from "@/types/flow";
import { CodingCard, CodingRow, CodingSection, CodingSelect } from "./coding-settings-primitives";
import { useCodingEnvironments, useCodingPolicy } from "./use-coding-settings";
import "./coding-sessions.css";

/**
 * Settings › Loops › Capabilities: which repositories loops may open coding sessions in.
 * Coding sessions settings link here ("Repository access for Loops"), as in the reference app.
 */
export function LoopCodeAccessSection({ disabled }: { disabled?: boolean }) {
  const { t } = useI18n();
  const { settings, update } = useCodingPolicy(undefined, true);
  const { environments } = useCodingEnvironments(false);
  const selected = new Set(settings.loopRepositories);
  return (
    <CodingSection id="capabilities" title="Capabilities">
      <CodingCard className="cs-loop-code-access">
        <CodingRow title="Allow loops to access code" description="Select which repositories loops can access" figure icon={<AgentCodingGlyph size={16} />}>
          <CodingSelect
            label="Allow loops to access code"
            value={settings.loopRepositoryAccess}
            disabled={disabled}
            options={[
              { value: "none", label: "Disabled" },
              { value: "selected", label: "Selected repositories" },
              { value: "all", label: "All repositories" },
            ]}
            onChange={(value) => void update({ loopRepositoryAccess: value as CodingSettings["loopRepositoryAccess"] })}
          />
        </CodingRow>
        {settings.loopRepositoryAccess === "selected" && (
          <div className="cs-loop-repos">
            {environments.length === 0 && <p className="cs-card-text">{t("Create an environment to give Loops access to its repository.")}</p>}
            {environments.map((environment) => (
              <label key={environment.id} className="cs-check-row">
                <input
                  type="checkbox"
                  checked={selected.has(environment.id)}
                  disabled={disabled}
                  onChange={(event) => {
                    const next = new Set(selected);
                    if (event.target.checked) next.add(environment.id);
                    else next.delete(environment.id);
                    void update({ loopRepositories: [...next] });
                  }}
                />
                <span data-i18n-ignore>{environment.repository}</span>
                <small data-i18n-ignore>{environment.name}</small>
              </label>
            ))}
          </div>
        )}
      </CodingCard>
    </CodingSection>
  );
}
