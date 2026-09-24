import { useMemo, useState } from "react";
import { MoreHorizontal, Search } from "lucide-react";
import { toast } from "sonner";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { PropertyMenu } from "@/components/property/property-menu";
import { useI18n } from "@/i18n/i18n";
import { setTeamMembership } from "@/lib/api";
import type { BootstrapData, Team, WorkspaceMember } from "@/types/flow";
import { SettingsPageTitle, SettingsSelect } from "./settings-primitives";

type MemberFilter = "All" | "Team owners" | "Members" | "Guests";
const FILTERS: MemberFilter[] = ["All", "Team owners", "Members", "Guests"];

type Row = { member: WorkspaceMember; owner: boolean };

/** Team members as a table: search, role filter, CSV export, add and remove. */
export function TeamMembersSettings({
  data,
  team,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<MemberFilter>("All");
  const [busy, setBusy] = useState(false);
  const scimGroup = data.workspaceSettings.scimTeamGroupMapping?.[team.id];
  const memberships = useMemo(
    () =>
      new Map(
        data.teamMembers
          .filter((item) => item.teamId === team.id)
          .map((item) => [item.userId, item]),
      ),
    [data.teamMembers, team.id],
  );
  const rows = useMemo<Row[]>(
    () =>
      data.members
        .filter((member) => member.status === "active" && memberships.has(member.user.id))
        .map((member) => ({ member, owner: memberships.get(member.user.id)?.role === "owner" }))
        .sort((left, right) => left.member.user.displayName.localeCompare(right.member.user.displayName)),
    [data.members, memberships],
  );
  const candidates = data.members.filter(
    (member) => member.status === "active" && !memberships.has(member.user.id),
  );
  const normalized = query.trim().toLowerCase();
  const visible = rows.filter(({ member, owner }) => {
    if (
      normalized &&
      !`${member.user.displayName} ${member.user.name} ${member.user.email}`.toLowerCase().includes(normalized)
    )
      return false;
    if (filter === "Team owners") return owner;
    if (filter === "Guests") return member.role === "guest";
    if (filter === "Members") return !owner && member.role !== "guest";
    return true;
  });
  const canManage =
    !scimGroup &&
    (data.viewerRole === "admin" ||
      data.viewerRole === "owner" ||
      memberships.get(data.viewer.id)?.role === "owner");
  const change = async (userId: string, member: boolean, role: "owner" | "member" = "member", success = "Team members updated") => {
    setBusy(true);
    try {
      await setTeamMembership(data.workspace.urlKey, team.id, userId, member, role);
      await onReload();
      toast.success(t(success));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update team members"));
    } finally {
      setBusy(false);
    }
  };
  const exportCsv = () => {
    const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
    const csv = [
      ["Name", "Username", "Email", "Role"].map(quote).join(","),
      ...rows.map(({ member, owner }) =>
        [member.user.displayName, member.user.name, member.user.email, roleLabel(member, owner)].map(quote).join(","),
      ),
    ].join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `${team.key}-members.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };
  return (
    <div className="settings-members-page settings-team-members-page" data-i18n-ignore>
      <SettingsPageTitle>{t("Team members")}</SettingsPageTitle>
      {scimGroup && (
        <div className="settings-team-members-scim" role="note">
          <strong>{t("SCIM enabled")}</strong>
          <span>
            {t("This team is managed by SCIM integration and linked to SCIM Group")} “{scimGroup}”.{" "}
            {t("Add or remove members on your identity provider.")}
          </span>
        </div>
      )}
      <div className="settings-members-toolbar">
        <label>
          <Search size={14} />
          <input
            aria-label={t("Search by name or email")}
            placeholder={t("Search by name or email")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <SettingsSelect
          label={t("Member filter")}
          value={t(filter)}
          options={FILTERS.map((item) => t(item))}
          onChange={(value) => setFilter(FILTERS.find((item) => t(item) === value) ?? "All")}
        />
        <span />
        {rows.length > 0 && (
          <button className="settings-action" type="button" onClick={exportCsv}>
            {t("Export CSV")}
          </button>
        )}
        {canManage ? (
          <PropertyMenu
            label={t("Add a member")}
            ariaLabel={t("Add a member")}
            triggerRole="button"
            triggerClassName="settings-action primary"
            trigger={<span>{t("Add a member")}</span>}
            align="end"
            searchPlaceholder={t("Search by name or email")}
            emptyLabel={t("Everyone in the workspace is already on this team")}
            options={candidates.map((member) => ({
              id: member.user.id,
              label: member.user.displayName,
              description: member.user.email,
            }))}
            onChange={(userId) => change(userId, true, "member", "Member added")}
          />
        ) : (
          <button className="settings-action primary" type="button" disabled>
            {t("Add a member")}
          </button>
        )}
      </div>
      <div className="settings-members-directory">
        <div className="settings-members-columns">
          <span>{t("Name")}</span>
          <span>{t("Email")}</span>
          <span>{t("Role")}</span>
          <span />
        </div>
        {visible.map(({ member, owner }) => {
          const self = member.user.id === data.viewer.id;
          return (
            <div className="settings-member-directory-row" key={member.user.id}>
              <span>
                <b className="settings-member-avatar">{initials(member.user.displayName)}</b>
                <i>
                  <strong>{member.user.displayName}</strong>
                  <small>{member.user.name}</small>
                </i>
              </span>
              <span>{member.user.email}</span>
              <span>
                <em className={`settings-member-role is-${owner ? "owner" : member.role}`}>
                  {t(roleLabel(member, owner))}
                </em>
              </span>
              <span>
                {(canManage || self) && (
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <button
                        className="settings-member-more"
                        aria-label={`${t("Open menu")} ${member.user.displayName}`}
                        disabled={busy}
                      >
                        <MoreHorizontal size={15} />
                      </button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end" className="settings-member-action-menu">
                      {canManage && member.role !== "guest" && (
                        <DropdownMenuItem
                          onSelect={() =>
                            void change(member.user.id, true, owner ? "member" : "owner", owner ? "Team owner removed" : "Team owner added")
                          }
                        >
                          {t(owner ? "Remove team owner" : "Make team owner")}
                        </DropdownMenuItem>
                      )}
                      <DropdownMenuItem
                        className="danger-item"
                        onSelect={() =>
                          void change(member.user.id, false, "member", self ? "You left the team" : "Removed from team")
                        }
                      >
                        {t(self ? "Leave team" : "Remove from team")}
                      </DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenu>
                )}
              </span>
            </div>
          );
        })}
        {!visible.length && (
          <div className="settings-team-members-empty">
            {t(rows.length ? "No members match your filters" : "This team has no members yet")}
          </div>
        )}
      </div>
    </div>
  );
}

function roleLabel(member: WorkspaceMember, owner: boolean) {
  if (owner) return "Team owner";
  if (member.role === "owner") return "Workspace owner";
  if (member.role === "admin") return "Workspace admin";
  if (member.role === "guest") return "Guest";
  return "Member";
}

function initials(name: string) {
  return (
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase())
      .join("") || "?"
  );
}
