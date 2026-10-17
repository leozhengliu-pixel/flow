import { ArrowUpRight, ChevronRight, Presentation } from "lucide-react";
import { useMemo, useState } from "react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useI18n } from "@/i18n/i18n";
import { updateWorkspacePreferences } from "@/lib/api";
import type { UsageSettingsView } from "@/lib/app-routes";
import { toast } from "@/lib/toast";
import type { BootstrapData, SpendLimitReset, SpendLimitSettings } from "@/types/flow";
import { SettingsCrumb, SettingsPageTitle, SettingsSelect } from "./settings-primitives";
import { LimitDialog } from "./spend-limit-dialog";
import { UsageExplorer } from "./usage-explorer";
import { UsageOverrides } from "./usage-overrides";
import {
  RESET_OPTIONS,
  WEEKDAYS,
  formatLimit,
  hourLabel,
  ordinal,
  resetSchedule,
  resetsText,
  scheduleSummary,
  timeZoneAbbreviation,
} from "./spend-limits-model";
import "./usage-settings.css";

const DOCS_URL = "https://flow.app/docs/usage-and-limits";

type UsageProps = {
  data: BootstrapData;
  view?: UsageSettingsView;
  onReload: () => Promise<void>;
  onOpenView: (view?: UsageSettingsView) => void;
};

/** Saves a partial spend-limit update (the server merges it into the stored limits) and reloads the workspace. */
function useSpendLimitSaver(onReload: () => Promise<void>) {
  const { t } = useI18n();
  return async (patch: SpendLimitSettings) => {
    try {
      await updateWorkspacePreferences({ spendLimits: patch });
      await onReload();
    } catch (reason) {
      toast.error(reason instanceof Error ? reason.message : t("Could not save spend limits"));
      throw reason;
    }
  };
}

export function UsageSettings(props: UsageProps) {
  if (props.view === "analytics") return <UsageExplorer onBack={() => props.onOpenView()} data={props.data} />;
  if (props.view === "spend-limits") return <SpendLimitsPage {...props} />;
  if (props.view === "user-limits" || props.view === "loop-limits") {
    return <UsageOverrides data={props.data} kind={props.view === "user-limits" ? "user" : "loop"} onBack={() => props.onOpenView("spend-limits")} onReload={props.onReload} />;
  }
  return <UsageOverview {...props} />;
}

function Chevron() {
  return <ChevronRight size={16} className="usage-chevron" aria-hidden="true" />;
}

function DocsLink({ children }: { children: string }) {
  return (
    <a className="usage-docs-link" href={DOCS_URL} target="_blank" rel="noreferrer">
      {children}
      <ArrowUpRight size={11} aria-hidden="true" />
    </a>
  );
}

function UsageOverview({ data, onOpenView }: UsageProps) {
  const { t } = useI18n();
  const [creditsOpen, setCreditsOpen] = useState(false);
  const limits = data.workspaceSettings?.spendLimits;
  const limitSummary = [limits?.workspaceLimit, limits?.userLimit, limits?.loopLimit].some((value) => (value ?? 0) > 0) ? t("Limits set") : t("No limits set");
  const unavailable = t("Billing is not available in Flow");
  return (
    <div className="usage-page">
      <SettingsPageTitle description={t("Track usage, manage credits, and set spend limits across your workspace")}>
        {t("Usage & limits")}
      </SettingsPageTitle>

      <button type="button" className="usage-card usage-link-row usage-explorer-row" onClick={() => onOpenView("analytics")}>
        <span className="usage-icon-box">
          <Presentation size={16} aria-hidden="true" />
        </span>
        <span className="usage-row-copy">
          <strong>{t("Usage explorer")}</strong>
          <span>{t("View usage by team, feature, and user")}</span>
        </span>
        <Chevron />
      </button>

      <section className="usage-section">
        <header className="usage-section-header">
          <div>
            <h3>{t("AI credits")}</h3>
            <p>
              {t("Credits apply to usage of")} <em>{t("Coding sessions")}</em> {t("and")} <em>{t("Loops")}</em>. <DocsLink>{t("Docs")}</DocsLink>
            </p>
          </div>
          <button type="button" className="usage-pill-button usage-raised" onClick={() => setCreditsOpen(true)}>
            {t("Add credits")}
          </button>
        </header>
        <div className="usage-card usage-row usage-row-short">
          <strong>{t("Workspace credits")}</strong>
          <strong>{t("$0.00 remaining")}</strong>
        </div>
        <div className="usage-card usage-row">
          <span className="usage-row-copy">
            <strong>{t("Automatic reload")}</strong>
            <span className="usage-status-line">
              <i aria-hidden="true" />
              {t("Disabled")} - {t("Automatically top up credits when your balance runs low")}
            </span>
          </span>
          <button type="button" className="usage-pill-button" disabled title={unavailable}>
            {t("Manage")}
          </button>
        </div>
        <button type="button" className="usage-card usage-link-row" onClick={() => onOpenView("spend-limits")}>
          <span className="usage-row-copy">
            <strong>{t("Spend limits")}</strong>
            <span>{t("Set workspace, user, and Loop limits")}</span>
          </span>
          <span className="usage-row-meta">{limitSummary}</span>
          <Chevron />
        </button>
      </section>
      {creditsOpen && <AddCreditsDialog onClose={() => setCreditsOpen(false)} />}
    </div>
  );
}

