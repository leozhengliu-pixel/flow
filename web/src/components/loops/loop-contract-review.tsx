import { useState } from "react";
import { Check, X } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import type { LoopContractOutput, LoopOutputContract } from "@/types/flow";
import { CONTRACT_KIND_LABELS, CONTRACT_OPERATION_LABELS } from "./loop-output-contract-model";

type ReviewField = {
  id: string;
  label: string;
  before: unknown;
  after: unknown;
  apply: (contract: LoopOutputContract) => void;
};

const outputFields = {
  kind: "Output kind", operation: "Operation", required: "Must produce", cardinality: "How many", conditions: "Conditions", group: "Alternative group",
} as const;
const targetFields = {
  mode: "Target", titleTemplate: "Title template", placeholders: "Title placeholders", title: "Document title", documentId: "Document", ambiguous: "Document not chosen",
  team: "Team name", teamId: "Team", project: "Project name", projectId: "Project", initiative: "Initiative name", initiativeId: "Initiative", match: "Which issues", description: "Target description",
} as const;

function reviewFields(current: LoopOutputContract, suggested: LoopOutputContract): ReviewField[] {
  const fields: ReviewField[] = [];
  const add = (field: ReviewField) => {
    if (JSON.stringify(field.before ?? null) !== JSON.stringify(field.after ?? null)) fields.push(field);
  };
  for (const [key, label] of Object.entries({ readOnly: "Reply only", always: "Required even when nothing changed", skipConditions: "Skip conditions" }) as ["readOnly" | "always" | "skipConditions", string][]) {
    add({ id: key, label, before: current[key], after: suggested[key], apply: (next) => Object.assign(next, { [key]: suggested[key] }) });
  }
  const used = new Set<number>();
  current.outputs.forEach((output, index) => {
    let position = suggested.outputs.findIndex((item, at) => !used.has(at) && item.kind === output.kind && item.operation === output.operation);
    if (position < 0) position = suggested.outputs.findIndex((item, at) => !used.has(at) && item.kind === output.kind);
    // Reordering should not suggest changing unrelated outputs into one another.
    const other = position < 0 ? undefined : suggested.outputs[position];
    if (!other) {
      add({ id: `${index}.remove`, label: "Output {number}", before: output, after: undefined, apply: (next) => { delete next.outputs[index]; } });
      return;
    }
    used.add(position);
    for (const [key, label] of Object.entries(outputFields) as [keyof typeof outputFields, string][]) {
      add({ id: `${index}.${key}`, label: `Output {number}|${label}`, before: output[key], after: other[key], apply: (next) => Object.assign(next.outputs[index], { [key]: other[key] }) });
    }
    for (const [key, label] of Object.entries(targetFields) as [keyof typeof targetFields, string][]) {
      add({ id: `${index}.target.${key}`, label: `Output {number}|${label}`, before: output.target[key], after: other.target[key], apply: (next) => Object.assign(next.outputs[index].target, { [key]: other.target[key] }) });
    }
  });
  suggested.outputs.forEach((output, index) => {
    if (!used.has(index)) add({ id: `add-${index}`, label: "New output", before: undefined, after: output, apply: (next) => { next.outputs.push(output); } });
  });
  return fields;
}

export function LoopContractReview({ current, suggested, saving, onSave, onCancel }: { current: LoopOutputContract; suggested: LoopOutputContract; saving: boolean; onSave: (contract: LoopOutputContract) => void; onCancel: () => void }) {
  const { t } = useI18n();
  const fields = reviewFields(current, suggested);
  const [accepted, setAccepted] = useState<Set<string>>(new Set());
  const format = (value: unknown): string => {
    if (value == null || value === "" || Array.isArray(value) && value.length === 0) return t("Not set");
    if (typeof value === "boolean") return t(value ? "Yes" : "No");
    if (Array.isArray(value)) return value.map(format).join(", ");
    if (typeof value === "object") {
      const output = value as LoopContractOutput;
      if (output.kind && output.operation) return `${t(CONTRACT_OPERATION_LABELS[output.operation])} ${t(CONTRACT_KIND_LABELS[output.kind])}: ${format(output.target)}`;
      return Object.values(value).map(format).join(" · ");
    }
    return String(value);
  };
  const save = () => {
    const next: LoopOutputContract = { ...current, outputs: current.outputs.map((output) => ({ ...output, target: { ...output.target } })) };
    for (const field of fields) if (accepted.has(field.id)) field.apply(next);
    next.outputs = next.outputs.filter(Boolean);
    onSave(next);
  };
  return <div className="loops-contract-review" aria-label={t("Review suggested outputs")}>
    {fields.length === 0 && <p className="loops-contract-hint">{t("No output changes suggested")}</p>}
    {fields.map((field) => {
      const index = Number.parseInt(field.id, 10);
      const label = field.label.split("|").map((part) => t(part).replace("{number}", String(index + 1))).join(" · ");
      const choose = (accept: boolean) => setAccepted((previous) => {
        const next = new Set(previous);
        if (accept) next.add(field.id); else next.delete(field.id);
        return next;
      });
      return <fieldset key={field.id} className="loops-contract-review-field">
        <legend>{label}</legend>
        <div className="loops-contract-review-values">
          <div><small>{t("Your output")}</small><span data-i18n-ignore>{format(field.before)}</span></div>
          <div><small>{t("Suggested output")}</small><span data-i18n-ignore>{format(field.after)}</span></div>
        </div>
        <div className="loops-contract-actions">
          <button type="button" className="loops-ghost-button is-small" aria-pressed={!accepted.has(field.id)} onClick={() => choose(false)}><X size={12} />{t("Keep mine")}</button>
          <button type="button" className="loops-ghost-button is-small" aria-pressed={accepted.has(field.id)} onClick={() => choose(true)}><Check size={12} />{t("Accept suggestion")}</button>
        </div>
      </fieldset>;
    })}
    <div className="loops-contract-edit-footer">
      <button type="button" className="loops-ghost-button" disabled={saving} onClick={onCancel}>{t("Cancel")}</button>
      <button type="button" className="loops-primary-button" disabled={saving} onClick={save}>{saving ? t("Saving…") : t("Save review")}</button>
    </div>
  </div>;
}
