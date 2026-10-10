import type { ReactNode } from "react";
import { TeamIcon } from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { loopPath, loopsPath, teamLoopsPath } from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type { BootstrapData, Loop } from "@/types/flow";
import { LoopIcon } from "./loop-glyph";
import { loopIconColor } from "./loop-template-visuals";
import { loopTeam } from "./loop-model";

/**
 * Linear's loop breadcrumb: `[team] Team › Loops › [icon] Loop › Edit ⋯`. Ancestors are primary 13/500,
 * separators and the trailing page name secondary; the loop name links back unless it is the current page.
 */
export function LoopBreadcrumb({
  data,
  loop,
  loopId,
  current,
  actions,
  onNavigate,
}: {
  data: BootstrapData;
  loop?: Loop;
  loopId: string;
  /** Trailing page name ("Edit", "Run history"); without it the loop itself is the current page. */
  current?: string;
  /** Rendered right after the last crumb (the ⋯ menu). */
  actions?: ReactNode;
  onNavigate: (path: string) => void;
}) {
  const { t } = useI18n();
  const workspace = data.workspace.urlKey;
  const team = loop ? loopTeam(data, loop) : undefined;
  const link = (path: string) => ({
    href: path,
    onClick: (event: { preventDefault: () => void }) => {
      event.preventDefault();
      onNavigate(path);
    },
  });
  const name = loop?.name || t(loop ? "Untitled loop" : "Loop");
  const icon = loop && (
    <span className="loops-breadcrumb-icon" style={{ color: loopIconColor(loop) }}>
      <LoopIcon source={loop} size={16} />
    </span>
  );
  return (
    <nav className="loops-breadcrumb" aria-label={t("Breadcrumb")}>
      {team ? (
        <a {...link(teamLoopsPath(workspace, team.key))}>
          <TeamIcon team={team} size={16} />
          <span data-i18n-ignore>{team.name}</span>
        </a>
      ) : (
        <span className="loops-breadcrumb-scope">
          <ViewGlyph color="currentColor" icon="Team" />
          {t("Workspace")}
        </span>
      )}
      <span aria-hidden="true">›</span>
      <a {...link(loopsPath(workspace))}>{t("Loops")}</a>
      <span aria-hidden="true">›</span>
      {current ? (
        <>
          <a {...link(loopPath(workspace, loopId))} data-i18n-ignore={loop?.name ? true : undefined}>
            {icon}
            {name}
          </a>
          <span aria-hidden="true">›</span>
          <h2 className="loops-breadcrumb-current">{current}</h2>
        </>
      ) : (
        <h2 data-i18n-ignore={loop?.name ? true : undefined}>
          {icon}
          {name}
        </h2>
      )}
      {actions}
    </nav>
  );
}
