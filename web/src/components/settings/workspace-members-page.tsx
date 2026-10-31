import { Fragment, useEffect, useMemo, useState, type ReactNode } from "react";
import { useCurrentTimeMs } from "@/hooks/use-current-time";
import { useDesktopSettings } from '@/hooks/use-desktop-settings';
import { useNavigate } from "react-router-dom";
import * as DropdownMenuPrimitive from "@radix-ui/react-dropdown-menu";
import { ArrowDown, ArrowUp, Check, ChevronDown, Command, MoreHorizontal, Search, UserRound, X } from "lucide-react";
import { useI18n } from "@/i18n/i18n";
import { toast } from "@/lib/toast";
import { personUsername } from "@/lib/people";
import { installedApplicationPath, memberProfilePath, settingsPath } from "@/lib/app-routes";
import { uninstallApplication } from "@/lib/application-agents";
import {
  removeMember,
  resendInvitation,
  resumeMember,
  revokeInvitation,
  setTeamMembership,
  suspendMember,
  updateMemberIdentity,
  updateMemberRole,
} from "@/lib/api";
import { AppLink } from "@/components/ui/app-link";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { UserAvatar } from "@/components/ui/user-avatar";
import { PhoneNativeSelect } from "@/components/ui/phone-native-select";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { InviteMembersDialog } from "@/components/workspace-directory/invite-members-dialog";
import type { BootstrapData, Invitation, WorkspaceMember } from "@/types/flow";
import { SettingsPageTitle as PageTitle } from "./settings-primitives";
import { SettingsSearchGlyph } from "./settings-nav-glyphs";
import "./workspace-members-scim.css";
import {
  MEMBER_GROUPS,
  availableMemberFilters,
  emptyMembersMessage,
  invitationStatusLabel,
  isAdminRole,
  isApplicationMember,
  memberEntryGroup,
  memberMatchesFilter,
  memberMatchesQuery,
  memberSearchQuery,
  memberSelectable,
  memberStatusLabel,
  sortMemberEntries,
  type MemberEntry,
  type MemberFilter,
  type MemberGroup,
  type MemberSort,
} from "./workspace-members";

const SCIM_MANAGED_HINT = "Managed by SCIM. Change their role, name, email or status in your identity provider.";
const isSCIMManaged = (member: WorkspaceMember) => member.managedBy === "scim";

type AssignableRole = "admin" | "member" | "guest";
type ConfirmKind = "suspend" | "activate" | "remove" | "revoke";
const ASSIGNABLE_ROLES: AssignableRole[] = ["admin", "member", "guest"];
const ONLINE_WINDOW_MS = 5 * 60 * 1000;
const FILTER_STORAGE_KEY = "flow:settings-members-filter";

function MembersSortGlyph({ descending }: { descending: boolean }) {
  return (
    <svg className={`settings-members-phone-sort${descending ? " is-descending" : ""}`} aria-hidden viewBox="0 0 16 16" width="12" height="12" fill="currentColor">
      <path d="M11.5361 10.2745C11.8024 10.0029 11.8249 9.56807 11.5762 9.26961C11.3275 8.97139 10.8961 8.91526 10.5811 9.12801L10.5195 9.17391L8.00001 11.2735L5.48048 9.17391L5.41895 9.12801C5.10388 8.91526 4.67252 8.97139 4.42384 9.26961C4.17512 9.56807 4.19758 10.0029 4.46387 10.2745L4.51954 10.3263L7.51954 12.8263C7.79767 13.058 8.20234 13.058 8.48048 12.8263L11.4805 10.3263L11.5361 10.2745Z" />
      <path d="M8.75 12.25C8.75 12.6642 8.41421 13 8 13C7.58579 13 7.25 12.6642 7.25 12.25L7.25 3.75C7.25 3.33579 7.58579 3 8 3C8.41421 3 8.75 3.33579 8.75 3.75V12.25Z" />
    </svg>
  );
}

function title(value: string) {
  return value ? value[0].toUpperCase() + value.slice(1) : value;
}

function readStoredFilter(): MemberFilter {
  try {
    return (localStorage.getItem(FILTER_STORAGE_KEY) as MemberFilter | null) ?? "All";
  } catch {
    return "All";
  }
}

function storeFilter(value: MemberFilter) {
  try {
    localStorage.setItem(FILTER_STORAGE_KEY, value);
  } catch {
    // The filter is a convenience; it works without storage.
  }
}

