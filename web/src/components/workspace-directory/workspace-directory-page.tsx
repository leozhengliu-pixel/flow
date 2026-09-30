import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { refreshResourcePreferences } from '@/lib/resource-preferences';
import { teamHierarchy } from '@/lib/team-hierarchy';
import { formatCustomerRevenue } from '@/lib/customer-settings';
import { personMatchesQuery, personSearchText, personUsername } from '@/lib/people';
import { compareDirectoryTeams, indexTeamPeople, matchesTeamDate, matchesTeamFilters, teamDateChoices, teamTimestamp, type TeamFilterField, type TeamOrdering } from './team-directory-model';
import { TeamDateFilterDialog, TeamFilterBar } from './team-directory-controls';
import { useTeamDirectoryControls, type TeamColumn } from './use-team-directory-controls';
import { AppLink } from '@/components/ui/app-link';
import {
  ArrowDown,
  ArrowUp,
  Banknote,
  Check,
  ChevronRight,
  Circle,
  MoreHorizontal,
  Plus,
  Search,
  Settings2,
  Trash2,
  UserRound,
  UsersRound,
  X,
} from "lucide-react";
import {
  Fragment,
  useEffect,
  useMemo,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import { VirtualColumnList } from '@/components/ui/virtual-column-list';
import { toggleFavoriteFor } from "@/lib/favorites";
import { addSubscription, removeSubscription, setTeamMembership } from "@/lib/api";
import { ContentViewHeaderSearch } from '@/components/content-view/content-view-header-search'
import { useI18n } from "@/i18n/i18n";

import { UserAvatar } from "@/components/ui/user-avatar";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { CustomerDialog } from "@/components/customer-detail/customer-dialog";
import type {
  BootstrapData,
  Customer,
  CustomerMutationInput,
  Invitation,
  Team,
  User,
  WorkspaceMember,
} from "@/types/flow";

import "./workspace-directory.css";
import {
  DirectoryDisplayMenu,
  DirectoryCreatedDateIcon,
  DirectoryFilterMenu,
  DirectoryLockIcon,
  DirectoryPeopleIcon,
  type DirectoryFilterGroup,
} from "./directory-menus";
import { InviteMembersDialog } from "./invite-members-dialog";
import { memberColumnIds, useMemberDirectoryPreferences, type MemberColumn, type MemberOrdering } from "./use-member-directory-preferences";

type DirectoryKind = "members" | "customers" | "teams";
type CustomerColumn =
  | "requests"
  | "revenue"
  | "size"
  | "owner"
  | "status"
  | "tier"
  | "domains"
  | "source";
type CustomerOrdering = "created" | "updated" | "name" | "requests" | "revenue" | "size" | "status" | "tier";
const DIRECTORY_VIRTUALIZATION_THRESHOLD = 80;

function DirectoryRows<T>({ header, items, itemKey, render }: { header: ReactNode; items: readonly T[]; itemKey: (item: T) => string; render: (item: T) => ReactNode }) {
  if (items.length <= DIRECTORY_VIRTUALIZATION_THRESHOLD) return <>{header}{items.map((item) => <Fragment key={itemKey(item)}>{render(item)}</Fragment>)}</>;
  return <VirtualColumnList header={header} scrollerClassName="workspace-directory__virtual-list" data={items} computeItemKey={(_index, item) => itemKey(item)} increaseViewportBy={{ top: 196, bottom: 490 }} itemContent={(_index, item) => render(item)}/>;
}

export function WorkspaceDirectoryPage({
  kind,
  data,
  inviteOnOpen = false,
  customerOnOpen = false,
  onOpenSidebar,
  onNavigateTeamMembers,
  onNavigateMember,
  onNavigateTeam,
  onNavigateTeamProjects,
  onNavigateTeamCycles,
  onNavigateTeamsSettings,
  onNewTeam,
  onCreateCustomer,
  onUpdateCustomer,
  onDeleteCustomer,
  onOpenCustomer,
  onReload,
}: {
  kind: DirectoryKind;
  data: BootstrapData;
  inviteOnOpen?: boolean;
  customerOnOpen?: boolean;
  onOpenSidebar: () => void;
  onNavigateTeamMembers: (team: Team) => void;
  onNavigateMember: (user: User) => void;
  onNavigateTeam: (team: Team) => void;
  onNavigateTeamProjects: (team: Team) => void;
  onNavigateTeamCycles: (team: Team) => void;
  onNavigateTeamsSettings: () => void;
  onNewTeam: () => void;
  onCreateCustomer: (
    input: CustomerMutationInput & { name: string },
  ) => Promise<void>;
  onUpdateCustomer: (
    customer: Customer,
    input: CustomerMutationInput,
  ) => Promise<void>;
  onDeleteCustomer: (customer: Customer) => Promise<void>;
  onOpenCustomer: (customer: Customer) => void;
  /** Refreshes workspace metadata; a team id also refetches that team's issues (membership changed). */
  onReload: (teamId?: string) => Promise<void>;
}) {
  const {t}=useI18n();
  const [inviteOpen, setInviteOpen] = useState(inviteOnOpen);
  const [customerOpen, setCustomerOpen] = useState(customerOnOpen);
  const [customerResultCount, setCustomerResultCount] = useState<number>();
  const title = t(
    kind === "members"
      ? "Members"
      : kind === "customers"
        ? "Customers"
        : "Teams");
  return (
    <main className="main-panel workspace-directory" aria-label={title}>
      <DirectoryHeader
        title={title}
        count={kind === "customers" ? customerResultCount : undefined}
        onOpenSidebar={onOpenSidebar}
        onCreate={kind === "members" && data.viewerRole !== "admin" && data.viewerRole !== "owner" ? undefined : () =>
          kind === "members"
            ? setInviteOpen(true)
            : kind === "customers"
              ? setCustomerOpen(true)
              : onNewTeam()
        }
        createLabel={
          kind === "members"
            ? t("Invite members")
            : kind === "customers"
              ? t("New customer")
              : t("New team")
        }
        createVariant={kind === "customers" ? "pill" : "ghost"}
        options={kind === "teams" ? <TeamsOptions onOpenSettings={onNavigateTeamsSettings} /> : undefined}
      />
      {kind === "members" && <MembersDirectory key={`${data.workspace.id}:${data.viewer.id}`} data={data} onOpen={onNavigateMember} onOpenTeam={onNavigateTeam} />}
      {kind === "customers" && (
        <CustomersDirectory
          featureSettings={data.workspaceSettings.featureSettings}
          customers={data.customers ?? []}
          requests={data.customerRequests}
          users={data.users}
          onResultCount={setCustomerResultCount}
          onCreate={() => setCustomerOpen(true)}
          onUpdate={onUpdateCustomer}
          onDelete={onDeleteCustomer}
          onOpen={onOpenCustomer}
        />
      )}
      {kind === "teams" && (
        <TeamsDirectory
          key={`${data.workspace.id}:${data.viewer.id}`}
          data={data}
          onMembers={onNavigateTeamMembers}
          onOpen={onNavigateTeam}
          onProjects={onNavigateTeamProjects}
          onCycles={onNavigateTeamCycles}
          onReload={onReload}
        />
      )}
      {kind === "members" && <InviteMembersDialog
        open={inviteOpen}
        workspace={data.workspace}
        teams={data.teams}
        onClose={() => setInviteOpen(false)}
        onInvited={() => onReload()}
      />}
      <CustomerDialog
        currency={data.workspaceSettings.featureSettings?.customerRevenueCurrency}
        open={customerOpen}
        users={data.users}
        onOpenChange={setCustomerOpen}
        onSubmit={async (input) => {
          await onCreateCustomer(input);
        }}
      />
    </main>
  );
}

function DirectoryHeader({
  title,
  count,
  createLabel,
  createVariant = "pill",
  onCreate,
  onOpenSidebar,
  options,
}: {
  title: string;
  count?: number;
  createLabel: string;
  createVariant?: "pill" | "ghost";
  onCreate?: () => void;
  onOpenSidebar: () => void;
  options?: ReactNode;
}) {
  return (
    <header className="workspace-directory__header">
      {onCreate && <button
        className="workspace-directory__mobile"
        type="button"
        aria-label="Open sidebar"
        data-sidebar-trigger onClick={onOpenSidebar}
      >
        <span />
        <span />
        <span />
      </button>}
      <h1>{title}</h1>
      {count !== undefined && <small>{count}</small>}
      {options}
      {onCreate && <button
        className={`workspace-directory__icon-button workspace-directory__create${createVariant === "ghost" ? " is-ghost" : ""}`}
        type="button"
        aria-label={createLabel}
        title={createLabel}
        onClick={onCreate}
      >
        <Plus />
        <span>{createLabel}</span>
      </button>}
    </header>
  );
}

function TeamsOptions({ onOpenSettings }: { onOpenSettings: () => void }) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          className="workspace-directory__title-menu"
          type="button"
          aria-label="Teams options"
        >
          <MoreHorizontal />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating"
          className="workspace-directory__menu"
          align="start"
          sideOffset={6}
        >
          <DropdownMenu.Item onSelect={onOpenSettings}>
            <Settings2 />
            <span>Go to teams settings</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

type MemberStatusFilter = "admin" | "guest" | "member";
type MemberEntry = { kind: "member"; member: WorkspaceMember } | { kind: "invitation"; invitation: Invitation };
const MEMBER_COLUMN_WIDTHS: Record<Exclude<MemberColumn, "teams">, string> = { status: "99px", joined: "94px" };
/** Linear's Teams column is 71px; it only grows when a key (plus the "+N" chip) would otherwise truncate. */
const MEMBER_TEAMS_MIN_WIDTH = 71;
const MEMBER_ONLINE_WINDOW_MS = 5 * 60 * 1000;

function memberStatusFilter(role: WorkspaceMember["role"]): MemberStatusFilter | undefined {
  if (role === "owner" || role === "admin") return "admin";
  if (role === "guest" || role === "member") return role;
  return undefined;
}

function memberEntryStatus(entry: MemberEntry) {
  if (entry.kind === "invitation") return memberStatusFilter(entry.invitation.role);
  return entry.member.user.app ? undefined : memberStatusFilter(entry.member.role);
}

function memberStatusLabel(entry: MemberEntry) {
  if (entry.kind === "invitation") return "Invited";
  if (entry.member.user.app || entry.member.role === "app") return "Application";
  if (entry.member.status === "suspended") return "Suspended";
  return capitalize(entry.member.role);
}

/** Linear shows every non-member status (Owner/Admin, Guest, Application, Invited, Suspended) as a badge. */
function memberStatusIsBadge(entry: MemberEntry) {
  return memberStatusLabel(entry) !== "Member";
}

function memberEntryName(entry: MemberEntry) {
  return entry.kind === "member" ? entry.member.user.displayName : entry.invitation.email;
}

function memberEntryJoined(entry: MemberEntry) {
  return entry.kind === "member" ? entry.member.joinedAt : entry.invitation.createdAt;
}

function compareMemberEntries(left: MemberEntry, right: MemberEntry, ordering: MemberOrdering) {
  const byName = memberEntryName(left).localeCompare(memberEntryName(right));
  if (ordering === "status") return memberStatusLabel(left).localeCompare(memberStatusLabel(right)) || byName;
  if (ordering === "joined") return (new Date(memberEntryJoined(left)).getTime() - new Date(memberEntryJoined(right)).getTime()) || byName;
  return byName;
}

/** Teams column fits the widest key (icon + key, plus the "+N" chip when anyone has one) so keys never truncate into "D… +1". */
function memberTeamsColumnWidth(longestKey: number, hasExtraTeams: boolean) {
  return `max(${MEMBER_TEAMS_MIN_WIDTH}px, calc(${Math.max(longestKey, 1)}ch + ${hasExtraTeams ? 76 : 34}px))`;
}

function memberDirectoryColumns(columns: Set<MemberColumn>, longestTeamKey: number, hasExtraTeams: boolean) {
  const widths = memberColumnIds.filter(column => columns.has(column)).map(column => column === "teams" ? memberTeamsColumnWidth(longestTeamKey, hasExtraTeams) : MEMBER_COLUMN_WIDTHS[column]);
  return `minmax(160px, 1fr) ${widths.join(" ")} 85px`;
}

function MembersDirectory({ data, onOpen, onOpenTeam }: { data: BootstrapData; onOpen: (user: User) => void; onOpenTeam:(team:Team)=>void }) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [preferences, setPreferences] = useMemberDirectoryPreferences(data.workspace.id, data.viewer.id);
  const { ordering, descending } = preferences;
  const columns = useMemo(() => new Set(preferences.columns), [preferences.columns]);
  const [statuses, setStatuses] = useState<Set<string>>(new Set());
  const [advanced, setAdvanced] = useState(false);
  const teamsByUserId = useMemo(() => {
    const teamsById = new Map(data.teams.map(team => [team.id, team]));
    const result = new Map<string, Team[]>();
    for (const membership of data.teamMembers) {
      const team = teamsById.get(membership.teamId);
      if (!team) continue;
      const teams = result.get(membership.userId) ?? [];
      teams.push(team);
      result.set(membership.userId, teams);
    }
    return result;
  }, [data.teamMembers, data.teams]);
  const longestTeamKey = useMemo(() => Math.max(0, ...[...teamsByUserId.values()].map(teams => teams[0]?.key.length ?? 0), ...data.invitations.flatMap(invitation => data.teams.filter(team => invitation.teamIds.includes(team.id)).slice(0, 1).map(team => team.key.length))), [data.invitations, data.teams, teamsByUserId]);
  const hasExtraTeams = useMemo(() => [...teamsByUserId.values()].some(teams => teams.length > 1) || data.invitations.some(invitation => invitation.status === "pending" && invitation.teamIds.length > 1), [data.invitations, teamsByUserId]);
  const entries = useMemo<MemberEntry[]>(() => [
    ...data.members.map(member => ({ kind: "member" as const, member })),
    ...data.invitations.filter(invitation => invitation.status === "pending").map(invitation => ({ kind: "invitation" as const, invitation })),
  ], [data.invitations, data.members]);
  const statusCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const entry of entries) {
      const status = memberEntryStatus(entry);
      if (status) counts.set(status, (counts.get(status) ?? 0) + 1);
    }
    return counts;
  }, [entries]);
  const visible = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return entries
      .filter((entry) => {
        if (entry.kind === "member" ? needle && !personMatchesQuery(entry.member.user, needle) : needle && !entry.invitation.email.toLocaleLowerCase().includes(needle)) return false;
        if (statuses.size) {
          const status = memberEntryStatus(entry);
          if (!status || !statuses.has(status)) return false;
        }
        return true;
      })
      .sort((left, right) => compareMemberEntries(left, right, ordering) * (descending ? -1 : 1));
  }, [descending, entries, ordering, query, statuses]);
  const filtersActive = advanced || statuses.size > 0;
  const countLabel = (count: number) => `${count} ${count === 1 ? "member" : "members"}`;
  const filterGroups: DirectoryFilterGroup[] = [
    { id: "status", label: "Status", icon: <DirectoryPeopleIcon />, choices: ([
      { id: "admin", label: "Admin" },
      { id: "guest", label: "Guest" },
      { id: "member", label: "Member" },
    ] as const).map(choice => ({ ...choice, meta: statusCounts.get(choice.id) ? countLabel(statusCounts.get(choice.id)!) : undefined })) },
  ];
  const changeFilter = (groupId: string, choiceId: string, checked: boolean) => {
    if (groupId !== "status") return;
    setStatuses((current) => {
      const next = new Set(current);
      if (checked) next.add(choiceId);
      else next.delete(choiceId);
      return next;
    });
  };
  const clearFilters = () => { setStatuses(new Set()); setAdvanced(false); };
  const setOrdering = (next: MemberOrdering) => setPreferences(current => ({ ...current, ordering: next }));
  const toggleDirection = () => setPreferences(current => ({ ...current, descending: !current.descending }));
  const changeSort = (next: MemberOrdering) => setPreferences(current => current.ordering === next
    ? { ...current, descending: !current.descending }
    : { ...current, ordering: next, descending: false });
  const toggleColumn = (column: MemberColumn) => setPreferences(current => ({
    ...current,
    columns: current.columns.includes(column) ? current.columns.filter(id => id !== column) : memberColumnIds.filter(id => id === column || current.columns.includes(id)),
  }));
  const shortDate = (value: Date) => new Intl.DateTimeFormat("en-US", value.getFullYear() === new Date().getFullYear() ? { month: "short", day: "numeric" } : { month: "short", day: "numeric", year: "numeric" }).format(value);
  const dateTimeTitle = (value: Date) => `${new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(value)}, ${new Intl.DateTimeFormat("en-US", { hour: "numeric", minute: "2-digit", second: "2-digit" }).format(value)}`;
  const longDate = (value: Date) => new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric" }).format(value);
  const renderStatus = (entry: MemberEntry) => {
    const label = t(memberStatusLabel(entry));
    return <span className="workspace-member-status">{memberStatusIsBadge(entry) ? <span className="workspace-member-role">{label}</span> : label}</span>;
  };
  const renderTeams = (teams: Team[]) => {
    const team = teams[0];
    const extra = teams.length - 1;
    return <div className="workspace-member-teams">{team ? <>
      <button type="button" aria-label={`Open team ${team.name}`} title={team.name} onClick={(event) => { event.preventDefault(); event.stopPropagation(); onOpenTeam(team) }}><TeamGlyph team={team} /><span data-i18n-ignore>{team.key}</span></button>
      {extra > 0 ? <span className="workspace-member-teams__more" data-i18n-ignore title={teams.map(item => `${item.key} · ${item.name}`).join(", ")}>+{extra}</span> : null}
    </> : null}</div>;
  };
  const renderEntry = (entry: MemberEntry) => {
    if (entry.kind === "invitation") {
      const invitation = entry.invitation;
      const invitedTeams = data.teams.filter(item => invitation.teamIds.includes(item.id));
      const invited = new Date(invitation.createdAt);
      return <div className="workspace-directory-member-row is-invited">
        <div className="workspace-member-identity"><span className="workspace-directory-avatar is-invited">{initials(invitation.email)}</span><span><strong data-i18n-ignore>{invitation.email}</strong></span></div>
        {columns.has("status") && renderStatus(entry)}
        {columns.has("joined") && <time className="workspace-member-date" dateTime={invitation.createdAt} title={`Invited ${dateTimeTitle(invited)}`}>{shortDate(invited)}</time>}
        {columns.has("teams") && renderTeams(invitedTeams)}
        <span className="workspace-member-last-seen" />
      </div>;
    }
    const member = entry.member;
    const user = member.user;
    const teams = member.status === "active" ? teamsByUserId.get(user.id) ?? [] : [];
    const joined = new Date(member.joinedAt);
    const lastSeen = member.lastSeenAt ? new Date(member.lastSeenAt) : undefined;
    const online = member.status === "active" && !user.app && (user.id === data.viewer.id || (lastSeen !== undefined && Date.now() - lastSeen.getTime() < MEMBER_ONLINE_WINDOW_MS));
    return <a
      className="workspace-directory-member-row"
      href={`/${encodeURIComponent(data.workspace.urlKey)}/profiles/${encodeURIComponent(user.name)}`}
      onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return; event.preventDefault(); onOpen(user) }}
    >
      <div className="workspace-member-identity"><DirectoryUserAvatar user={user} /><span><strong data-i18n-ignore>{user.displayName || user.name}</strong><small data-i18n-ignore>{personUsername(user)}</small></span></div>
      {columns.has("status") && renderStatus(entry)}
      {columns.has("joined") && <time className="workspace-member-date" dateTime={member.joinedAt} title={`Joined ${dateTimeTitle(joined)}`}>{shortDate(joined)}</time>}
      {columns.has("teams") && renderTeams(teams)}
      {online
        ? <span className="workspace-member-last-seen is-online"><i aria-hidden="true" />{t("Online")}</span>
        : member.status === "suspended"
          ? <span className="workspace-member-last-seen">{t("Suspended")}</span>
          : lastSeen
            ? <time className="workspace-member-last-seen" dateTime={member.lastSeenAt} title={`Last seen ${longDate(lastSeen)}`}>{shortDate(lastSeen)}</time>
            : <span className="workspace-member-last-seen">{t("Never")}</span>}
    </a>;
  };
  return (
    <>
      <div className="workspace-directory__toolbar workspace-members-toolbar">
        <ContentViewHeaderSearch
          alwaysVisible
          aria-label={t("Find members")}
          placeholder={t("Find members…")}
          value={query}
          onChange={setQuery}
          className="workspace-members-cvh-search"
        />
        <span />
        <DirectoryFilterMenu
          groups={filterGroups}
          hideSearch
          menuClassName="workspace-members-filter-menu"
          submenuClassName="workspace-members-filter-menu"
          onAdvanced={() => setAdvanced(true)}
          onChoice={changeFilter}
          selected={{ status: statuses }}
        />
        <DirectoryDisplayMenu<MemberColumn, MemberOrdering>
          className="is-compact"
          descending={descending}
          onDirection={toggleDirection}
          onOrdering={setOrdering}
          onProperty={toggleColumn}
          ordering={ordering}
          orderingOptions={[
            { id: "name", label: "Name" },
            { id: "status", label: "Status" },
            { id: "joined", label: "Joined" },
          ]}
          properties={columns}
          propertyOptions={[
            { id: "status", label: "Status" },
            { id: "joined", label: "Joined" },
            { id: "teams", label: "Teams" },
          ]}
        />
      </div>
      {filtersActive && (
        <DirectoryFilterBar
          advanced={advanced}
          chips={statuses.size ? [{ id: "status", label: "Status", value: (["admin", "guest", "member"] as const).filter(id => statuses.has(id)).map(capitalize).join(", ") }] : []}
          groups={filterGroups}
          onAdvanced={() => setAdvanced(true)}
          onChoice={changeFilter}
          onClear={clearFilters}
          onRemoveAdvanced={() => setAdvanced(false)}
          onRemoveChip={() => setStatuses(new Set())}
          selected={{ status: statuses }}
        />
      )}
      {visible.length === 0 ? (
        statuses.size > 0 && !query ? (
          <DirectoryFilteredEmpty
            hiddenCount={entries.length}
            noun="members"
            onClear={clearFilters}
          />
        ) : (
          <div className="workspace-members-empty" role="status">
            <svg className="workspace-members-empty__mark" viewBox="0 0 80 80" aria-hidden="true">
              <ellipse cx="40" cy="40" rx="22" ry="34" fill="none" stroke="currentColor" strokeWidth="1.2" />
              <ellipse cx="40" cy="40" rx="34" ry="22" fill="none" stroke="currentColor" strokeWidth="1.2" />
              <circle cx="40" cy="40" r="34" fill="none" stroke="currentColor" strokeWidth="1.2" />
              <path d="M6 40h68M40 6v68" fill="none" stroke="currentColor" strokeWidth="1.2" />
            </svg>
            <strong>{t("No matching members")}</strong>
          </div>
        )
      ) : (
        <div
          className={`workspace-directory__table workspace-members-table${visible.length > DIRECTORY_VIRTUALIZATION_THRESHOLD ? " is-virtualized" : ""}`}
          style={{ "--member-columns": memberDirectoryColumns(columns, longestTeamKey, hasExtraTeams) } as React.CSSProperties}
        >
          <DirectoryRows header={<div className="workspace-members-columns">
            <DirectorySortHeader active={ordering === "name"} descending={descending} label="Name" onClick={() => changeSort("name")} />
            {columns.has("status") && <DirectorySortHeader active={ordering === "status"} descending={descending} label="Status" onClick={() => changeSort("status")} />}
            {columns.has("joined") && <DirectorySortHeader active={ordering === "joined"} descending={descending} label="Joined" onClick={() => changeSort("joined")} />}
            {columns.has("teams") && <span>{t("Teams")}</span>}
            <span>{t("Last seen")}</span>
          </div>} items={visible} itemKey={entry => entry.kind === "member" ? `member:${entry.member.user.id}` : `invitation:${entry.invitation.id}`} render={renderEntry}/>
        </div>
      )}
    </>
  );
}

