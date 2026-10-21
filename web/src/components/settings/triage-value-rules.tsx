import { ListFilter, X } from "lucide-react";

import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useI18n } from "@/i18n/i18n";
import type { TriageIntelligenceAction, TriageValueRule, TriageValueRuleKind } from "@/types/flow";
import { FeatureSelect, type FeatureSelectOption } from "./feature-primitives";
import type { TriageValueOption } from "./triage-value-options";

/** The filter button beside a Behavior row's select: pick a value to give it its own action. */
export function TriageValueFilter({ kind, title, options, rules, disabled, onAdd }: { kind: TriageValueRuleKind; title: string; options: TriageValueOption[]; rules: TriageValueRule[]; disabled?: boolean; onAdd: (option: TriageValueOption) => void }) {
  const { t } = useI18n();
  const taken = new Set(rules.filter((rule) => rule.kind === kind).map((rule) => rule.value));
  const available = options.filter((option) => !taken.has(option.value));
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button type="button" className="feature-filter-button" aria-label={`${t("Filter by value")}: ${t(title)}`} disabled={disabled} data-active={taken.size > 0 || undefined}>
          <ListFilter size={14} aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="feature-select-menu feature-value-menu">
        {available.length === 0 && <div className="feature-value-empty">{t("No matching options")}</div>}
        {available.map((option) => (
          <DropdownMenuItem key={option.value} data-i18n-ignore onSelect={() => onAdd(option)}>
            <span className="feature-select-icon" aria-hidden="true">{option.icon}</span>
            <span className="feature-select-label">{option.label}</span>
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** One per-value rule under its row: the value, its own Show / Auto-apply / Hide choice and a remove button. */
export function TriageValueRuleRow({ rule, option, actions, disabled, onChange, onRemove }: { rule: TriageValueRule; option?: TriageValueOption; actions: FeatureSelectOption[]; disabled?: boolean; onChange: (action: TriageIntelligenceAction) => void; onRemove: () => void }) {
  const { t } = useI18n();
  const label = option?.label ?? rule.value;
  return (
    <div className="feature-row feature-value-rule" data-testid={`triage-rule-${rule.kind}-${rule.value}`}>
      <span className="feature-row-icon" aria-hidden="true">{option?.icon}</span>
      <div><strong data-i18n-ignore>{label}</strong></div>
      <aside>
        <FeatureSelect label={`${label}`} value={rule.action} options={actions} disabled={disabled} onChange={(value) => onChange(value as TriageIntelligenceAction)} />
        <button type="button" className="feature-filter-button" aria-label={`${t("Remove rule")}: ${label}`} disabled={disabled} onClick={onRemove}>
          <X size={14} aria-hidden="true" />
        </button>
      </aside>
    </div>
  );
}
