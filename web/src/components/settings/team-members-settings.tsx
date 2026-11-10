import { useMemo, useState } from "react";
import { usePhoneLayout } from "@/hooks/use-phone-layout";
import { ArrowDown, ArrowUp, MoreHorizontal, Search } from "lucide-react";
import { toast } from "@/lib/toast";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { PropertyMenu } from "@/components/property/property-menu";
import { PhoneNativeSelect } from "@/components/ui/phone-native-select";
import { SettingsSearchGlyph } from "./settings-nav-glyphs";
import { useI18n } from "@/i18n/i18n";
import { setTeamMembership } from "@/lib/api";
import { UserAvatar } from "@/components/ui/user-avatar";
import { personUsername } from "@/lib/people";
import { memberProfilePath, settingsPath } from "@/lib/app-routes";
import { AppLink } from "@/components/ui/app-link";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { canManageTeamSettings } from "@/lib/settings-permissions";
import { ScopedFlowTooltip } from "@/components/ui/tooltip";
import type { BootstrapData, Team, TeamMember, TeamMemberOrigin, WorkspaceMember } from "@/types/flow";
import { SettingsPageTitle, SettingsSelect } from "./settings-primitives";

type MemberFilter = "All" | "Team owners" | "Members" | "Guests";
type MemberSort = "name" | "email" | "role";
const FILTERS: MemberFilter[] = ["All", "Team owners", "Members", "Guests"];

type Row = { member: WorkspaceMember; owner: boolean; origin: TeamMemberOrigin };

/**
 * The note shown next to a member a sync, the API or an invitation added.
 * Manual memberships (and those from before origins existed) show none.
 */
const ORIGIN_NOTES: Partial<Record<TeamMemberOrigin, { label: string; detail: string }>> = {
  scim: {
    label: "Synced from SCIM",
    detail: "Added by the SCIM group mapped to this team. SCIM removes this member when they leave the group, unless they are a team owner.",
  },
  sync: {
    label: "Added by sync",
    detail: "Added by a directory sync through the API. That sync may remove this member again; team owners are kept.",
  },
  api: {
    label: "Added via API",
    detail: "Added by an integration through the API. Syncs that remove only their own members keep this member.",
  },
  invitation: {
    label: "Joined by invitation",
    detail: "Joined by accepting an invitation to this team. Syncs keep this member.",
  },
};

