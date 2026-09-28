import { useEffect, useMemo, type ReactNode } from "react";
import { Lock } from "lucide-react";

import { SetActiveTeam } from "@/lib/active-team";
import { settingsPath } from "@/lib/app-routes";
import { preloadRoute } from "@/lib/route-preload";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Team } from "@/types/flow";

import "./team-pages-layout.css";

const TEAM_PAGE_KINDS = new Set([
  "team-overview",
  "team-documents",
  "team-loops",
  "team-members",
  "team-board",
  "team-triage",
  "team-updates",
  "team-update",
  "team-resources",
  "team-links",
  "team-archive",
  "team-cycles",
  "cycle",
  "cycle-upcoming",
  "team-initiatives",
  "team-issues",
  "team-saved-view",
  "team-views",
  "team-views-new",
  "team-projects",
  "team-projects-new-view",
  "team-projects-saved-view",
]);

export function isTeamPagesRoute(kind: string) {
  return TEAM_PAGE_KINDS.has(kind);
}

function isPrivateTeam(data: BootstrapData, team: Team) {
  if (team.private) return true;
  const access = data.teamSettings?.[team.id]?.access;
  return access === "private";
}

function viewerCanAccessTeam(data: BootstrapData, team: Team) {
  if (!isPrivateTeam(data, team)) return true;
  if (data.viewerRole === "admin" || data.viewerRole === "owner") {
    // Workspace admins can resolve the team shell, but Linear still gates the
    // private empty until they are members — match that for non-members.
  }
  return data.teamMembers.some(
    (member) => member.teamId === team.id && member.userId === data.viewer.id,
  );
}

function PrivateTeamEmpty({
  data,
  team,
  onNavigate,
}: {
  data: BootstrapData;
  team: Team;
  onNavigate: (path: string) => void;
}) {
  const { t } = useI18n();
  const canManage =
    data.viewerRole === "admin" || data.viewerRole === "owner";
  const roleLabel =
    data.viewerRole === "owner" ? t("owner") : t("admin");
  return (
    <main className="main-panel team-pages-private" data-testid="private-team-empty">
      <div className="team-pages-private-card">
        <span className="team-pages-private-icon" aria-hidden>
          <Lock size={22} />
        </span>
        <h1>{t("Private team")}</h1>
        <p>
          {t("This is a private team and you are not a team member.")}
        </p>
        {canManage && (
          <p className="team-pages-private-admin">
            {t("As a workspace")} {roleLabel},{" "}
            <button
              type="button"
              className="team-pages-private-link"
              onClick={() =>
                onNavigate(
                  settingsPath(data.workspace.urlKey, "team", team.key, "members"),
                )
              }
            >
              {t("open team settings")}
            </button>{" "}
            {t("to manage membership.")}
          </p>
        )}
      </div>
    </main>
  );
}

/**
 * LS-0594 TeamPagesLayout — wrapper gate + SetActiveTeam mount + progressive preload.
 */
export function TeamPagesLayout({
  data,
  teamKey,
  onNavigate,
  children,
}: {
  data: BootstrapData;
  teamKey?: string;
  onNavigate: (path: string) => void;
  children: ReactNode;
}) {
  const team = useMemo(() => {
    if (!teamKey) return undefined;
    return data.teams.find(
      (item) => item.key.toLowerCase() === teamKey.toLowerCase(),
    );
  }, [data.teams, teamKey]);

  const blocked = Boolean(
    team && isPrivateTeam(data, team) && !viewerCanAccessTeam(data, team),
  );

  useEffect(() => {
    if (!team || blocked) return;
    // Progressive preload OK — warm common team surfaces after paint.
    const path = typeof window !== "undefined" ? window.location.pathname : "";
    const handle = globalThis.setTimeout(() => {
      void preloadRoute(path).catch(() => undefined);
    }, 120);
    return () => globalThis.clearTimeout(handle);
  }, [team?.id, blocked]);

  return (
    <>
      <SetActiveTeam team={blocked ? undefined : team} />
      {blocked && team ? (
        <PrivateTeamEmpty data={data} team={team} onNavigate={onNavigate} />
      ) : (
        children
      )}
    </>
  );
}