function DirectorySortHeader({
  label,
  active,
  descending,
  onClick,
}: {
  label: string;
  active: boolean;
  descending: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="workspace-members-sort"
      onClick={onClick}
      aria-label={active ? `Order by ${label}, sorted ${descending ? "descending" : "ascending"}` : `Order by ${label}`}
    >
      {label}
      {active ? descending ? <ArrowUp aria-hidden="true" /> : <ArrowDown aria-hidden="true" /> : null}
    </button>
  );
}

function CustomersDirectory({
  featureSettings,
  customers,
  requests,
  users,
  onResultCount,
  onCreate,
  onUpdate,
  onDelete,
  onOpen,
}: {
  featureSettings: BootstrapData['workspaceSettings']['featureSettings'];
  customers: Customer[];
  requests: BootstrapData["customerRequests"];
  users: User[];
  onResultCount: (count: number | undefined) => void;
  onCreate: () => void;
  onUpdate: (customer: Customer, input: CustomerMutationInput) => Promise<void>;
  onDelete: (customer: Customer) => Promise<void>;
  onOpen: (customer: Customer) => void;
}) {
  const [query, setQuery] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [ownerIds, setOwnerIds] = useState<Set<string>>(new Set());
  const [statuses, setStatuses] = useState<Set<string>>(new Set());
  const [revenue, setRevenue] = useState<Set<string>>(new Set());
  const [size, setSize] = useState<Set<string>>(new Set());
  const [ordering, setOrdering] = useState<CustomerOrdering>("created");
  const [descending, setDescending] = useState(false);
  const [columns, setColumns] = useState<Set<CustomerColumn>>(
    new Set(["requests", "revenue", "size", "owner", "status", "tier"]),
  );
  const requestCounts = useMemo(() => requests.reduce((counts, request) => counts.set(request.customerId, (counts.get(request.customerId) ?? 0) + 1), new Map<string, number>()), [requests]);
  const usersById = useMemo(() => new Map(users.map(user => [user.id, user])), [users]);
  const filtersActive =
    advanced ||
    ownerIds.size > 0 ||
    statuses.size > 0 ||
    revenue.size > 0 ||
    size.size > 0;
  const visible = useMemo(() => customers
    .filter((customer) => {
      const minimumRevenue = Number([...revenue][0] ?? 0);
      const minimumSize = Number([...size][0] ?? 0);
      return (
        (!ownerIds.size || ownerIds.has(customer.ownerId ?? "")) &&
        (!statuses.size || statuses.has(customer.status)) &&
        (!minimumRevenue || (customer.annualRevenue ?? 0) >= minimumRevenue) &&
        (!minimumSize || (customer.size ?? 0) >= minimumSize) &&
        [customer.name, ...customer.domains]
          .join(" ")
          .toLowerCase()
          .includes(query.toLowerCase())
      );
    })
    .sort(
      (left, right) =>
        (ordering === "requests" ? (requestCounts.get(left.id) ?? 0) - (requestCounts.get(right.id) ?? 0) : compareCustomers(left, right, ordering)) * (descending ? -1 : 1),
    ), [customers, descending, ordering, ownerIds, query, requestCounts, revenue, size, statuses]);
  useEffect(() => {
    onResultCount(query || filtersActive ? visible.length : undefined);
    return () => onResultCount(undefined);
  }, [filtersActive, onResultCount, query, visible.length]);
  const filterGroups: DirectoryFilterGroup[] = [
    {
      id: "owner",
      label: "Owner",
      icon: <UserRound />,
      choices: users.map((user) => ({
        id: user.id,
        label: user.displayName,
        icon: <DirectoryUserAvatar user={user} />,
      })),
    },
    {
      id: "status",
      label: "Status",
      icon: <Circle />,
      choices: [
        { id: "active", label: "Active" },
        { id: "inactive", label: "Inactive" },
      ],
    },
    {
      id: "revenue",
      label: "Revenue",
      icon: <Banknote />,
      choices: [
        { id: "1000", label: "$1k+" },
        { id: "10000", label: "$10k+" },
        { id: "100000", label: "$100k+" },
        { id: "1000000", label: "$1m+" },
      ],
    },
    {
      id: "size",
      label: "Size",
      icon: <UsersRound />,
      choices: [
        { id: "10", label: "10+" },
        { id: "50", label: "50+" },
        { id: "100", label: "100+" },
        { id: "1000", label: "1,000+" },
      ],
    },
  ];
  const selectedFilters = { owner: ownerIds, status: statuses, revenue, size };
  const changeFilter = (
    groupId: string,
    choiceId: string,
    checked: boolean,
  ) => {
    const update = (
      setter: Dispatch<SetStateAction<Set<string>>>,
      single = false,
    ) =>
      setter((current) => {
        const next = single ? new Set<string>() : new Set(current);
        if (checked) next.add(choiceId);
        else next.delete(choiceId);
        return next;
      });
    if (groupId === "owner") update(setOwnerIds);
    if (groupId === "status") update(setStatuses);
    if (groupId === "revenue") update(setRevenue, true);
    if (groupId === "size") update(setSize, true);
  };
  const clearFilters = () => {
    setAdvanced(false);
    setOwnerIds(new Set());
    setStatuses(new Set());
    setRevenue(new Set());
    setSize(new Set());
  };
  const removeFilter = (id: string) => {
    if (id === "owner") setOwnerIds(new Set());
    if (id === "status") setStatuses(new Set());
    if (id === "revenue") setRevenue(new Set());
    if (id === "size") setSize(new Set());
  };
  const toggleColumn = (column: CustomerColumn) =>
    setColumns((current) => {
      const next = new Set(current);
      if (next.has(column)) next.delete(column);
      else next.add(column);
      return next;
    });
  const changeCustomerOrder = (next: CustomerOrdering) => {
    if (ordering === next) setDescending((current) => !current);
    else { setOrdering(next); setDescending(false); }
  };
  return (
    <>
      <div className="workspace-directory__toolbar workspace-customers-toolbar">
        <label>
          <Search />
          <input
            aria-label="Find by name or domain"
            placeholder="Find by name or domain…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          {query && (
            <button
              type="button"
              aria-label="Clear search"
              onClick={() => setQuery("")}
            >
              <X />
            </button>
          )}
        </label>
        <span />
        <DirectoryFilterMenu
          groups={filterGroups}
          onAdvanced={() => setAdvanced(true)}
          onChoice={changeFilter}
          selected={selectedFilters}
        />
        <DirectoryDisplayMenu<CustomerColumn, CustomerOrdering>
          descending={descending}
          onDirection={() => setDescending((value) => !value)}
          onOrdering={setOrdering}
          onProperty={toggleColumn}
          ordering={ordering}
          orderingOptions={[
            { id: "created", label: "Created" },
            { id: "updated", label: "Updated" },
            { id: "name", label: "Name" },
            { id: "revenue", label: "Annual revenue" },
            { id: "size", label: "Size" },
          ]}
          properties={columns}
          propertyOptions={[
            { id: "requests", label: "Requests" },
            { id: "revenue", label: "Annual revenue" },
            { id: "size", label: "Size" },
            { id: "owner", label: "Owner" },
            { id: "status", label: "Status" },
            { id: "tier", label: "Tier" },
            { id: "domains", label: "Domains" },
            { id: "source", label: "Data source" },
          ]}
        />
      </div>
      {filtersActive && (
        <DirectoryFilterBar
          advanced={advanced}
          chips={customerFilterChips(ownerIds, statuses, revenue, size, users)}
          groups={filterGroups}
          onAdvanced={() => setAdvanced(true)}
          onChoice={changeFilter}
          onClear={clearFilters}
          onRemoveAdvanced={() => setAdvanced(false)}
          onRemoveChip={removeFilter}
          selected={selectedFilters}
        />
      )}
      {customers.length === 0 ? (
        <CustomerEmpty onCreate={onCreate} />
      ) : visible.length === 0 ? (
        <DirectoryFilteredEmpty
          hiddenCount={customers.length}
          noun="customers"
          onClear={clearFilters}
        />
      ) : (
        <div
          className={`workspace-directory__table workspace-customers-table${visible.length > DIRECTORY_VIRTUALIZATION_THRESHOLD ? " is-virtualized" : ""}`}
          style={
            {
              "--customer-columns": customerColumns(columns),
            } as React.CSSProperties
          }
        >
      <DirectoryRows header={<div className="workspace-customer-columns">
            <button onClick={() => changeCustomerOrder("name")}>Name</button>
            {columns.has("requests") && <button onClick={() => changeCustomerOrder("requests")}>Requests</button>}
            {columns.has("revenue") && <button onClick={() => changeCustomerOrder("revenue")}>Annual revenue</button>}
            {columns.has("size") && <button onClick={() => changeCustomerOrder("size")}>Size</button>}
            {columns.has("status") && <button onClick={() => changeCustomerOrder("status")}>Status</button>}
            {columns.has("tier") && <button onClick={() => changeCustomerOrder("tier")}>Tier</button>}
            {columns.has("owner") && <span>Owner</span>}
            {columns.has("domains") && <span>Domains</span>}
            {columns.has("source") && <span>Data source</span>}
            <span />
          </div>} items={visible} itemKey={customer => customer.id} render={(customer) => (
            <div className="workspace-customer-row" key={customer.id} role="button" tabIndex={0} onClick={() => onOpen(customer)} onKeyDown={event => { if (event.key === 'Enter') onOpen(customer) }}>
              <div>
                <CustomerMark customer={customer} />
                <span>
                  <strong>{customer.name}</strong>
                  <small>{customer.domains[0] ?? "No domain"}</small>
                </span>
              </div>
              {columns.has("requests") && <span>{requestCounts.get(customer.id) ?? 0}</span>}
              {columns.has("revenue") && (
                <span>{formatCustomerRevenue(customer.annualRevenue,featureSettings)}</span>
              )}
              {columns.has("size") && <span>{customer.size ?? "—"}</span>}
              {columns.has("status") && (
                <button
                  className="workspace-customer-status"
                  type="button"
                  onClick={(event) => {
                    event.stopPropagation();
                    void onUpdate(customer, {
                      status:
                        customer.status === "active" ? "inactive" : "active",
                    });
                  }}
                >
                  <i data-active={customer.status === "active"} />
                  {capitalize(customer.status)}
                </button>
              )}
              {columns.has("tier") && <span>{customer.tier || "No tier"}</span>}
              {columns.has("owner") && (
                <span>
                  {usersById.get(customer.ownerId ?? "")?.displayName ?? "No owner"}
                </span>
              )}
              {columns.has("domains") && (
                <span>{customer.domains.join(", ") || "—"}</span>
              )}
              {columns.has("source") && <span>Manual</span>}
              <DropdownMenu.Root>
                <DropdownMenu.Trigger asChild>
                  <button
                    className="workspace-row-menu"
                    type="button"
                    aria-label={`Open ${customer.name} menu`}
                    onClick={event => event.stopPropagation()}
                  >
                    <MoreHorizontal />
                  </button>
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <DropdownMenu.Content data-flow-motion="floating"
                    className="workspace-directory__menu"
                    align="end"
                  >
                    <DropdownMenu.Item
                      className="workspace-directory__danger"
                      onSelect={() => void onDelete(customer)}
                    >
                      <Trash2 />
                      Delete customer
                    </DropdownMenu.Item>
                  </DropdownMenu.Content>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            </div>
          )}/>
        </div>
      )}
    </>
  );
}

