import type { ReactNode } from "react";
import { AppLink } from "@/components/ui/app-link";
import { TEAM_PAGE_DOCS, TEAM_PAGE_LEADS } from "./team-page-titles";
import type { TeamSettingsSection } from "@/lib/app-routes";

/** The quiet "Docs ↗" link after a page's lead sentence. */
export function TeamDocsLink({ href, label }: { href: string; label: string }) {
  return (
    <a className="team-docs-link" href={href} target="_blank" rel="noopener noreferrer">
      {label}
    </a>
  );
}

/** Lead sentence and optional docs link of a sub-page header. */
export function TeamPageLead({ section, t }: { section: TeamSettingsSection; t: (text: string) => string }): ReactNode {
  const lead = TEAM_PAGE_LEADS[section];
  if (!lead) return null;
  const docs = TEAM_PAGE_DOCS[section];
  return (
    <p>
      {t(lead)}
      {docs && (
        <>
          {" "}
          <TeamDocsLink href={docs} label={t("Docs")} />
        </>
      )}
    </p>
  );
}

/** Glyph of the sub-team inheritance rows and banners (a frame with a downward arrow). */
export function InheritIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M8 2.5H5.2A2.7 2.7 0 0 0 2.5 5.2v5.6a2.7 2.7 0 0 0 2.7 2.7h5.6a2.7 2.7 0 0 0 2.7-2.7V8" />
      <path d="M10.4 2.5v4.7M8.2 5.2l2.2 2.2 2.2-2.2" />
    </svg>
  );
}

/** Card above a page that says how many sub-teams inherit its settings ("1 sub-team is inheriting…"). */
export function InheritBanner({ count, one, many, description, href, t }: { count: number; one: string; many: string; description: string; href: string; t: (text: string) => string }) {
  if (count < 1) return null;
  return (
    <div className="team-inherit-banner" role="note">
      <span className="settings-row-icon">
        <InheritIcon />
      </span>
      <div>
        <strong>
          <AppLink className="team-inherit-chip" href={href}>
            {t(count === 1 ? "1 sub-team" : "{count} sub-teams").replace("{count}", () => String(count))}
          </AppLink>{" "}
          {t(count === 1 ? one : many)}
        </strong>
        <small>{t(description)}</small>
      </div>
    </div>
  );
}
