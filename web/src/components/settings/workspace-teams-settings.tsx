import { useCallback, useEffect, useMemo, useState } from "react";
import { Archive, ArchiveRestore, MoreHorizontal, RotateCcw, Search, Settings, SquareStack, Trash2 } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { toast } from "sonner";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import { deleteTeam, listDeletedTeams, restoreDeletedTeam, updateTeam } from "@/lib/api";
import { teamIssuesPath } from "@/lib/app-routes";
import { DELETE_TEAM_DESCRIPTION } from "@/lib/team-deletion";
import type { BootstrapData, DeletedTeam, Team } from "@/types/flow";
import { SettingsPageTitle, SettingsSelect } from "./settings-primitives";

type Visibility = "Active" | "Retired" | "Recently deleted";


/** Workspace teams table: filter, visibility, members, issues, created. */
export function WorkspaceTeamsSettings({
  data,
  onCreate,
  onOpen,
  onReload,
}: {
  data: BootstrapData;
  onCreate: () => void;
  onOpen: (team: Team) => void;
  onReload: () => Promise<void>;
}) {
  const { t, formatDate } = useI18n();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [visibility, setVisibility] = useState<Visibility>("Active");
  const admin = data.viewerRole === "admin" || data.viewerRole === "owner";
  const visibilities: Visibility[] = admin ? ["Active", "Retired", "Recently deleted"] : ["Active", "Retired"];
  const [deleted, setDeleted] = useState<DeletedTeam[]>();
  const loadDeleted = useCallback(async () => {
    try {
      setDeleted(await listDeletedTeams(data.workspace.urlKey));
    } catch (error) {
      setDeleted([]);
      toast.error(error instanceof Error ? error.message : t("Could not load deleted teams"));
    }
  }, [data.workspace.urlKey, t]);
  useEffect(() => {
    if (visibility === "Recently deleted") void loadDeleted();
  }, [visibility, loadDeleted, data.teams]);
  const memberCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const item of data.teamMembers) counts.set(item.teamId, (counts.get(item.teamId) ?? 0) + 1);
    return counts;
  }, [data.teamMembers]);
  const issueCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const issue of data.issues) counts.set(issue.team.id, (counts.get(issue.team.id) ?? 0) + 1);
    return counts;
  }, [data.issues]);
  const normalized = query.trim().toLowerCase();
  const teams = data.teams
    .filter((team) => (visibility === "Retired" ? Boolean(team.retiredAt) : !team.retiredAt))
    .filter((team) => !normalized || `${team.name} ${team.key}`.toLowerCase().includes(normalized))
    .sort((left, right) => left.name.localeCompare(right.name));
  const visibilityLabel = (team: Team) => {
    const access = data.teamSettings?.[team.id]?.access;
    if (team.private || access === "private") return "Private";
    if (access === "restricted") return "Restricted";
    return "Workspace";
  };
  const run = async (action: () => Promise<unknown>, success: string) => {
    try {
      await action();
      await onReload();
      toast.success(t(success));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update team"));
    }
  };
  const exportCsv = () => {
    const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
    const csv = [
      ["Name", "Identifier", "Visibility", "Members", "Issues", "Created", "Retired"].map(quote).join(","),
      ...data.teams.map((team) =>
        [
          team.name,
          team.key,
          visibilityLabel(team),
          String(memberCounts.get(team.id) ?? 0),
          String(issueCounts.get(team.id) ?? 0),
          team.createdAt ?? "",
          team.retiredAt ?? "",
        ]
          .map(quote)
          .join(","),
      ),
    ].join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = "teams.csv";
    link.click();
    URL.revokeObjectURL(url);
  };
  const count = (value: number) => (value ? value.toLocaleString() : "-");
  return (
    <div className="settings-members-page settings-teams-page" data-i18n-ignore>
      <SettingsPageTitle>{t("Teams")}</SettingsPageTitle>
      <div className="settings-members-toolbar">
        <label>
          <Search size={14} />
          <input
            aria-label={t("Filter by name…")}
            placeholder={t("Filter by name…")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <SettingsSelect
          label={t("Team visibility filter")}
          value={t(visibility)}
          options={visibilities.map((item) => t(item))}
          onChange={(value) => setVisibility(visibilities.find((item) => t(item) === value) ?? "Active")}
        />
        <span />
        {admin && visibility !== "Recently deleted" && data.teams.length > 0 && (
          <button className="settings-action" type="button" onClick={exportCsv}>
            {t("Export CSV")}
          </button>
        )}
        <button className="settings-action primary" type="button" onClick={onCreate}>
          {t("Create team")}
        </button>
      </div>
      {visibility === "Recently deleted" ? (
        <DeletedTeamsTable
          teams={(deleted ?? []).filter((team) => !normalized || `${team.name} ${team.key}`.toLowerCase().includes(normalized))}
          loading={deleted === undefined}
          filtered={Boolean(normalized)}
          onRestore={(team) =>
            void run(async () => {
              await restoreDeletedTeam(data.workspace.urlKey, team.id);
              setDeleted((items) => items?.filter((item) => item.id !== team.id));
            }, "Team restored")
          }
        />
      ) : (
      <div className="settings-members-directory">
        <div className="settings-members-columns">
          <span>{t("Name")}</span>
          <span>{t("Description")}</span>
          <span>{t(visibility === "Retired" ? "Retired" : "Visibility")}</span>
          <span>{t("Members")}</span>
          <span>{t("Issues")}</span>
          <span>{t("Created")}</span>
          <span />
        </div>
        {teams.length > 0 && (
          <div className="settings-members-group">
            <strong>{t(visibility)}</strong>
            <span>{teams.length}</span>
          </div>
        )}
        {teams.map((team) => (
          <div
            className="settings-member-directory-row settings-teams-row"
            key={team.id}
            role="link"
            tabIndex={0}
            onClick={() => onOpen(team)}
            onKeyDown={(event) => {
              if (event.key === "Enter") onOpen(team);
            }}
          >
            <span>
              <span className="settings-team-icon" style={{ color: team.color }}>
                <ViewGlyph color={team.color} icon={team.icon || "Team"} />
              </span>
              <strong>{team.name}</strong>
              <small>{team.key}</small>
            </span>
            <span className="settings-teams-description">{data.teamSettings?.[team.id]?.description ?? ""}</span>
            <span>{t(visibility === "Retired" && team.retiredAt ? formatDate(team.retiredAt, { dateStyle: "medium" }) : visibilityLabel(team))}</span>
            <span>{count(memberCounts.get(team.id) ?? 0)}</span>
            <span>{count(issueCounts.get(team.id) ?? 0)}</span>
            <span>{team.createdAt ? formatDate(team.createdAt, { month: "short", day: "numeric" }) : "-"}</span>
            <span onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button className="settings-member-more" aria-label={`${t("Open menu")} ${team.name}`}>
                    <MoreHorizontal size={15} />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="settings-member-action-menu">
                  <DropdownMenuItem onSelect={() => onOpen(team)}>
                    <Settings size={16} />
                    {t("Team settings")}
                  </DropdownMenuItem>
                  <DropdownMenuItem onSelect={() => navigate(teamIssuesPath(data.workspace.urlKey, team.key))}>
                    <SquareStack size={16} />
                    {t("Go to all issues")}
                  </DropdownMenuItem>
                  {admin && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        onSelect={() =>
                          void (async () => {
                            const retiring = !team.retiredAt;
                            if (
                              retiring &&
                              !(await confirmAction(`${t("Retire")} ${team.name}?`, {
                                confirmLabel: t("Retire team"),
                                description: t("Retired teams are read-only and hidden from the sidebar."),
                              }))
                            )
                              return;
                            await run(
                              () => updateTeam(data.workspace.urlKey, team.id, { retired: retiring, subTeamAction: "retire" }),
                              retiring ? "Team retired" : "Team restored",
                            );
                          })()
                        }
                      >
                        {team.retiredAt ? <ArchiveRestore size={16} /> : <Archive size={16} />}
                        {t(team.retiredAt ? "Restore team" : "Retire team…")}
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        className="danger-item"
                        onSelect={() =>
                          void (async () => {
                            if (
                              !(await confirmAction(`${t("Delete")} ${team.name}?`, {
                                confirmLabel: t("Delete team"),
                                description: t(DELETE_TEAM_DESCRIPTION),
                              }))
                            )
                              return;
                            await run(() => deleteTeam(data.workspace.urlKey, team.id), "Team deleted");
                          })()
                        }
                      >
                        <Trash2 size={16} />
                        {t("Delete team…")}
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </span>
          </div>
        ))}
        {!teams.length && (
          <div className="settings-team-members-empty">
            {normalized ? t("No matching teams") : t(visibility === "Retired" ? "No retired teams" : "No active teams")}
          </div>
        )}
      </div>
      )}
    </div>
  );
}

function DeletedTeamsTable({
  teams,
  loading,
  filtered,
  onRestore,
}: {
  teams: DeletedTeam[];
  loading: boolean;
  filtered: boolean;
  onRestore: (team: DeletedTeam) => void;
}) {
  const { t, formatDate } = useI18n();
  const date = (value: string) => formatDate(value, { dateStyle: "medium" });
  return (
    <div className="settings-members-directory">
      <div className="settings-members-columns">
        <span>{t("Name")}</span>
        <span>{t("Deleted by")}</span>
        <span>{t("Deleted")}</span>
        <span />
        <span>{t("Issues")}</span>
        <span>{t("Deletes on")}</span>
        <span />
      </div>
      {teams.length > 0 && (
        <div className="settings-members-group">
          <strong>{t("Recently deleted")}</strong>
          <span>{teams.length}</span>
        </div>
      )}
      {teams.map((team) => (
        <div className="settings-member-directory-row settings-teams-row is-deleted" key={team.id}>
          <span>
            <span className="settings-team-icon" style={{ color: team.color }}>
              <ViewGlyph color={team.color} icon={team.icon || "Team"} />
            </span>
            <strong>{team.name}</strong>
            <small>{team.key}</small>
          </span>
          <span className="settings-teams-description">{team.archivedBy?.displayName ?? "-"}</span>
          <span>{date(team.archivedAt)}</span>
          <span />
          <span>{team.issueCount ? team.issueCount.toLocaleString() : "-"}</span>
          <span>{date(team.purgeAt)}</span>
          <span>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button className="settings-member-more" aria-label={`${t("Open menu")} ${team.name}`}>
                  <MoreHorizontal size={15} />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="settings-member-action-menu">
                <DropdownMenuItem onSelect={() => onRestore(team)}>
                  <RotateCcw size={16} />
                  {t("Restore team")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        </div>
      ))}
      {!loading && !teams.length && (
        <div className="settings-team-members-empty">
          {filtered ? t("No matching teams") : t("Deleted teams appear here for 30 days before being permanently removed.")}
        </div>
      )}
    </div>
  );
}