function CustomerEmpty({ onCreate }: { onCreate: () => void }) {
  return (
    <div className="workspace-customer-empty">
      <CustomerEmptyArt />
      <h2>Customers</h2>
      <p>
        Add organizations using your product to track their
        <br />
        feature requests and use attributes like revenue and
        <br />
        size to prioritize development.
      </p>
      <div>
        <button type="button" onClick={onCreate}>
          Create new customer
        </button>
        <a
          href="https://flow.app/docs/customer-requests"
          target="_blank"
          rel="noreferrer"
        >
          Documentation
        </a>
      </div>
    </div>
  );
}

function CustomerEmptyArt() {
  return (
    <div className="workspace-customer-art" aria-hidden="true">
      <i />
      <i />
      <i />
      <i>
        <span>ℓ</span>
      </i>
    </div>
  );
}

function TeamsDirectory({
  data,
  onMembers,
  onOpen,
  onProjects,
  onCycles,
  onReload,
}: {
  data: BootstrapData;
  onMembers: (team: Team) => void;
  onOpen: (team: Team) => void;
  onProjects: (team: Team) => void;
  onCycles: (team: Team) => void;
  /** Refreshes workspace metadata; a team id also refetches that team's issues (membership changed). */
  onReload: (teamId?: string) => Promise<void>;
}) {
  const {t}=useI18n();
  const { filters, setFilters, preferences, setPreferences } = useTeamDirectoryControls(data.workspace.id, data.viewer.id);
  const hierarchy = useMemo(() => teamHierarchy(data.teams, data.teamSettings), [data.teams, data.teamSettings]);
  const [collapsedTeams, setCollapsedTeams] = useState<Set<string>>(new Set());
  const { descending, ordering } = preferences;
  const columns = new Set(preferences.columns);
  const [dateOpen, setDateOpen] = useState(false);
  const [query, setQuery] = useState("");
  const people = useMemo(() => indexTeamPeople(data.teamMembers), [data.teamMembers]);
  const setDescending = (update: boolean | ((value: boolean) => boolean)) => setPreferences(current => ({ ...current, descending: typeof update === 'function' ? update(current.descending) : update }));
  const setOrdering = (value: TeamOrdering) => setPreferences(current => ({ ...current, ordering: value, descending: value !== 'name' }));
  const [nameSortChosen, setNameSortChosen] = useState(false);
  const nameSorted = ordering === "name" && (nameSortChosen || descending);
  const teamMetrics = useMemo(() => {
    const usersById = new Map(data.users.map(user => [user.id, user]));
    const metrics = new Map(data.teams.map(team => [team.id, {
      memberIds: new Set<string>(),
      users: [] as User[],
      owners: [] as User[],
      viewerMember: false,
      cycleCount: 0,
      projectCount: 0,
      favorite: false,
      subscribed: false,
    }]));
    for (const membership of data.teamMembers) {
      const metric = metrics.get(membership.teamId);
      if (!metric) continue;
      metric.memberIds.add(membership.userId);
      const user = usersById.get(membership.userId);
      if (user) metric.users.push(user);
      if (membership.role === "owner" && user) metric.owners.push(user);
      if (membership.userId === data.viewer.id) metric.viewerMember = true;
    }
    for (const cycle of data.cycles) if (cycle.status === "current") {
      const metric = metrics.get(cycle.teamId);
      if (metric) metric.cycleCount = cycle.number;
    }
    for (const project of data.projects) if (project.status.type !== "completed" && project.status.type !== "canceled") {
      for (const teamId of project.teamIds) {
        const metric = metrics.get(teamId);
        if (metric) metric.projectCount += 1;
      }
    }
    for (const favorite of data.favorites) if (favorite.userId === data.viewer.id && favorite.resourceType === "team") {
      const metric = metrics.get(favorite.resourceId);
      if (metric) metric.favorite = true;
    }
    for (const subscription of data.subscriptions) if (subscription.userId === data.viewer.id && subscription.resourceType === "team") {
      const metric = metrics.get(subscription.resourceId);
      if (metric) metric.subscribed = true;
    }
    return metrics;
  }, [data.cycles, data.favorites, data.projects, data.subscriptions, data.teamMembers, data.teams, data.users, data.viewer.id]);
  const filtersActive = filters.advanced || ['members', 'owners', 'private', 'created'].some(field => filters[field as TeamFilterField].length);
  const teams = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return data.teams
      .filter(team => !needle || team.name.toLocaleLowerCase().includes(needle) || team.key.toLocaleLowerCase().includes(needle))
      .filter(team => matchesTeamFilters(team, filters, people))
      .sort((left, right) => compareDirectoryTeams(left, right, ordering, descending));
  }, [data.teams, descending, filters, ordering, people, query]);
  const toggleColumn = (column: TeamColumn) =>
    setPreferences((current) => {
      const next = new Set(current.columns);
      if (next.has(column)) next.delete(column);
      else next.add(column);
      return { ...current, columns: [...next] };
    });
  const teamRows = useMemo(() => hierarchy.rows(teams, collapsedTeams), [hierarchy, teams, collapsedTeams]);
  // Only reserve the expand-arrow slot when some team has sub-teams (Linear
  // aligns team icons under the Name header otherwise).
  const anyTeamHasChildren = teamRows.some(row => row.hasChildren);
  const counts = useMemo(() => {
    const result = { members: new Map<string, number>(), owners: new Map<string, number>() };
    for (const team of data.teams) {
      if (team.retiredAt) continue;
      for (const field of ['members', 'owners'] as const) {
        for (const id of people.get(team.id)?.[field] ?? []) result[field].set(id, (result[field].get(id) ?? 0) + 1);
      }
    }
    return result;
  }, [data.teams, people]);
  const countLabel = (count: number) => `${count} ${count === 1 ? 'team' : 'teams'}`;
  const filterGroups: DirectoryFilterGroup[] = [
    {
      id: "members",
      label: "Members",
      icon: <DirectoryPeopleIcon />,
      choices: data.users.map((user) => ({
        id: user.id,
        label: user.displayName,
        meta: countLabel(counts.members.get(user.id) ?? 0),
        keywords: personSearchText(user),
        person: user,
        icon: <DirectoryUserAvatar user={user} />,
      })),
    },
    {
      id: "owners",
      label: "Owners",
      icon: <DirectoryPeopleIcon />,
      choices: data.users.map((user) => ({
        id: user.id,
        label: user.displayName,
        meta: countLabel(counts.owners.get(user.id) ?? 0),
        keywords: personSearchText(user),
        person: user,
        icon: <DirectoryUserAvatar user={user} />,
      })),
    },
    {
      id: "private",
      label: "Private",
      icon: <DirectoryLockIcon />,
      separatorBefore: true,
    },
    {
      id: "created",
      label: "Created date",
      icon: <DirectoryCreatedDateIcon />,
      selectionMode: "single",
      choices: [
        ...teamDateChoices.map(choice => ({ ...choice, meta: countLabel(data.teams.filter(team => !team.retiredAt && matchesTeamDate(team, choice.id)).length) })),
        { id: "custom", label: "Custom date or timeframe…" },
      ],
    },
  ];
  const selectedFilters = Object.fromEntries(filterGroups.map(group => [group.id, new Set(filters[group.id as TeamFilterField])]));
  const changeFilter = (
    groupId: string,
    choiceId: string,
    checked: boolean,
  ) => {
    if (choiceId === 'custom') { setDateOpen(true); return; }
    const field = groupId as TeamFilterField;
    setFilters(current => {
      const next = new Set(field === 'created' || field === 'private' ? [] : current[field]);
      if (checked) next.add(choiceId); else next.delete(choiceId);
      return { ...current, [field]: [...next] };
    });
  };
  const clearFilters = () => {
    setFilters({ members: [], owners: [], private: [], created: [], operators: {}, conjunction: 'and', advanced: false });
  };
  return (
    <>
      <div className="workspace-directory__toolbar workspace-teams-toolbar">
        <ContentViewHeaderSearch
          alwaysVisible
          aria-label={t("Find teams")}
          placeholder={t("Find teams…")}
          value={query}
          onChange={setQuery}
          className="workspace-teams-cvh-search"
        />
        <span />
        <DirectoryFilterMenu
          groups={filterGroups}
          onAdvanced={() => setFilters(current => ({ ...current, advanced: true }))}
          onChoice={changeFilter}
          onDirect={field => changeFilter(field, 'true', true)}
          selected={selectedFilters}
        />
        <DirectoryDisplayMenu<TeamColumn, TeamOrdering>
          descending={descending}
          onDirection={() => setDescending((value) => !value)}
          onOrdering={setOrdering}
          onProperty={toggleColumn}
          ordering={ordering}
          orderingOptions={[
            { id: "name", label: "Name" },
            { id: "created", label: "Created" },
            { id: "updated", label: "Updated" },
          ]}
          properties={columns}
          propertyOptions={[
            { id: "id", label: "ID" },
            { id: "membership", label: "Membership" },
            { id: "owners", label: "Owners" },
            { id: "projects", label: "Projects" },
            { id: "created", label: "Created" },
            { id: "updated", label: "Updated" },
            { id: "members", label: "Members" },
            { id: "cycle", label: "Cycle" },
          ]}
        />
      </div>
      {filtersActive && (
        <TeamFilterBar filters={filters} groups={filterGroups} onChange={setFilters} onChoice={changeFilter} onDate={() => setDateOpen(true)}/>
      )}
      <TeamDateFilterDialog open={dateOpen} value={filters.created[0]} onClose={() => setDateOpen(false)} onApply={value => { changeFilter('created', value, true); setDateOpen(false); }}/>
      {teams.length === 0 && query.trim() && !filtersActive ? (
        <div className="workspace-members-empty" role="status">
          <strong>{t("No matching teams")}</strong>
        </div>
      ) : teams.length === 0 ? (
        <DirectoryFilteredEmpty
          hiddenCount={data.teams.length}
          noun="teams"
          onClear={clearFilters}
        />
      ) : (
        <div
          className={`workspace-directory__table workspace-teams-table${teams.length > DIRECTORY_VIRTUALIZATION_THRESHOLD ? " is-virtualized" : ""}`}
          style={
            { "--team-columns": teamColumns(columns) } as React.CSSProperties
          }
        >
      <DirectoryRows header={<div className="workspace-team-columns">
            <button
              aria-label={nameSorted ? `Order by Name, sorted ${descending ? "descending" : "ascending"}` : "Order by Name"}
              className={nameSorted ? "is-sorted" : undefined}
              type="button"
              onClick={() => {
                setNameSortChosen(true);
                if (ordering === "name") setDescending((value) => !value);
                else { setOrdering("name"); setDescending(false); }
              }}
            >
              {/* Like Linear, the default A–Z order shows its arrow only on hover; a chosen order keeps it. */}
              {t('Name')}{ordering === 'name' ? (descending ? <ArrowUp aria-hidden="true" /> : <ArrowDown aria-hidden="true" />) : <ArrowDown aria-hidden="true" />}
            </button>
            <span>{t('Description')}</span>
            {columns.has("membership") && <span>{t('Membership')}</span>}
            {columns.has("owners") && <span>Owners</span>}
            {columns.has("members") && <span>{t('Members')}</span>}
            {columns.has("cycle") && <span>{t('Cycle')}</span>}
            {columns.has("projects") && <span>{t('Active projects')}</span>}
            {(['created', 'updated'] as const).map(field => columns.has(field) && <button key={field} aria-label={`Order by ${field === 'created' ? 'Created' : 'Updated'}`} className={ordering === field ? "is-sorted" : undefined} type="button" onClick={() => { if (ordering === field) setDescending(value => !value); else setOrdering(field); }}>{field === 'created' ? 'Created' : 'Updated'}{ordering === field && (descending ? <ArrowUp aria-hidden="true"/> : <ArrowDown aria-hidden="true"/>)}</button>)}
            <span />
          </div>} items={teamRows} itemKey={row => row.team.id} render={({team, depth, hasChildren}) => {
            const metric = teamMetrics.get(team.id);
            const viewerMembership = metric?.viewerMember;
            const teamUsers = metric?.users ?? [];
            const owners = metric?.owners ?? [];
            const cycleCount = metric?.cycleCount ?? 0;
            const projectCount = metric?.projectCount ?? 0;
            const description = data.teamSettings?.[team.id]?.description?.trim();
            return (
              <div
                className="workspace-team-row"
                key={team.id}
                role="link"
                tabIndex={0}
                onClick={() => onOpen(team)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") onOpen(team);
                }}
              >
                <div className="workspace-team-identity" style={{paddingInlineStart: depth * 18}} title={hierarchy.path(team.id)}>
                  {hasChildren ? <button className="workspace-team-expand" aria-label={`${collapsedTeams.has(team.id) ? 'Expand' : 'Collapse'} ${team.name}`} aria-expanded={!collapsedTeams.has(team.id)} onKeyDown={event => event.stopPropagation()} onClick={event => {event.stopPropagation(); setCollapsedTeams(current => {const next = new Set(current); if(next.has(team.id))next.delete(team.id);else next.add(team.id);return next;});}}><ChevronRight size={14} style={{transform: collapsedTeams.has(team.id) ? undefined : 'rotate(90deg)'}}/></button> : anyTeamHasChildren && <span className="workspace-team-expand-placeholder"/>}
                  <TeamGlyph team={team} />
                  <strong>{team.name}</strong>
                  {columns.has("id") && <small>{team.key}</small>}
                </div>
                <span className="workspace-team-description" title={description || undefined}>{description}</span>
                {columns.has("membership") && (
                  viewerMembership?<span className="workspace-team-joined"><Check/>{t('Joined')}</span>:<button className="workspace-team-joined" onClick={event=>{event.stopPropagation();void setTeamMembership(data.workspace.urlKey,team.id,data.viewer.id,true,'member').then(()=>onReload(team.id))}}>{t('Join')}</button>
                )}
                {columns.has("owners") && (
                  <span className="workspace-team-owner">
                    {owners.map(owner => <DirectoryUserAvatar key={owner.id} user={owner} />)}
                    {owners.map(owner => owner.displayName).join(', ') || 'No owner'}
                  </span>
                )}
                {columns.has("members") && (
                  <button
                    className="workspace-team-members"
                    type="button"
                    aria-label="Open team member page"
                    onClick={(event) => {
                      event.stopPropagation();
                      onMembers(team);
                    }}
                  >
                    {teamUsers.slice(0, 3).map((user) => (
                      <DirectoryUserAvatar key={user.id} user={user} />
                    ))}
                  </button>
                )}
                {columns.has("cycle") && <button className="workspace-team-projects" type="button" aria-label={`Open Cycle ${cycleCount} ${cycleCount===1?'cycle':'cycles'}`} onClick={event=>{event.stopPropagation();onCycles(team)}}><Circle/>{cycleCount}</button>}
                {columns.has("projects") && (
                  <button
                    className="workspace-team-projects"
                    type="button"
                    aria-label="Open team projects page"
                    onClick={(event) => {
                      event.stopPropagation();
                      onProjects(team);
                    }}
                  >
                    <FlowProjectIcon />
                    {projectCount}
                  </button>
                )}
                {columns.has("created") && (
                  <time dateTime={team.createdAt}>{formatTeamDate(team, 'created')}</time>
                )}
                {columns.has("updated") && (
                  <time dateTime={team.updatedAt}>{formatTeamDate(team, 'updated')}</time>
                )}
                <TeamRowMenu
                  data={data}
                  team={team}
                  workspaceKey={data.workspace.urlKey}
                  favorite={Boolean(metric?.favorite)}
                  subscribed={Boolean(metric?.subscribed)}
                />
              </div>
            );
          }}/>
        </div>
      )}
    </>
  );
}

