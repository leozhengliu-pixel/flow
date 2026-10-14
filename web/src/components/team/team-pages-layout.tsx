import { useEffect, useMemo, type MouseEvent, type ReactNode } from "react";

import { SetActiveTeam } from "@/lib/active-team";
import { settingsPath } from "@/lib/app-routes";
import { preloadRoute } from "@/lib/route-preload";
import { isWorkspaceAdmin, teamContentAccessible } from "@/lib/team-access";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Team } from "@/types/flow";

import "./team-pages-layout.css";

/** A computer showing a closed screen: the private-team and not-found illustration. */
function ClosedScreenIllustration() {
  const vents = Array.from({ length: 23 }, (_, index) => 21 + index * 4);
  const dots: Array<[number, number]> = [
    [34, 31], [40, 31], [46, 31], [52, 31], [58, 31], [64, 31], [70, 31],
    [34, 42], [40, 42], [46, 42], [52, 42], [58, 42],
    [34, 53], [40, 53], [46, 53],
  ];
  return (
    <svg className="team-pages-private-illustration" viewBox="0 0 130 155" width="92" height="110" fill="none" aria-hidden="true">
      <g className="team-pages-private-illustration-case" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round">
        <rect x="8" y="4" width="114" height="124" rx="8" />
        <path d="M14 128v18a3 3 0 0 0 3 3h96a3 3 0 0 0 3-3v-18" />
        {vents.map((x) => <path key={x} d={`M${x} 138v6`} strokeWidth="1" />)}
        <path d="M22 110h6M86 104h20" strokeWidth="2" />
      </g>
      <g className="team-pages-private-illustration-screen">
        <rect x="21.75" y="17.75" width="86.5" height="70.5" rx="5" stroke="currentColor" strokeWidth="1.5" />
        <rect x="27.5" y="23.5" width="75" height="59" rx="3" stroke="currentColor" />
        {dots.map(([x, y]) => <rect key={`${x}-${y}`} x={x} y={y} width="2" height="4" rx="1" fill="currentColor" />)}
        <path d="M83 62l9 9M92 62l-9 9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
      </g>
    </svg>
  );
}

/**
 * The reference app's private-team page for a viewer outside the team: workspace admins
 * see the team and manage its settings, but read its content only as members,
 * so the admin line explains what the role does grant.
 */
function PrivateTeamEmpty({
  data,
  team,
  onNavigate,
}: {
  data: BootstrapData;
  team: Team;
  onNavigate: (path: string) => void;
}) {
  const { t, locale } = useI18n();
  const settingsHref = settingsPath(data.workspace.urlKey, "team", team.key);
  const openSettings = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return;
    event.preventDefault();
    onNavigate(settingsHref);
  };
  return (
    <main className="main-panel team-pages-private" data-testid="private-team-empty">
      <ClosedScreenIllustration />
      <div className="team-pages-private-copy">
        <h1>{t("Private team")}</h1>
        <p>
          {t("This is a private team and you are not a team member.")}
          {isWorkspaceAdmin(data) && (
            <>
              <br />
              {t("As an admin you have access to the")}
              {locale === "zh-CN" ? "" : " "}
              <a className="team-pages-private-link" href={settingsHref} onClick={openSettings}>
                {t("team’s settings")}
              </a>
              {locale === "zh-CN" ? "。" : "."}
            </>
          )}
        </p>
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

  // Same rule as the server (store.TeamContentVisible): a workspace admin who
  // has not joined a private team sees this page, not its content.
  const blocked = Boolean(team && !teamContentAccessible(data, team));

  const teamId = team?.id;
  useEffect(() => {
    if (teamId === undefined || blocked) return;
    // Progressive preload OK — warm common team surfaces after paint.
    const path = typeof window !== "undefined" ? window.location.pathname : "";
    const handle = globalThis.setTimeout(() => {
      void preloadRoute(path).catch(() => undefined);
    }, 120);
    return () => globalThis.clearTimeout(handle);
  }, [teamId, blocked]);

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
