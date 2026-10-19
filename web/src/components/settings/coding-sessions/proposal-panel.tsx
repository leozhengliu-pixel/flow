import { useState } from "react";
import { Sparkles } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import { applyCodingEnvironmentProposal } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { CodingEnvironment, CodingEnvironmentProposal } from "@/types/flow";
import { lineDiff, proposalChanges } from "./coding-sessions-model";
import { CodingButton } from "./coding-settings-primitives";
import { errorMessage } from "./use-coding-settings";

/** Review of a Configure/Fix with agent proposal: per-field diff, then Apply or Discard (§18). */
export function ProposalPanel({ environment, proposal, disabled, onApplied }: { environment: CodingEnvironment; proposal: CodingEnvironmentProposal; disabled?: boolean; onApplied: (environment: CodingEnvironment, discarded: boolean) => void }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const changes = proposalChanges(environment, proposal);
  const resolve = async (discard: boolean) => {
    setBusy(true);
    try {
      const next = await applyCodingEnvironmentProposal(environment.id, discard);
      onApplied(next, discard);
      toast.success(t(discard ? "Proposal discarded" : "Proposal applied"));
    } catch (reason) {
      toast.error(errorMessage(reason, "Could not update the proposal"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="cs-proposal" data-testid="proposal-panel">
      <div className="feature-row cs-row">
        <span className="feature-row-icon"><Sparkles size={16} /></span>
        <div>
          <strong>
            {t("Proposed setup")}
            <span className={`cs-badge ${proposal.verified ? "is-ready" : "is-queued"}`}>{t(proposal.verified ? "Verified" : "Not verified")}</span>
          </strong>
          {proposal.explanation && <span data-i18n-ignore>{proposal.explanation}</span>}
        </div>
        <aside>
          <CodingButton disabled={disabled || busy} onClick={() => void resolve(true)}>Discard</CodingButton>
          <CodingButton primary disabled={disabled || busy} onClick={() => void resolve(false)}>Apply</CodingButton>
        </aside>
      </div>
      {proposal.missingSecrets && proposal.missingSecrets.length > 0 && (
        <p className="cs-card-text cs-warning-text">
          {t("Add these secrets before the next snapshot:")} <code data-i18n-ignore>{proposal.missingSecrets.join(", ")}</code>
        </p>
      )}
      {changes.length === 0 && <p className="cs-card-text">{t("The proposal matches the current configuration.")}</p>}
      {changes.map((change) => (
        <div key={change.field} className="cs-diff">
          <h4>{t(change.field)}</h4>
          <pre aria-label={t("Changes to {field}").replace("{field}", t(change.field))}>
            {lineDiff(change.before, change.after).map((line, index) => (
              <span key={index} className={`cs-diff-line is-${line.kind}`} data-i18n-ignore>
                {line.kind === "add" ? "+ " : line.kind === "remove" ? "- " : "  "}
                {line.text}
                {"\n"}
              </span>
            ))}
          </pre>
        </div>
      ))}
    </div>
  );
}