function TeamRowMenu({
  data,
  team,
  workspaceKey,
  favorite,
  subscribed,
}: {
  data: BootstrapData;
  team: Team;
  workspaceKey:string;
  favorite:boolean;
  subscribed:boolean;
}) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          className="workspace-row-menu"
          type="button"
          aria-label="Open menu"
          onClick={(event) => event.stopPropagation()}
        >
          <MoreHorizontal />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating" className="workspace-directory__menu" align="end">
          <DropdownMenu.Item onSelect={()=>void toggleFavoriteFor(data,'team',team.id,undefined,favorite)}>{favorite?'Unfavorite':'Favorite'}</DropdownMenu.Item>
          <DropdownMenu.Separator />
          <DropdownMenu.Item asChild><AppLink href={`/${encodeURIComponent(workspaceKey)}/settings/teams/${encodeURIComponent(team.key)}`}>Team settings</AppLink></DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() =>
              void navigator.clipboard.writeText(
                `${window.location.origin}${window.location.pathname.replace(/\/teams$/, `/team/${team.key}/overview`)}`,
              )
            }
          >
            Copy URL
          </DropdownMenu.Item>
          <DropdownMenu.Item asChild><AppLink href={`/${encodeURIComponent(workspaceKey)}/team/${encodeURIComponent(team.key)}/archive/issues`}>Open archive</AppLink></DropdownMenu.Item>
          <DropdownMenu.Separator />
          <DropdownMenu.Item onSelect={()=>void (subscribed?removeSubscription('team',team.id):addSubscription('team',team.id)).then(()=>refreshResourcePreferences())}>{subscribed?'Unsubscribe':'Subscribe'}</DropdownMenu.Item>
          <DropdownMenu.Item asChild><AppLink href={`/${encodeURIComponent(workspaceKey)}/settings/teams/${encodeURIComponent(team.key)}/notifications`}>Configure Slack notifications…</AppLink></DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

