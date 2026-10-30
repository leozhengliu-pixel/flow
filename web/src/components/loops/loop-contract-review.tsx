import { FileText, Check, X } from "lucide-react";
import { useState } from "react";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, LoopContractOutput, LoopOutputContract } from "@/types/flow";
import { CARDINALITY_LABELS, contractOutputSummary, contractTargetText } from "./loop-output-contract-model";

type ReviewItem = {
  id: string;
  index?: number;
  current?: LoopContractOutput;
  suggested?: LoopContractOutput;
  removed?: boolean;
  added?: boolean;
};

function outputKey(output: LoopContractOutput) {
  return `${output.kind}:${output.operation}`;
}

function comparableOutput(output: LoopContractOutput) {
  return {
    kind: output.kind,
    operation: output.operation,
    required: output.required,
    target: output.target,
    cardinality: output.cardinality,
    conditions: output.conditions,
    group: output.group,
  };
}

function reviewItems(current: LoopOutputContract, suggested: LoopOutputContract): ReviewItem[] {
  const items: ReviewItem[] = [];
  const used = new Set<number>();
  current.outputs.forEach((output, index) => {
    let position = suggested.outputs.findIndex((candidate, candidateIndex) => !used.has(candidateIndex) && outputKey(candidate) === outputKey(output));
    if (position < 0 && output.evidence) {
      position = suggested.outputs.findIndex((candidate, candidateIndex) => !used.has(candidateIndex) && candidate.evidence && candidate.evidence === output.evidence);
    }
    if (position < 0) {
      items.push({ id: `removed-${index}`, index, current: output, removed: true });
      return;
    }
    used.add(position);
    const candidate = suggested.outputs[position];
    if (JSON.stringify(comparableOutput(output)) !== JSON.stringify(comparableOutput(candidate))) items.push({ id: `output-${index}`, index, current: output, suggested: candidate });
  });
  suggested.outputs.forEach((output, index) => {
    if (!used.has(index)) items.push({ id: `added-${index}`, suggested: output, added: true });
  });
  return items;
}

function changedDetails(current: LoopContractOutput | undefined, suggested: LoopContractOutput | undefined, data: BootstrapData, t: (source: string) => string) {
  if (!current || !suggested) return [];
  const details: string[] = [];
  if (current.required !== suggested.required) details.push(`${t("Must produce")}: ${t(current.required ? "Yes" : "No")} → ${t(suggested.required ? "Yes" : "No")}`);
  const currentTarget = reviewTargetText(current, data, t);
  const suggestedTarget = reviewTargetText(suggested, data, t);
  if (currentTarget !== suggestedTarget) details.push(`${t("Target")}: ${currentTarget || t("Not set")} → ${suggestedTarget || t("Not set")}`);
  if (current.cardinality !== suggested.cardinality) details.push(`${t("How many")}: ${t(CARDINALITY_LABELS[current.cardinality] ?? "Any number")} → ${t(CARDINALITY_LABELS[suggested.cardinality] ?? "Any number")}`);
  if (JSON.stringify(current.conditions ?? []) !== JSON.stringify(suggested.conditions ?? [])) details.push(`${t("Conditions")}: ${current.conditions?.join("; ") || t("Not set")} → ${suggested.conditions?.join("; ") || t("Not set")}`);
  if (current.group !== suggested.group) details.push(t("Alternative outputs changed"));
  return details;
}

function reviewTargetText(output: LoopContractOutput, data: BootstrapData, t: (source: string) => string) {
  return contractTargetText(output, data, t).replace(t("document not chosen yet"), t("Choose an existing document"));
}

function ReviewOutputLine({ output, data, t }: { output?: LoopContractOutput; data: BootstrapData; t: (source: string) => string }) {
  if (!output) return null;
  return (
    <span className="loops-contract-review-summary">
      {output.kind === "document" && <FileText size={13} aria-hidden="true" />}
      {contractOutputSummary(output, data, t)}
    </span>
  );
}

export function LoopContractReview({ current, suggested, data, saving, onSave, onCancel }: { current: LoopOutputContract; suggested: LoopOutputContract; data: BootstrapData; saving: boolean; onSave: (contract: LoopOutputContract) => void; onCancel: () => void }) {
  const { t } = useI18n();
  const items = reviewItems(current, suggested);
  const [accepted, setAccepted] = useState<Set<string>>(new Set());
  const choose = (id: string, accept: boolean) => setAccepted((previous) => {
    const next = new Set(previous);
    if (accept) next.add(id); else next.delete(id);
    return next;
  });
  const save = () => {
    const next: LoopOutputContract = { ...current, outputs: current.outputs.map((output) => ({ ...output, target: { ...output.target } })) };
    const replacements = new Map<number, LoopContractOutput>();
    const removals = new Set<number>();
    for (const item of items) {
      if (!accepted.has(item.id)) continue;
      if (item.index != null && item.suggested) replacements.set(item.index, item.suggested);
      if (item.index != null && item.removed) removals.add(item.index);
      if (item.added && item.suggested) next.outputs.push(item.suggested);
    }
    next.outputs = next.outputs.map((output, index) => replacements.get(index) ?? output).filter((_, index) => !removals.has(index));
    onSave(next);
  };
  const acceptAll = () => setAccepted(new Set(items.map((item) => item.id)));
  return <div className="loops-contract-review" aria-label={t("Review suggested outputs")}>
    <div className="loops-contract-review-toolbar">
      <p className="loops-contract-hint">{t("Compare each output with the suggested contract, then choose what to keep.")}</p>
      {items.length > 0 && <button type="button" className="loops-ghost-button is-small" onClick={acceptAll}><Check size={12} />{t("Accept all")}</button>}
    </div>
    {items.length === 0 && <p className="loops-contract-hint">{t("No output changes suggested")}</p>}
    {items.map((item, position) => {
      const title = item.removed ? t("Removed output") : item.added ? t("New output") : t("Output {number}").replace("{number}", String((item.index ?? position) + 1));
      const details = changedDetails(item.current, item.suggested, data, t);
      return <fieldset key={item.id} className="loops-contract-review-output">
        <legend>{title}</legend>
        <div className="loops-contract-review-values">
          <div><small>{t("Your output")}</small><ReviewOutputLine output={item.current} data={data} t={t} /></div>
          <div><small>{t("Suggested output")}</small><ReviewOutputLine output={item.suggested} data={data} t={t} /></div>
        </div>
        {details.length > 0 && <p className="loops-contract-review-detail"><small>{t("What changed")}</small>{details.join(" · ")}</p>}
        <div className="loops-contract-actions">
          <button type="button" className="loops-ghost-button is-small" aria-pressed={!accepted.has(item.id)} onClick={() => choose(item.id, false)}><X size={12} />{t("Keep mine")}</button>
          <button type="button" className="loops-ghost-button is-small" aria-pressed={accepted.has(item.id)} onClick={() => choose(item.id, true)}><Check size={12} />{t("Accept suggestion")}</button>
        </div>
      </fieldset>;
    })}
    <div className="loops-contract-edit-footer">
      <button type="button" className="loops-ghost-button" disabled={saving} onClick={onCancel}>{t("Cancel")}</button>
      <button type="button" className="loops-primary-button" disabled={saving} onClick={save}>{saving ? t("Saving…") : t("Save review")}</button>
    </div>
  </div>;
}
