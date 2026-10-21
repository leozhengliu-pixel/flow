import { ChevronRight, Info } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import { formatCredits, useAICredits } from "@/lib/ai-credits";
import type { BootstrapData } from "@/types/flow";
import { SettingsPageTitle } from "./settings-primitives";
import { CheckGlyph } from "./billing-glyphs";
import "./billing-settings.css";

const DOCS_URL = "https://flow.app/docs/billing";

/** Everything the paid plan lists in the reference app; Flow includes all of it. */
const PLAN_FEATURES = [
  "Unlimited teams",
  "Team initiatives",
  "Private teams",
  "Guest accounts",
  "Advanced authentication",
  "Support integrations",
  "Triage responsibility",
  "Triage rules",
  "Basic loops",
  "Insights",
  "Restrict team creation",
  "Restrict label management",
  "Restrict template management",
  "Restrict automation management",
  "Restrict pinned view management",
  "Triage Intelligence",
  "Issue discussion summaries",
  "Code Intelligence",
  "Issue SLAs",
  "Asks",
  "1 sub-teams",
  "15 releases",
  "Issue activity summary",
  "Flow Agent for support integrations",
  "Team owners",
];

/**
 * Settings › Billing. The layout, plan card and feature list follow the reference app. Flow has no billing provider, so
 * the plan actions are disabled and say so; the workspace is on the free plan with every feature included.
 */
export function BillingSettings({ data, onOpenUsage }: { data: BootstrapData; onOpenUsage: () => void }) {
  const { t } = useI18n();
  const users = (data.members ?? []).filter((member) => member.status === "active" && !member.user.app).length || 1;
  const unavailable = t("Billing is not available in Flow");
  const credits = useAICredits();
  return (
    <div className="billing-page">
      <SettingsPageTitle
        description={
          <>
            {t("For questions about billing,")}{" "}
            <a className="billing-link" href={DOCS_URL} target="_blank" rel="noreferrer">
              {t("contact us")}
            </a>
          </>
        }
        action={
          <button type="button" className="billing-ghost-button billing-all-plans" disabled title={unavailable}>
            {t("All plans")}
            <ChevronRight size={14} aria-hidden="true" />
          </button>
        }
      >
        {t("Billing")}
      </SettingsPageTitle>

      <section className="billing-card billing-current-plan" aria-label={t("Current plan")}>
        <div>
          <h3>{t("Free plan")}</h3>
          <p>{t("Free for all users")} · {users === 1 ? t("1 user") : `${users} ${t("users")}`}</p>
        </div>
        <button type="button" className="billing-ghost-button" disabled title={unavailable}>
          {t("Switch plan")}
        </button>
      </section>

      <section className="billing-card billing-upgrade" aria-label={t("Business plan")}>
        <header>
          <div>
            <h3>
              {t("Business plan")} <span className="billing-badge">{t("Included")}</span>
            </h3>
            <p>{t("Free in Flow")}</p>
          </div>
          <div className="billing-upgrade-actions">
            <button type="button" className="billing-ghost-button billing-large" disabled title={unavailable}>
              {t("View all plans")}
            </button>
            <button type="button" className="billing-primary-button" disabled title={unavailable}>
              {t("Upgrade now")}
            </button>
          </div>
        </header>
        <ul className="billing-features">
          {PLAN_FEATURES.map((feature) => (
            <li key={feature} title={t(feature)}>
              <CheckGlyph />
              <span>{t(feature)}</span>
            </li>
          ))}
        </ul>
        <div className="billing-notice" role="note">
          <Info size={14} aria-hidden="true" />
          <span>{t("Billing is not available in Flow. Every plan feature is included in this workspace at no cost.")}</span>
        </div>
      </section>

      <button type="button" className="billing-card billing-usage-row" onClick={onOpenUsage}>
        <span>
          <strong>{t("Usage")}</strong>
          {credits && <span>{t("{amount} remaining").replace("{amount}", formatCredits(credits.remainingMicros))}</span>}
        </span>
        <ChevronRight size={16} aria-hidden="true" />
      </button>

      <h3 className="billing-invoices-title">{t("Recent invoices")}</h3>
      <section className="billing-card billing-invoices" aria-label={t("Recent invoices")}>
        <p>{t("No invoices yet")}</p>
      </section>
    </div>
  );
}