interface DirectoryFilterChip {
  id: string;
  label: string;
  value: string;
}

function DirectoryFilterBar({
  advanced,
  chips,
  groups,
  selected,
  onAdvanced,
  onChoice,
  onDirect,
  onRemoveAdvanced,
  onRemoveChip,
  onClear,
}: {
  advanced: boolean;
  chips: DirectoryFilterChip[];
  groups: DirectoryFilterGroup[];
  selected: Record<string, Set<string>>;
  onAdvanced: () => void;
  onChoice: (groupId: string, choiceId: string, checked: boolean) => void;
  onDirect?: (groupId: string) => void;
  onRemoveAdvanced: () => void;
  onRemoveChip: (id: string) => void;
  onClear: () => void;
}) {
  return (
    <>
      <div className="workspace-filter-bar">
        {advanced && (
          <span className="workspace-filter-chip is-advanced">
            Advanced filter
            <button
              aria-label="Remove advanced filter"
              onClick={onRemoveAdvanced}
              type="button"
            >
              <X />
            </button>
          </span>
        )}
        {chips.map((chip) => (
          <span className="workspace-filter-chip" key={chip.id}>
            <strong>{chip.label}</strong>
            <em>{chip.id === "created" ? "after" : "is"}</em>
            <span>{chip.value}</span>
            <button
              aria-label={`Remove ${chip.label} filter`}
              onClick={() => onRemoveChip(chip.id)}
              type="button"
            >
              <X />
            </button>
          </span>
        ))}
        {!advanced && <DirectoryFilterMenu
          groups={groups}
          onAdvanced={onAdvanced}
          onChoice={onChoice}
          onDirect={onDirect}
          selected={selected}
          trigger="add"
        />}
        <button
          className="workspace-filter-bar__clear"
          disabled={!advanced && chips.length === 0}
          onClick={onClear}
          type="button"
        >
          Clear
        </button>
      </div>
      {advanced && (
        <div className="workspace-advanced-filter-row">
          <DirectoryFilterMenu groups={groups} onAdvanced={onAdvanced} onChoice={onChoice} onDirect={onDirect} selected={selected} trigger="advanced"/>
        </div>
      )}
    </>
  );
}