function download(filename: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(url);
}

function formatDate(value: string, options: Intl.DateTimeFormatOptions) {
  return new Intl.DateTimeFormat(document.documentElement.lang || "en-US", options).format(new Date(value));
}
const shortDate = (value: string) => formatDate(value, { month: "short", day: "numeric" });
const fullDateTime = (value: string) => formatDate(value, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit" });
const fullDate = (value: string) => formatDate(value, { month: "short", day: "numeric", year: "numeric" });

/** The installation id of an app user ("app_<hash>" is installed as "installation_<hash>"). */
function installationIdOf(userId: string) {
  return `installation_${userId.replace(/^app_/, "")}`;
}

/**
 * Settings › Members (workspace), as the reference app: Active, Invited, Application and Inactive groups (Inactive — suspended
 * users and former members, "Left workspace" — for admins), row menus that depend on the row's state, selection of
 * active or of suspended people with bulk actions, and the reference app's empty states.
 */
export function WorkspaceMembersPage({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const desktop = useDesktopSettings();
  const { t } = useI18n();
  const navigate = useNavigate();
  const workspace = data.workspace.urlKey;
  const admin = isAdminRole(data.viewerRole);
  const viewerOwner = data.viewerRole === "owner";
  const [query, setQuery] = useState("");
  const [filter, setFilterState] = useState<MemberFilter>(readStoredFilter);
  const [sort, setSort] = useState<MemberSort>("name");
  const [descending, setDescending] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [identityTarget, setIdentityTarget] = useState<{ member: WorkspaceMember; field: "displayName" | "username" | "email"; value: string }>();
  const [roleTarget, setRoleTarget] = useState<{ members: WorkspaceMember[]; role: AssignableRole }>();
  const [teamsTarget, setTeamsTarget] = useState<{ mode: "add" | "manage"; members: WorkspaceMember[] }>();
  const [teamDraft, setTeamDraft] = useState<string[]>([]);
  const [confirmTarget, setConfirmTarget] = useState<{ members: WorkspaceMember[]; action: ConfirmKind }>();
  const [selectedIds, setSelectedIds] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const now = useCurrentTimeMs();

  // Inactive users (suspended and departed) are listed only for admins, as the reference app lists them only to members who can manage them.
  const allMembers = useMemo(
    () => [...data.members.filter((member) => admin || member.status === "active"), ...(admin ? (data.departedMembers ?? []) : [])],
    [admin, data.departedMembers, data.members],
  );
  const filters = availableMemberFilters(admin, data.members.some(isApplicationMember));
  const activeFilter: MemberFilter = filters.includes(filter) ? filter : "All";
  const setFilter = (value: MemberFilter) => {
    setFilterState(value);
    storeFilter(value);
  };
  const search = memberSearchQuery(query);
  const filtered = Boolean(search) || activeFilter !== "All";
  const isMemberOfTeam = (userId: string, teamId: string) => data.teamMembers.some((item) => item.userId === userId && item.teamId === teamId);
  const isOnline = (member: WorkspaceMember) =>
    member.status === "active" &&
    !isApplicationMember(member) &&
    (member.user.id === data.viewer.id || (member.lastSeenAt ? now - new Date(member.lastSeenAt).getTime() < ONLINE_WINDOW_MS : false));

  const entries: MemberEntry[] = [
    ...allMembers
      .filter((member) => memberMatchesFilter(member, activeFilter) && memberMatchesQuery(member, search))
      .map((member) => ({ kind: "member" as const, key: `member:${member.user.id}`, member })),
    ...(admin && (activeFilter === "All" || activeFilter === "Pending invites")
      ? data.invitations
          .filter((invitation) => invitation.status === "pending" && (!search || invitation.email.toLowerCase().includes(search)))
          .map((invitation) => ({ kind: "invitation" as const, key: `invitation:${invitation.id}`, invitation }))
      : []),
  ];
  const sorted = sortMemberEntries(entries, { sort, descending, isOnline, now });
  const groups = MEMBER_GROUPS.map((group) => [group, sorted.filter((entry) => memberEntryGroup(entry) === group)] as const).filter(
    ([, items]) => items.length > 0,
  );

  const selectedMembers = allMembers.filter((member) => selectedIds.includes(member.user.id));
  const selecting = selectedMembers.length > 0;
  const selectionActive = selecting ? selectedMembers[0].status === "active" : undefined;
  useEffect(() => {
    if (!selecting) return;
    const clearOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      // Menus and dialogs close first; the selection clears on the next Escape.
      if (document.querySelector('[role="menu"], [role="dialog"]')) return;
      event.stopPropagation();
      setSelectedIds([]);
    };
    window.addEventListener("keydown", clearOnEscape, true);
    return () => window.removeEventListener("keydown", clearOnEscape, true);
  }, [selecting]);
  const toggleSelected = (userId: string, checked: boolean) =>
    setSelectedIds((current) => (checked ? [...new Set([...current, userId])] : current.filter((id) => id !== userId)));

  const change = async (action: () => Promise<unknown>, success: string) => {
    setBusy(true);
    try {
      await action();
      await onReload();
      toast.success(t(success));
      return true;
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update member"));
      return false;
    } finally {
      setBusy(false);
    }
  };
  const order = (key: MemberSort) => {
    if (sort === key) setDescending((value) => !value);
    else {
      setSort(key);
      setDescending(false);
    }
  };
  const exportCsv = () => {
    const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
    download(
      "members.csv",
      [
        ["Name", "Username", "Email", "Status", "Role", "Teams", "Joined", "Last seen"].map(quote).join(","),
        ...allMembers.map((member) =>
          [
            member.user.displayName,
            personUsername(member.user),
            member.user.email,
            memberStatusLabel(member),
            member.role,
            String(data.teamMembers.filter((team) => team.userId === member.user.id).length),
            member.joinedAt,
            member.lastSeenAt ?? "",
          ]
            .map(quote)
            .join(","),
        ),
      ].join("\n"),
      "text/csv",
    );
  };
  const copyEmail = (email: string) => {
    void navigator.clipboard?.writeText(email).then(
      () => toast.success(t("Email copied to clipboard")),
      () => undefined,
    );
  };

  // Who the viewer may act on (the reference app's member management rules; owners are managed by owners).
  const manageable = (member: WorkspaceMember) =>
    admin && member.user.id !== data.viewer.id && !isApplicationMember(member) && member.status !== "departed" && (member.role !== "owner" || viewerOwner);
  const teamsFor = (member: WorkspaceMember) => data.teams.filter((team) => isMemberOfTeam(member.user.id, team.id));
  const openIdentity = (member: WorkspaceMember, field: "displayName" | "username" | "email") =>
    setIdentityTarget({ member, field, value: field === "displayName" ? member.user.displayName : field === "username" ? personUsername(member.user) : member.user.email });
  const openTeams = (mode: "add" | "manage", members: WorkspaceMember[]) => {
    setTeamsTarget({ mode, members });
    setTeamDraft(mode === "manage" ? teamsFor(members[0]).map((team) => team.id) : []);
  };
  const openRoles = (members: WorkspaceMember[]) =>
    setRoleTarget({ members, role: members.every((member) => member.role === members[0].role) && members[0].role !== "owner" ? (members[0].role as AssignableRole) : "member" });

  /** The row's own menu (the reference app's contextual actions for that user), grouped; empty groups are dropped. */
  const rowActions = (entry: MemberEntry): ReactNode[][] => {
    if (!admin) return [];
    if (entry.kind === "invitation") {
      const { invitation } = entry;
      return [[
        <DropdownMenuItem key="resend" onSelect={() => void change(() => resendInvitation(workspace, invitation.id), "Invitation resent")}>{t("Resend invite")}</DropdownMenuItem>,
        <DropdownMenuItem key="revoke" onSelect={() => void change(() => revokeInvitation(workspace, invitation.id), "Invitation revoked")}>{t("Revoke invite")}</DropdownMenuItem>,
      ]];
    }
    const { member } = entry;
    if (isApplicationMember(member)) {
      if (member.user.builtinAgent)
        return [[<DropdownMenuItem key="agent" onSelect={() => navigate(settingsPath(workspace, "ai"))}>{t("Manage Flow Agent…")}</DropdownMenuItem>]];
      if (member.status !== "active") return [];
      return [
        [<DropdownMenuItem key="access" onSelect={() => member.user.oauthClientId && navigate(installedApplicationPath(workspace, member.user.oauthClientId))}>{t("Manage team access…")}</DropdownMenuItem>],
        [<DropdownMenuItem key="revoke" onSelect={() => setConfirmTarget({ members: [member], action: "revoke" })}>{t("Revoke access…")}</DropdownMenuItem>],
      ];
    }
    if (!manageable(member)) return [];
    const suspended = member.status === "suspended";
    // SCIM-managed members: role, name, email and status change in the identity provider.
    const locked = isSCIMManaged(member);
    const lockedProps = locked ? { disabled: true, title: t(SCIM_MANAGED_HINT) } : {};
    return [
      locked
        ? [
            <div key="scim" className="settings-member-scim-note" role="note">
              {t(SCIM_MANAGED_HINT)}
            </div>,
          ]
        : [],
      suspended ? [<DropdownMenuItem key="activate" {...lockedProps} onSelect={() => setConfirmTarget({ members: [member], action: "activate" })}>{t("Activate user…")}</DropdownMenuItem>] : [],
      suspended ? [] : [<DropdownMenuItem key="role" {...lockedProps} onSelect={() => openRoles([member])}>{t("Change role…")}</DropdownMenuItem>],
      [
        <DropdownMenuItem key="name" {...lockedProps} onSelect={() => openIdentity(member, "displayName")}>{t("Update name…")}</DropdownMenuItem>,
        <DropdownMenuItem key="username" onSelect={() => openIdentity(member, "username")}>{t("Update username…")}</DropdownMenuItem>,
        ...(suspended ? [] : [<DropdownMenuItem key="email" {...lockedProps} onSelect={() => openIdentity(member, "email")}>{t("Update email…")}</DropdownMenuItem>]),
      ],
      suspended ? [] : [<DropdownMenuItem key="suspend" {...lockedProps} onSelect={() => setConfirmTarget({ members: [member], action: "suspend" })}>{t("Suspend user…")}</DropdownMenuItem>],
      suspended ? [] : [<DropdownMenuItem key="teams" onSelect={() => openTeams("manage", [member])}>{t("Manage teams…")}</DropdownMenuItem>],
      [<DropdownMenuItem key="remove" className="danger-item" {...lockedProps} onSelect={() => setConfirmTarget({ members: [member], action: "remove" })}>{t("Remove from workspace…")}</DropdownMenuItem>],
    ].filter((group) => group.length > 0);
  };

  /** Actions on the selection (the reference app's bulk actions), shown in the bulk bar and in a selected row's menu. */
  const bulkActions = (targets: WorkspaceMember[]): ReactNode[][] => {
    if (!targets.length) return [];
    const others = targets.every(manageable) && !targets.some(isSCIMManaged);
    const allActive = targets.every((member) => member.status === "active");
    const items: ReactNode[] = [];
    if (allActive) {
      const addable = data.teams.some((team) => !targets.every((member) => isMemberOfTeam(member.user.id, team.id)));
      items.push(<DropdownMenuItem key="teams" disabled={!addable} onSelect={() => openTeams("add", targets)}>{t("Add to teams…")}</DropdownMenuItem>);
    }
    if (others && allActive) {
      items.push(<DropdownMenuItem key="roles" onSelect={() => openRoles(targets)}>{t("Change roles…")}</DropdownMenuItem>);
      items.push(<DropdownMenuItem key="suspend" onSelect={() => setConfirmTarget({ members: targets, action: "suspend" })}>{t("Suspend users…")}</DropdownMenuItem>);
    }
    if (others && targets.every((member) => member.status === "suspended"))
      items.push(<DropdownMenuItem key="activate" onSelect={() => setConfirmTarget({ members: targets, action: "activate" })}>{t("Activate users…")}</DropdownMenuItem>);
    return items.length ? [items] : [];
  };

  const menu = (groups: ReactNode[][]) =>
    groups.map((group, index) => (
      <Fragment key={index}>
        {index > 0 && <DropdownMenuSeparator />}
        {group}
      </Fragment>
    ));

  const sortButton = (key: MemberSort, label: string) => (
    <button
      type="button"
      data-sort={key}
      aria-label={`${t("Order by")} ${t(label)}${sort === key ? `, ${t(descending ? "sorted descending" : "sorted ascending")}` : ""}`}
      onClick={() => order(key)}
    >
      <span className="settings-members-column-label">{t(label)}</span>
      {sort === key && <>
        <span className="settings-members-desktop-sort">{descending ? <ArrowUp aria-hidden size={12} /> : <ArrowDown aria-hidden size={12} />}</span>
        <MembersSortGlyph descending={descending} />
      </>}
    </button>
  );
  // Group labels share English words with unrelated UI ("Active" projects), so their Chinese copy is keyed by context.
  const groupLabel = (group: MemberGroup) => {
    const key = `Members group: ${group}`;
    const translated = t(key);
    return translated === key ? group : translated;
  };

  const rowMenu = (label: string, groups: ReactNode[][]) =>
    groups.length > 0 ? (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button type="button" className="settings-member-more" aria-label={`${t("Open menu")} ${label}`}>
            <MoreHorizontal size={16} className="settings-members-desktop-more" />
            <FlowGlyph name="ellipsis" size={16} className="settings-members-phone-more" />
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="settings-members-menu settings-member-action-menu" data-i18n-ignore>
          {menu(groups)}
        </DropdownMenuContent>
      </DropdownMenu>
    ) : null;

  const invitationRow = (invitation: Invitation, key: string) => (
    <div className="settings-member-directory-row is-invited" data-member-row="invitation" key={key}>
      <span>
        <UserAvatar className="settings-member-avatar is-inactive" name={invitation.email} />
        <i>
          <strong data-i18n-ignore>{invitation.email}</strong>
        </i>
      </span>
      <span>
        <button type="button" className="settings-member-email" data-i18n-ignore onClick={() => copyEmail(invitation.email)}>
          {invitation.email}
        </button>
      </span>
      <span>
        <em className={`settings-member-role${isAdminRole(invitation.role) ? " is-admin" : ""}`}>{t(invitationStatusLabel(invitation))}</em>
      </span>
      <span>{invitation.teamIds.length ? `${invitation.teamIds.length} ${t(invitation.teamIds.length === 1 ? "team" : "teams")}` : ""}</span>
      <time dateTime={invitation.createdAt} title={`${t("Invited")} ${fullDateTime(invitation.createdAt)}`}>
        {shortDate(invitation.createdAt)}
      </time>
      <span />
      <span>{rowMenu(invitation.email, rowActions({ kind: "invitation", key, invitation }))}</span>
    </div>
  );

  const memberRow = (member: WorkspaceMember, key: string) => {
    const teams = teamsFor(member);
    const selected = selectedIds.includes(member.user.id);
    const app = isApplicationMember(member);
    const inactive = member.status !== "active";
    const status = memberStatusLabel(member);
    const pill = !inactive && !app;
    const canSelect = memberSelectable(member, admin, selectionActive) || selected;
    const linked = member.status !== "departed";
    const actions = selected ? bulkActions(selectedMembers) : rowActions({ kind: "member", key, member });
    return (
      <div
        className={`settings-member-directory-row${selected ? " is-selected" : ""}${inactive && !app ? " is-inactive" : ""}`}
        data-member-row={app ? "application" : inactive ? "inactive" : "active"}
        key={key}
      >
        <span>
          {canSelect && (
            <input
              type="checkbox"
              className="settings-member-check"
              aria-label={`${t("Select")} ${personUsername(member.user) || member.user.name}`}
              checked={selected}
              onChange={(event) => toggleSelected(member.user.id, event.target.checked)}
            />
          )}
          <UserAvatar
            className={`settings-member-avatar${inactive && !app ? " is-inactive" : ""}`}
            name={member.user.displayName || member.user.name}
            avatarUrl={member.user.avatarUrl}
          />
          <i>
            {linked ? (
              <AppLink className="settings-member-link" href={memberProfilePath(workspace, personUsername(member.user) || member.user.id)}>
                <strong data-i18n-ignore>{member.user.displayName}</strong>
              </AppLink>
            ) : (
              <strong data-i18n-ignore>{member.user.displayName}</strong>
            )}
            <small data-i18n-ignore>{personUsername(member.user)}</small>
          </i>
          {isSCIMManaged(member) && (
            <em className="settings-member-scim-badge" title={t(SCIM_MANAGED_HINT)}>
              {t("Managed by SCIM")}
            </em>
          )}
        </span>
        <span>
          {!app && member.user.email && (
            <button type="button" className="settings-member-email" data-i18n-ignore onClick={() => copyEmail(member.user.email)}>
              {member.user.email}
            </button>
          )}
        </span>
        <span>
          {pill ? (
            <em className={`settings-member-role${isAdminRole(member.role) ? " is-admin" : ""}`}><span>{t(status)}</span></em>
          ) : (
            t(status)
          )}
        </span>
        <span title={teams.map((team) => team.name).join(", ") || undefined}>
          {teams.length ? `${teams.length} ${t(teams.length === 1 ? "team" : "teams")}` : ""}
        </span>
        <time dateTime={member.joinedAt} title={`${t("Joined")} ${fullDateTime(member.joinedAt)}`}>
          {shortDate(member.joinedAt)}
        </time>
        <span>
          {isOnline(member) ? (
            <span className="settings-member-presence">
              <i className="settings-member-online" />
              {t("Online")}
            </span>
          ) : member.lastSeenAt ? (
            <time dateTime={member.lastSeenAt} title={`${t("Last seen")} ${fullDate(member.lastSeenAt)}`}>
              {shortDate(member.lastSeenAt)}
            </time>
          ) : null}
        </span>
        <span>{rowMenu(member.user.displayName, actions)}</span>
      </div>
    );
  };

  const confirmMembers = confirmTarget?.members ?? [];
  const confirmSubject = confirmMembers.length === 1 ? confirmMembers[0].user.displayName : `${confirmMembers.length} ${t("members")}`;
  const confirmCopy: Record<ConfirmKind, { title: string; description: string; confirm: string; success: string }> = {
    suspend: { title: "Suspend {name}?", description: "They won’t be able to access this workspace.", confirm: "Suspend", success: "Member suspended" },
    activate: { title: "Activate {name}?", description: "They will be able to access this workspace again.", confirm: "Confirm", success: "Member activated" },
    remove: { title: "Remove {name}?", description: "This member will lose access to the workspace and all teams.", confirm: "Remove", success: "Member removed" },
    revoke: { title: "Revoke access for {name}?", description: "The application will lose access to the workspace for now. It can be reauthorized in the future.", confirm: "Confirm", success: "Access revoked" },
  };
  const confirmText = confirmTarget ? confirmCopy[confirmTarget.action] : undefined;
  const runConfirm = () => {
    if (!confirmTarget) return;
    const { action, members } = confirmTarget;
    const run = (member: WorkspaceMember) =>
      action === "suspend"
        ? suspendMember(workspace, member.user.id)
        : action === "activate"
          ? resumeMember(workspace, member.user.id)
          : action === "revoke"
            ? uninstallApplication(workspace, installationIdOf(member.user.id))
            : removeMember(workspace, member.user.id);
    void change(() => Promise.all(members.map(run)), confirmCopy[action].success).then((ok) => {
      if (!ok) return;
      setConfirmTarget(undefined);
      setSelectedIds((current) => current.filter((id) => !members.some((member) => member.user.id === id)));
    });
  };

  const saveTeams = () => {
    const target = teamsTarget;
    if (!target) return;
    const changes =
      target.mode === "add"
        ? teamDraft.flatMap((teamId) =>
            target.members.filter((member) => !isMemberOfTeam(member.user.id, teamId)).map((member) => ({ teamId, userId: member.user.id, member: true })),
          )
        : data.teams.flatMap((team) => {
            const userId = target.members[0].user.id;
            const wanted = teamDraft.includes(team.id);
            return wanted === isMemberOfTeam(userId, team.id) ? [] : [{ teamId: team.id, userId, member: wanted }];
          });
    void change(
      () => Promise.all(changes.map((item) => setTeamMembership(workspace, item.teamId, item.userId, item.member, "member"))),
      "Team access updated",
    ).then((ok) => ok && setTeamsTarget(undefined));
  };
  const teamOptions =
    teamsTarget?.mode === "add" ? data.teams.filter((team) => !teamsTarget.members.every((member) => isMemberOfTeam(member.user.id, team.id))) : data.teams;

  return (
    <div className="settings-members-page settings-workspace-members" data-i18n-ignore>
      <PageTitle>{t("Members")}</PageTitle>
      <div className="settings-members-toolbar">
        <label>
          <Search size={14} className="settings-members-desktop-search" />
          <SettingsSearchGlyph size={16} className="settings-members-phone-search" />
          <input aria-label={t("Search by name or email")} placeholder={t("Search by name or email")} value={query} onChange={(event) => setQuery(event.target.value)} />
        </label>
        <DropdownMenu phoneSheet={false}>
          <DropdownMenuTrigger asChild>
            <button type="button" className="settings-members-filter" aria-label={t("Member filter")}>
              {t(activeFilter)}
              <ChevronDown aria-hidden size={12} />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" sideOffset={4} className="settings-members-menu settings-members-filter-menu">
            <DropdownMenuPrimitive.RadioGroup value={activeFilter} onValueChange={(value) => setFilter(value as MemberFilter)}>
              {filters.map((value) => (
                <DropdownMenuPrimitive.RadioItem key={value} value={value} className="menu-item">
                  {t(value)}
                  <DropdownMenuPrimitive.ItemIndicator className="settings-members-menu-check">
                    <Check size={14} />
                  </DropdownMenuPrimitive.ItemIndicator>
                </DropdownMenuPrimitive.RadioItem>
              ))}
            </DropdownMenuPrimitive.RadioGroup>
          </DropdownMenuContent>
        </DropdownMenu>
        <PhoneNativeSelect className="settings-members-phone-filter" aria-label={t("Member filter")} value={activeFilter} onChange={event => setFilter(event.target.value as MemberFilter)}>
          {filters.map(value => <option key={value} value={value}>{t(value)}</option>)}
        </PhoneNativeSelect>
        <span />
        <button type="button" className="settings-action" onClick={exportCsv}>
          {t("Export CSV")}
        </button>
        <button type="button" className="settings-action primary" onClick={() => setInviteOpen(true)}>
          {t("Invite")}
        </button>
      </div>
      <div className={`settings-members-directory${selecting ? " has-selection" : ""}`}>
        {groups.length > 0 ? (
          <>
            <div className="settings-members-columns">
              {sortButton("name", "Name")}
              {sortButton("email", "Email")}
              {sortButton(desktop ? "role" : "status", "Role")}
              <span>{t("Teams")}</span>
              {sortButton("joined", "Joined")}
              {sortButton("lastSeen", "Last seen")}
              <span />
            </div>
            {groups.map(([group, items]) => (
              <Fragment key={group}>
                <div className="settings-members-group" data-member-group={group.toLowerCase()} role="presentation">
                  <strong>{groupLabel(group)}</strong>
                  <span>{items.length}</span>
                </div>
                {items.map((entry) => (entry.kind === "invitation" ? invitationRow(entry.invitation, entry.key) : memberRow(entry.member, entry.key)))}
              </Fragment>
            ))}
          </>
        ) : (
          <div className="settings-members-empty">
            <UserRound size={48} strokeWidth={1} aria-hidden />
            <p>{t(emptyMembersMessage(activeFilter, filtered))}</p>
            {filtered && (
              <button
                type="button"
                className="settings-members-clear-filter"
                onClick={() => {
                  setQuery("");
                  setFilter("All");
                }}
              >
                {t("Clear filter")}
                <X aria-hidden size={12} />
              </button>
            )}
          </div>
        )}
      </div>
      {selecting && (
        <div className="settings-members-bulk-bar" role="toolbar" aria-label={t("Selected members")}>
          <span>
            {selectedMembers.length} {t("selected")}
          </span>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button type="button" className="settings-members-bulk-actions" aria-label={t("Open command menu")}>
                <Command aria-hidden size={12} />
                {t("Actions")}
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="top" align="center" sideOffset={8} className="settings-members-menu settings-member-action-menu" data-i18n-ignore>
              {menu(bulkActions(selectedMembers))}
            </DropdownMenuContent>
          </DropdownMenu>
          <button type="button" className="settings-members-bulk-clear" aria-label={t("Clear selected")} onClick={() => setSelectedIds([])}>
            <X aria-hidden size={14} />
          </button>
        </div>
      )}
      <InviteMembersDialog workspace={data.workspace} teams={data.teams} teamSettings={data.teamSettings} open={inviteOpen} onClose={() => setInviteOpen(false)} onInvited={onReload} />
      <Dialog open={Boolean(identityTarget)} onOpenChange={(open) => !open && setIdentityTarget(undefined)}>
        <DialogContent className="settings-member-dialog">
          <DialogTitle>{t(identityTarget?.field === "displayName" ? "Update name" : identityTarget?.field === "username" ? "Update username" : "Update email")}</DialogTitle>
          <input
            autoFocus
            className="settings-input"
            value={identityTarget?.value ?? ""}
            onChange={(event) => setIdentityTarget((current) => (current ? { ...current, value: event.target.value } : current))}
          />
          <footer>
            <button type="button" className="settings-action" onClick={() => setIdentityTarget(undefined)}>
              {t("Cancel")}
            </button>
            <button
              type="button"
              className="settings-action primary"
              disabled={busy || !identityTarget?.value.trim()}
              onClick={() =>
                identityTarget &&
                void change(
                  () => updateMemberIdentity(workspace, identityTarget.member.user.id, { [identityTarget.field]: identityTarget.value.trim() }),
                  "Member updated",
                ).then((ok) => ok && setIdentityTarget(undefined))
              }
            >
              {t("Save")}
            </button>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog open={Boolean(roleTarget)} onOpenChange={(open) => !open && setRoleTarget(undefined)}>
        <DialogContent className="settings-member-dialog">
          <DialogTitle>{t(roleTarget && roleTarget.members.length > 1 ? "Change roles" : "Change role")}</DialogTitle>
          {ASSIGNABLE_ROLES.map((role) => (
            <label className="settings-member-role-option" key={role}>
              <input type="radio" name="settings-member-role" checked={roleTarget?.role === role} onChange={() => setRoleTarget((current) => (current ? { ...current, role } : current))} />
              <span>{t(title(role))}</span>
            </label>
          ))}
          <footer>
            <button type="button" className="settings-action" onClick={() => setRoleTarget(undefined)}>
              {t("Cancel")}
            </button>
            <button
              type="button"
              className="settings-action primary"
              disabled={busy}
              onClick={() => {
                const target = roleTarget;
                if (!target) return;
                void change(
                  () => Promise.all(target.members.filter((member) => member.role !== target.role).map((member) => updateMemberRole(workspace, member.user.id, target.role))),
                  "Role updated",
                ).then((ok) => ok && setRoleTarget(undefined));
              }}
            >
              {t("Save")}
            </button>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog open={Boolean(teamsTarget)} onOpenChange={(open) => !open && setTeamsTarget(undefined)}>
        <DialogContent className="settings-member-dialog">
          <DialogTitle>{t(teamsTarget?.mode === "manage" ? "Manage teams" : "Add to teams")}</DialogTitle>
          {teamOptions.map((team) => (
            <label className="settings-member-team-option" key={team.id}>
              <input
                type="checkbox"
                checked={teamDraft.includes(team.id)}
                onChange={(event) => setTeamDraft((current) => (event.target.checked ? [...current, team.id] : current.filter((id) => id !== team.id)))}
              />
              <ViewGlyph color={team.color} icon={team.icon || "Team"} />
              <span data-i18n-ignore>{team.name}</span>
            </label>
          ))}
          {teamsTarget?.mode === "manage" && teamDraft.length > 0 && (
            <p className="settings-member-dialog-note">{t("{name} has full access to these teams").replace("{name}", teamsTarget.members[0].user.displayName)}</p>
          )}
          <footer>
            <button type="button" className="settings-action" onClick={() => setTeamsTarget(undefined)}>
              {t("Cancel")}
            </button>
            <button type="button" className="settings-action primary" disabled={busy || (teamsTarget?.mode === "add" && !teamDraft.length)} onClick={saveTeams}>
              {t(teamsTarget?.mode === "manage" ? "Save" : "Add")}
            </button>
          </footer>
        </DialogContent>
      </Dialog>
      <Dialog open={Boolean(confirmTarget)} onOpenChange={(open) => !open && setConfirmTarget(undefined)}>
        <DialogContent className="settings-confirm">
          <DialogTitle>{confirmText ? t(confirmText.title).replace("{name}", confirmSubject) : ""}</DialogTitle>
          <p>{confirmText ? t(confirmText.description) : ""}</p>
          <footer>
            <button type="button" className="settings-action" onClick={() => setConfirmTarget(undefined)}>
              {t("Cancel")}
            </button>
            <button type="button" className={`settings-action ${confirmTarget?.action === "activate" ? "primary" : "danger"}`} disabled={busy} onClick={runConfirm}>
              {confirmText ? t(confirmText.confirm) : ""}
            </button>
          </footer>
        </DialogContent>
      </Dialog>
    </div>
  );
}
