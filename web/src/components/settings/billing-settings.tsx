import { ChevronRight, Info } from "lucide-react";
import { useDesktopSettings } from '@/hooks/use-desktop-settings';
import { useI18n } from "@/i18n/i18n";
import { formatCredits, useAICredits } from "@/lib/ai-credits";
import { settingsPath } from '@/lib/app-routes';
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
  const desktop = useDesktopSettings();
  const UsageControl = desktop ? 'a' : 'button';
  if (!desktop) {
    return <MobileBillingSettings data={data} onOpenUsage={onOpenUsage} />;
  }
  return (
    <div className="billing-page">
      <SettingsPageTitle
        description={
          <>
            {t("For questions about billing,")}{desktop ? '' : ' '}
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

      <UsageControl type={desktop ? undefined : 'button'} href={desktop ? settingsPath(data.workspace.urlKey, 'usage') : undefined} className="billing-card billing-usage-row" onClick={event => {
        if (desktop) {
          if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
        }
        onOpenUsage();
      }}>
        <span>
          <strong>{t("Usage")}</strong>
          {credits && <span>{t("{amount} remaining").replace("{amount}", formatCredits(credits.remainingMicros))}</span>}
        </span>
        <ChevronRight size={16} aria-hidden="true" />
      </UsageControl>

      <h3 className="billing-invoices-title">{t("Recent invoices")}</h3>
      <section className="billing-card billing-invoices" aria-label={t("Recent invoices")}>
        <p>{t("No invoices yet")}</p>
      </section>
    </div>
  );
}

const MOBILE_FEATURES = [
  "Unlimited file upload size",
  "Unlimited issues",
  "Admin roles",
  "Guided reviews",
  "Loops",
  "Third-party app management",
  "Restrict new user invitations",
  "Restrict agent invocation to full workspace members",
];

function MobileCheckGlyph() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M14.6722 3.27047C15.0901 3.6482 15.1112 4.28103 14.7195 4.68394L6.94174 12.6839C6.74966 12.8815 6.48271 12.9955 6.2019 12.9999C5.9211 13.0042 5.65047 12.8986 5.45189 12.7071L1.30374 8.70711C0.898753 8.31659 0.898753 7.68342 1.30374 7.2929C1.70873 6.90237 2.36534 6.90237 2.77033 7.2929L6.16115 10.5626L13.2064 3.31606C13.5981 2.91315 14.2544 2.89274 14.6722 3.27047Z" fill="currentColor" />
    </svg>
  );
}

function MobileArrowGlyph() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M11.75 10a.75.75 0 1 1-1.5 0V6.81l-4.72 4.72a.75.75 0 1 1-1.06-1.06l4.72-4.72H6a.75.75 0 0 1 0-1.5h5a.75.75 0 0 1 .75.75z" fill="currentColor" />
    </svg>
  );
}

function MobileChevronGlyph({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path d="M5.46967 11.4697C5.17678 11.7626 5.17678 12.2374 5.46967 12.5303C5.76256 12.8232 6.23744 12.8232 6.53033 12.5303L10.5303 8.53033C10.8207 8.23999 10.8236 7.77014 10.5368 7.47624L6.63419 3.47624C6.34492 3.17976 5.87009 3.17391 5.57361 3.46318C5.27713 3.75244 5.27128 4.22728 5.56054 4.52376L8.94583 7.99351L5.46967 11.4697Z" fill="currentColor" />
    </svg>
  );
}

function MobileBillingSettings({ data, onOpenUsage }: { data: BootstrapData; onOpenUsage: () => void }) {
  const { t } = useI18n();
  const users = (data.members ?? []).filter((member) => member.status === "active" && !member.user.app).length || 1;
  const features = [`${data.teams.length} teams`, ...MOBILE_FEATURES];
  return (
    <div className="billing-mobile">
      <header className="billing-mobile-title">
        <h1>{t("Billing")}</h1>
        <div className="billing-mobile-description">
          <span>{t("For questions about billing,")}</span>
          <a className="billing-link" href={DOCS_URL} target="_blank" rel="noreferrer">{t("contact us")}</a>
        </div>
        <button type="button" className="billing-mobile-all-plans">{t("All plans")}<MobileChevronGlyph size={14} /></button>
      </header>

      <section className="billing-mobile-card billing-mobile-current" aria-label={t("Current plan")}>
        <div>
          <h2>{t("Free plan")}</h2>
          <p>{t("Free for all users")} · {users === 1 ? t("1 user") : `${users} ${t("users")}`}</p>
        </div>
        <button type="button" className="billing-mobile-ghost">{t("Switch plan")}</button>
      </section>

      <section className="billing-mobile-card billing-mobile-upgrade" aria-label={t("Basic plan")}>
        <div className="billing-mobile-upgrade-main">
          <div className="billing-mobile-upgrade-copy">
            <h2>{t("Upgrade to Basic plan")}</h2>
            <p><span>$ 12</span> <span>{t("per user/mo")}</span></p>
          </div>
          <div className="billing-mobile-upgrade-actions">
            <button type="button" className="billing-mobile-ghost billing-mobile-view-plans">{t("View all plans")}</button>
            <button type="button" className="billing-mobile-primary">{t("Upgrade now")}</button>
          </div>
        </div>
        <div className="billing-mobile-features">
          {features.map(feature => <div className="billing-mobile-feature" key={feature}><MobileCheckGlyph /><span title={t(feature)}>{t(feature)}</span></div>)}
        </div>
      </section>

      <div className="billing-mobile-usage-flow">
        <div className="billing-mobile-usage-slot">
          <button type="button" className="billing-mobile-card billing-mobile-usage" onClick={onOpenUsage}>
            <span className="billing-mobile-usage-copy">
              <strong>{t("Usage")}</strong>
              <span aria-label={t("Usage remaining")}><i /></span>
            </span>
            <MobileChevronGlyph />
          </button>
        </div>
      </div>

      <h2 className="billing-mobile-invoices-title">{t("Recent invoices")}</h2>
      <div className="billing-mobile-invoice-slot">
        <section className="billing-mobile-card billing-mobile-invoice" aria-label={t("Recent invoices")}>
          <div className="billing-mobile-invoice-copy"><strong>Oct 19, 2026</strong><span>{t("Credits purchase")}</span></div>
          <div className="billing-mobile-invoice-action"><a href="#invoice" onClick={event => event.preventDefault()}>{t("View")}<MobileArrowGlyph /></a><span>$10.00</span></div>
        </section>
      </div>
    </div>
  );
}