function DirectoryFilteredEmpty({
  hiddenCount,
  noun,
  onClear,
}: {
  hiddenCount: number;
  noun: string;
  onClear: () => void;
}) {
  return (
    <div className="workspace-directory-filtered-empty">
      <img
        alt=""
        className="workspace-directory-filtered-empty__art"
        aria-hidden="true"
        src="/flow-filter-empty.svg"
      />
      <h2>{`No ${noun} matching the filters`}</h2>
      <div className="workspace-directory-filtered-empty__notice">
        <span>
          {hiddenCount} {hiddenCount === 1 ? noun.replace(/s$/, "") : noun}{" "}
          <small>hidden by filters</small>
        </span>
        <button onClick={onClear} type="button">
          Clear Filters
        </button>
        <button aria-label="Clear filters" onClick={onClear} type="button">
          <X />
        </button>
      </div>
    </div>
  );
}

function DirectoryUserAvatar({ user }: { user: User }) {
  return <UserAvatar avatarUrl={user.avatarUrl} className="workspace-directory-avatar" color={avatarColor(user.id)} name={user.displayName} title={user.email}/>;
}
function CustomerMark({ customer }: { customer: Customer }) {
  return customer.logoUrl ? (
    <img className="workspace-customer-mark" src={customer.logoUrl} alt="" />
  ) : (
    <span className="workspace-customer-mark">{initials(customer.name)}</span>
  );
}
function TeamGlyph({ team }: { team: Team }) {
  return <ViewGlyph className="workspace-team-glyph" color={team.color} icon={team.icon || "Team"} />;
}
function FlowProjectIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <use href="#Project" />
    </svg>
  );
}
function initials(value: string) {
  return value
    .split(/\s+/)
    .map((part) => part[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
}
function avatarColor(value: string) {
  const colors = ["#e56663", "#4f8d78", "#c09a43", "#6874d7", "#9b65a5"];
  return colors[
    [...value].reduce((sum, char) => sum + char.charCodeAt(0), 0) %
      colors.length
  ];
}
function capitalize(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}
function teamColumns(columns: Set<TeamColumn>) {
  const widths: Record<Exclude<TeamColumn, "id">, string> = {
    membership: "96px",
    owners: "150px",
    projects: "153px",
    cycle: "90px",
    created: "105px",
    updated: "105px",
    members: "126px",
  };
  // Linear splits the flexible space ~41/59 between Name and Description (x13 / x345 in a 1218px panel).
  return `minmax(220px,327fr) minmax(0,463fr) ${(
    [
      "membership",
      "owners",
      "members",
      "cycle",
      "projects",
      "created",
      "updated",
    ] as Exclude<TeamColumn, "id">[]
  )
    .filter((column) => columns.has(column))
    .map((column) => widths[column])
    .join(" ")} 22px`;
}

function customerColumns(columns: Set<CustomerColumn>) {
  const widths: Record<CustomerColumn, string> = {
    requests: "88px",
    revenue: "125px",
    size: "80px",
    owner: "145px",
    status: "105px",
    tier: "105px",
    domains: "180px",
    source: "110px",
  };
  return `minmax(250px,1fr) ${(
    [
      "requests",
      "revenue",
      "size",
      "status",
      "tier",
      "owner",
      "domains",
      "source",
    ] as CustomerColumn[]
  )
    .filter((column) => columns.has(column))
    .map((column) => widths[column])
    .join(" ")} 38px`;
}

function compareCustomers(
  left: Customer,
  right: Customer,
  ordering: CustomerOrdering,
) {
  if (ordering === "name") return left.name.localeCompare(right.name);
  if (ordering === "status") return left.status.localeCompare(right.status);
  if (ordering === "tier") return (left.tier ?? "").localeCompare(right.tier ?? "");
  if (ordering === "revenue")
    return (left.annualRevenue ?? 0) - (right.annualRevenue ?? 0);
  if (ordering === "size") return (left.size ?? 0) - (right.size ?? 0);
  return (
    new Date(
      ordering === "created" ? left.createdAt : left.updatedAt,
    ).getTime() -
    new Date(
      ordering === "created" ? right.createdAt : right.updatedAt,
    ).getTime()
  );
}

function formatTeamDate(team: Team, field: 'created' | 'updated') {
  const timestamp = teamTimestamp(team, field);
  return timestamp === undefined ? '-' : formatDirectoryDate(new Date(timestamp));
}

function formatDirectoryDate(value: Date) {
  return value.toLocaleDateString("en", { month: "short", day: "numeric" });
}

function namesFor(ids: Set<string>, users: User[]) {
  return [...ids]
    .map((id) => users.find((user) => user.id === id)?.displayName ?? id)
    .join(", ");
}

function customerFilterChips(
  ownerIds: Set<string>,
  statuses: Set<string>,
  revenue: Set<string>,
  size: Set<string>,
  users: User[],
): DirectoryFilterChip[] {
  const chips: DirectoryFilterChip[] = [];
  if (ownerIds.size)
    chips.push({
      id: "owner",
      label: "Owner",
      value: namesFor(ownerIds, users),
    });
  if (statuses.size)
    chips.push({
      id: "status",
      label: "Status",
      value: [...statuses].map(capitalize).join(", "),
    });
  if (revenue.size)
    chips.push({
      id: "revenue",
      label: "Revenue",
      value: `$${Number([...revenue][0]).toLocaleString()}+`,
    });
  if (size.size)
    chips.push({
      id: "size",
      label: "Size",
      value: `${Number([...size][0]).toLocaleString()}+`,
    });
  return chips;
}