/** The membership's origin; SCIM-managed rows are scim whatever was stored. */
function teamMemberOrigin(membership: TeamMember | undefined): TeamMemberOrigin {
  if (membership?.managed) return "scim";
  return membership?.origin ?? "manual";
}

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
  const phone = usePhoneLayout();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<MemberFilter>("All");
  const [sort, setSort] = useState<MemberSort>("name");
  const [descending, setDescending] = useState(false);
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
        .map((member) => ({
          member,
          owner: memberships.get(member.user.id)?.role === "owner",
          origin: teamMemberOrigin(memberships.get(member.user.id)),
        }))
        .sort((left, right) => left.member.user.displayName.localeCompare(right.member.user.displayName)),
    [data.members, memberships],
  );
  const candidates = data.members.filter(
    (member) => member.status === "active" && !memberships.has(member.user.id),
  );
  const normalized = query.trim().toLowerCase();
  const sortValue = ({ member, owner }: Row) =>
    sort === "email" ? member.user.email : sort === "role" ? roleLabel(member, owner) : member.user.displayName;
  const visible = rows
    .filter(({ member, owner }) => {
      if (
        normalized &&
        !`${member.user.displayName} ${member.user.name} ${member.user.email}`.toLowerCase().includes(normalized)
      )
        return false;
      if (filter === "Team owners") return owner;
      if (filter === "Guests") return member.role === "guest";
      if (filter === "Members") return !owner && member.role !== "guest";
      return true;
    })
    .sort((left, right) => sortValue(left).localeCompare(sortValue(right)) * (descending ? -1 : 1));
  const order = (key: MemberSort) => {
    if (key === sort) setDescending((current) => !current);
    else {
      setSort(key);
      setDescending(false);
    }
  };
  const sortButton = (key: MemberSort, label: string) => (
    <button
      type="button"
      data-sort={key}
      data-sorted={sort === key}
      aria-label={`${t("Order by")} ${t(label)}${sort === key ? `, ${t(descending ? "sorted descending" : "sorted ascending")}` : ""}`}
      onClick={() => order(key)}
    >
      <span className="settings-members-column-label">{t(label)}</span>
      <span className="settings-members-desktop-sort">{sort === key && descending ? <ArrowUp aria-hidden size={12} /> : <ArrowDown aria-hidden size={12} />}</span>
      <svg className={`settings-members-phone-sort${sort === key && descending ? " is-descending" : ""}`} aria-hidden viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
        <path d="M11.5361 10.2745C11.8024 10.0029 11.8249 9.56807 11.5762 9.26961C11.3275 8.97139 10.8961 8.91526 10.5811 9.12801L10.5195 9.17391L8.00001 11.2735L5.48048 9.17391L5.41895 9.12801C5.10388 8.91526 4.67252 8.97139 4.42384 9.26961C4.17512 9.56807 4.19758 10.0029 4.46387 10.2745L4.51954 10.3263L7.51954 12.8263C7.79767 13.058 8.20234 13.058 8.48048 12.8263L11.4805 10.3263L11.5361 10.2745Z" />
        <path d="M8.75 12.25C8.75 12.6642 8.41421 13 8 13C7.58579 13 7.25 12.6642 7.25 12.25L7.25 3.75C7.25 3.33579 7.58579 3 8 3C8.41421 3 8.75 3.33579 8.75 3.75V12.25Z" />
      </svg>
    </button>
  );
  const workspace = data.workspace.urlKey;
  // The sub-teams of this team a member also belongs to.
  const subTeamsOf = (userId: string) =>
    data.teams.filter(
      (item) =>
        !item.retiredAt &&
        data.teamSettings?.[item.id]?.parentTeamId === team.id &&
        data.teamMembers.some((membership) => membership.teamId === item.id && membership.userId === userId),
    );
  // Explain the removal rules wherever a sync is involved.
  const syncedTeam =
    Boolean(scimGroup) || rows.some(({ origin }) => origin === "scim" || origin === "sync" || origin === "api");
  // Owner roles stay with admins and team owners; adding and removing ordinary
  // members follows the team's "Member management" permission.
  const canManage =
    !scimGroup &&
    (data.viewerRole === "admin" ||
      data.viewerRole === "owner" ||
      memberships.get(data.viewer.id)?.role === "owner");
  const canAddMembers = canManage || (!scimGroup && canManageTeamSettings(data, team.id, "members"));
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
      ["Name", "Username", "Email", "Role", "Origin"].map(quote).join(","),
      ...rows.map(({ member, owner, origin }) =>
        [member.user.displayName, personUsername(member.user), member.user.email, roleLabel(member, owner), origin]
          .map(quote)
          .join(","),
      ),
    ].join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const link = document.createElement("a");
    link.href = url;
    link.download = `${team.key}-members.csv`;
    link.click();
    URL.revokeObjectURL(url);
  };
  const filterControls = (
    <>
      <label>
        <Search size={16} className="settings-members-desktop-search" />
        <SettingsSearchGlyph size={16} className="settings-members-phone-search" />
        <input
          aria-label={t("Search by name or email")}
          placeholder={t("Search by name or email")}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
        />
      </label>
      {phone ? (
        <PhoneNativeSelect className="settings-members-phone-filter" aria-label={t("Member filter")} value={filter} onChange={(event) => setFilter(event.target.value as MemberFilter)}>
          {FILTERS.map((item) => <option key={item} value={item}>{t(item)}</option>)}
        </PhoneNativeSelect>
      ) : (
        <SettingsSelect
          label={t("Member filter")}
          value={t(filter)}
          options={FILTERS.map((item) => t(item))}
          onChange={(value) => setFilter(FILTERS.find((item) => t(item) === value) ?? "All")}
        />
      )}
    </>
  );
  return (
    <div className="settings-members-page settings-workspace-members settings-team-members-page" data-i18n-ignore>
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
        {phone ? <div className="settings-members-filters">{filterControls}</div> : filterControls}
        <span />
        {rows.length > 0 && (
          <button className="settings-action" type="button" onClick={exportCsv}>
            {t("Export CSV")}
          </button>
        )}
        {canAddMembers ? (
          <PropertyMenu
            label={t("Add a member")}
            ariaLabel={t("Add team members")}
            triggerRole="button"
            triggerClassName="settings-action primary"
            trigger={<span>{phone ? t("Add") : t("Add a member")}</span>}
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
          <button className="settings-action primary" type="button" aria-label={t("Add team members")} disabled>
            {phone ? t("Add") : t("Add a member")}
          </button>
        )}
      </div>
      {syncedTeam && (
        <div className="settings-team-members-sync" role="note">
          <strong>{t("Who a sync can remove")}</strong>
          <span>
            {t("SCIM removes only members marked “Synced from SCIM” when they leave the identity provider group, and directory syncs remove only members marked “Added by sync”.")}{" "}
            {t("Team owners and members added in Flow by hand or by invitation are always kept; remove them here.")}
          </span>
        </div>
      )}
      <div className="settings-members-directory">
        <div className="settings-members-columns">
          {sortButton("name", "Name")}
          {sortButton("email", "Email")}
          {sortButton("role", "Role")}
          {!phone && <span>{t("Sub-teams")}</span>}
          <span />
        </div>
        {visible.map(({ member, owner, origin }) => {
          const self = member.user.id === data.viewer.id;
          const originNote = ORIGIN_NOTES[origin];
          return (
            <div className="settings-member-directory-row" role="row" key={member.user.id}>
              <span>
                <UserAvatar className="settings-member-avatar" name={member.user.displayName} avatarUrl={member.user.avatarUrl} userId={member.user.id} />
                <i>
                  <strong>{member.user.displayName}</strong>
                  <small>
                    <span data-i18n-ignore>{personUsername(member.user)}</span>
                    {originNote && (
                      <ScopedFlowTooltip label={t(originNote.detail)} side="top">
                        <span className={`settings-member-origin is-${origin}`} tabIndex={0} data-origin={origin}>
                          {t(originNote.label)}
                        </span>
                      </ScopedFlowTooltip>
                    )}
                  </small>
                </i>
              </span>
              <span>
                <span className="settings-member-email">{member.user.email}</span>
              </span>
              <span>
                <em className={`settings-member-role is-${owner ? "owner" : member.role}${owner || member.role === "admin" || member.role === "owner" ? " is-admin" : ""}`}>
                  <span>{t(roleLabel(member, owner))}</span>
                </em>
              </span>
              {!phone && (
                <span className="settings-member-subteams">
                  {subTeamsOf(member.user.id).map((sub) => (
                    <AppLink key={sub.id} className="settings-member-subteam" href={settingsPath(workspace, "team", sub.key, "members")} aria-label={`${t("Open")} ${sub.name}`}>
                      <span><ViewGlyph color={sub.color} icon={sub.icon || "Team"} /></span>
                    </AppLink>
                  ))}
                </span>
              )}
              <span>
                {(canManage || self || (canAddMembers && !owner)) && (
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <button
                        className="settings-member-more"
                        aria-label={`${t("Open menu")} ${member.user.displayName}`}
                        disabled={busy}
                      >
                        <MoreHorizontal size={16} />
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
              <AppLink className="settings-member-row-link" href={memberProfilePath(workspace, personUsername(member.user) || member.user.id)} aria-label={member.user.displayName} tabIndex={-1} />
            </div>
          );
        })}
        {!visible.length && (
          <div className="settings-team-members-empty">
            {t(rows.length ? "No members match your filters" : "This team has no members yet")}
          </div>
        )}
      </div>
      {rows.length > 0 && (
        <button type="button" className="settings-export-mobile" onClick={exportCsv}>
          {t("Export CSV")}
        </button>
      )}
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