/** "Add credits": the reference dialog. Flow has no payment provider, so it cannot be submitted and says so. */
function AddCreditsDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const [amount, setAmount] = useState<"50" | "75" | "other">("50");
  const [agreed, setAgreed] = useState(false);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="usage-dialog usage-credits-dialog">
        <DialogTitle>{t("Add credits")}</DialogTitle>
        <p className="usage-dialog-description">{t("Unused credits expire 12 months after purchase")}</p>
        <div className="usage-amount-box">
          <strong>{t("Amount")}</strong>
          <div role="radiogroup" aria-label={t("Amount")}>
            {(["50", "75", "other"] as const).map((option) => (
              <button
                key={option}
                type="button"
                role="radio"
                aria-checked={amount === option}
                className={amount === option ? "is-selected" : ""}
                onClick={() => setAmount(option)}
              >
                {option === "other" ? t("Other") : `$${option}`}
              </button>
            ))}
          </div>
        </div>
        <label className="usage-agree">
          <input type="checkbox" checked={agreed} onChange={(event) => setAgreed(event.target.checked)} />
          <span>
            {t("I agree to the")} <b>{t("AI Addendum")}</b> {t("and")} <b>{t("Acceptable Use Policy")}</b>
          </span>
        </label>
        <footer>
          <span className="usage-dialog-note">{t("Billing is not available in Flow")}</span>
          <button type="button" className="usage-pill-button usage-raised" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button type="button" className="usage-primary-button" disabled title={t("Billing is not available in Flow")}>
            {t("Add credits")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

type LimitKind = "workspace" | "user" | "loop";

function SpendLimitsPage({ data, onOpenView, onReload }: UsageProps) {
  const { t } = useI18n();
  const save = useSpendLimitSaver(onReload);
  const limits: SpendLimitSettings = data.workspaceSettings?.spendLimits ?? {};
  const zone = useMemo(() => timeZoneAbbreviation(), []);
  const schedule = resetSchedule(limits, data.workspace.createdAt);
  const [editing, setEditing] = useState<LimitKind | undefined>();
  const [editingSchedule, setEditingSchedule] = useState(false);
  const config = {
    workspace: { amount: limits.workspaceLimit, reset: limits.workspaceReset ?? "weekly", amountKey: "workspaceLimit", resetKey: "workspaceReset", label: t("Default limit"), dialog: t("Workspace limit") },
    user: { amount: limits.userLimit, reset: limits.userReset ?? "weekly", amountKey: "userLimit", resetKey: "userReset", label: t("Per-user limit"), dialog: t("Per-user limit") },
    loop: { amount: limits.loopLimit, reset: limits.loopReset ?? "weekly", amountKey: "loopLimit", resetKey: "loopReset", label: t("Per-loop limit"), dialog: t("Per-loop limit") },
  } as const;
  const block = (kind: LimitKind) => {
    const item = config[kind];
    return (
      <>
        <div className="usage-card usage-row">
          <span className="usage-row-copy">
            <strong>{item.label}</strong>
            <span>{item.amount ? t("{amount} per period").replace("{amount}", formatLimit(item.amount)) : t("No limit set")}</span>
          </span>
          <button type="button" className="usage-pill-button" onClick={() => setEditing(kind)}>
            {t("Set limit")}
          </button>
        </div>
        <div className="usage-card usage-row">
          <span className="usage-row-copy">
            <strong>{t("Reset frequency")}</strong>
            <span>{t(resetsText(item.reset, schedule, zone))}</span>
          </span>
          <SettingsSelect
            label={t("Reset frequency")}
            value={item.reset}
            options={RESET_OPTIONS.map((option) => ({ value: option.value, label: t(option.label) }))}
            onChange={(value) => void save({ [item.resetKey]: value as SpendLimitReset }).catch(() => undefined)}
          />
        </div>
      </>
    );
  };
  const overrides = (view: "user-limits" | "loop-limits", description: string) => (
    <button type="button" className="usage-card usage-link-row" onClick={() => onOpenView(view)}>
      <span className="usage-row-copy">
        <strong>{t("Overrides")}</strong>
        <span>{description}</span>
      </span>
      <Chevron />
    </button>
  );
  const active = editing ? config[editing] : undefined;
  return (
    <div className="usage-page usage-limits-page">
      <SettingsCrumb onClick={() => onOpenView()}>{t("Usage & limits")}</SettingsCrumb>
      <SettingsPageTitle
        description={
          <>
            {t("Manage workspace, user, and loop limits.")} <DocsLink>{t("Docs")}</DocsLink>
          </>
        }
      >
        {t("Spend limits")}
      </SettingsPageTitle>

      <div className="usage-card usage-row">
        <span className="usage-row-copy">
          <strong>{t("Reset schedule")}</strong>
          <span>{scheduleSummary(schedule, zone)}</span>
        </span>
        <button type="button" className="usage-pill-button" onClick={() => setEditingSchedule(true)}>
          {t("Edit")}
        </button>
      </div>

      <section className="usage-section">
        <h3>{t("Workspace limit")}</h3>
        <div className="usage-group">{block("workspace")}</div>
      </section>
      <section className="usage-section">
        <h3>{t("User limits")}</h3>
        <div className="usage-group">
          {block("user")}
          {overrides("user-limits", t("View and manage per-user limits and usage"))}
        </div>
      </section>
      <section className="usage-section">
        <h3>{t("Loop limits")}</h3>
        <div className="usage-group">
          {block("loop")}
          {overrides("loop-limits", t("View and manage per-loop limits and usage"))}
        </div>
      </section>

      {editing && active && (
        <LimitDialog
          title={active.dialog}
          initial={active.amount}
          onClose={() => setEditing(undefined)}
          onSave={(amount) => save({ [active.amountKey]: amount })}
        />
      )}
      {editingSchedule && (
        <ScheduleDialog
          zone={zone}
          initial={schedule}
          onClose={() => setEditingSchedule(false)}
          onSave={(next) => save({ dailyResetHour: next.hour, weeklyResetDay: next.weekday, monthlyResetDay: next.monthDay })}
        />
      )}
    </div>
  );
}

function ScheduleDialog({
  zone,
  initial,
  onSave,
  onClose,
}: {
  zone: string;
  initial: ReturnType<typeof resetSchedule>;
  onSave: (next: ReturnType<typeof resetSchedule>) => Promise<void>;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [next, setNext] = useState(initial);
  const [saving, setSaving] = useState(false);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="usage-dialog usage-schedule-dialog">
        <DialogTitle>{t("Reset schedule")}</DialogTitle>
        <p className="usage-dialog-description">{t("Choose when each spend limit period starts over")} ({zone})</p>
        <div className="usage-schedule-grid">
          <strong>{t("Daily")}</strong>
          <SettingsSelect
            label={t("Daily reset time")}
            value={String(next.hour)}
            options={Array.from({ length: 24 }, (_, hour) => ({ value: String(hour), label: hourLabel(hour) }))}
            onChange={(value) => setNext({ ...next, hour: Number(value) })}
          />
          <strong>{t("Weekly")}</strong>
          <SettingsSelect
            label={t("Weekly reset day")}
            value={String(next.weekday)}
            options={WEEKDAYS.map((day, index) => ({ value: String(index), label: t(day) }))}
            onChange={(value) => setNext({ ...next, weekday: Number(value) })}
          />
          <strong>{t("Monthly")}</strong>
          <SettingsSelect
            label={t("Monthly reset day")}
            value={String(next.monthDay)}
            options={Array.from({ length: 28 }, (_, index) => ({ value: String(index + 1), label: `${t("The")} ${ordinal(index + 1)}` }))}
            onChange={(value) => setNext({ ...next, monthDay: Number(value) })}
          />
        </div>
        <footer>
          <button type="button" className="usage-pill-button usage-raised" onClick={onClose}>
            {t("Cancel")}
          </button>
          <button
            type="button"
            className="usage-primary-button"
            disabled={saving}
            onClick={() => {
              setSaving(true);
              onSave(next).then(onClose, () => setSaving(false));
            }}
          >
            {t("Save")}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}
