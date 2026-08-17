import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  ArrowDown,
  ArrowUp,
  Banknote,
  Check,
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
  useEffect,
  useMemo,
  useState,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";
import { toast } from "sonner";
import { inviteMembers } from "@/lib/api";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { CustomerDialog } from "@/components/customer-detail/customer-dialog";
import type {
  BootstrapData,
  Customer,
  CustomerMutationInput,
  Team,
  User,
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

type DirectoryKind = "members" | "customers" | "teams";
type TeamColumn =
  "membership" | "owners" | "projects" | "created" | "updated" | "members";
type TeamOrdering = "name" | "updated" | "created";
type CustomerColumn =
  | "requests"
  | "revenue"
  | "size"
  | "owner"
  | "status"
  | "tier"
  | "domains"
  | "source";
type CustomerOrdering = "created" | "updated" | "name" | "revenue" | "size";

export function WorkspaceDirectoryPage({
  kind,
  data,
  inviteOnOpen = false,
  customerOnOpen = false,
  onOpenSidebar,
  onNavigateMembers,
  onNavigateTeam,
  onNavigateTeamProjects,
  onNewTeam,
  onCreateCustomer,
  onUpdateCustomer,
  onDeleteCustomer,
  onOpenCustomer,
  onUpdateTeam,
  onDeleteTeam,
  onReload,
}: {
  kind: DirectoryKind;
  data: BootstrapData;
  inviteOnOpen?: boolean;
  customerOnOpen?: boolean;
  onOpenSidebar: () => void;
  onNavigateMembers: () => void;
  onNavigateTeam: (team: Team) => void;
  onNavigateTeamProjects: (team: Team) => void;
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
  onUpdateTeam: (team: Team, input: Partial<Team>) => Promise<void>;
  onDeleteTeam: (team: Team) => Promise<void>;
  onReload: () => Promise<void>;
}) {
  const [inviteOpen, setInviteOpen] = useState(inviteOnOpen);
  const [customerOpen, setCustomerOpen] = useState(customerOnOpen);
  const [customerResultCount, setCustomerResultCount] = useState<number>();
  const title =
    kind === "members"
      ? "Members"
      : kind === "customers"
        ? "Customers"
        : "Teams";
  return (
    <main className="main-panel workspace-directory" aria-label={title}>
      <DirectoryHeader
        title={title}
        count={
          kind === "members"
            ? data.members.length
            : kind === "customers"
              ? customerResultCount
              : undefined
        }
        onOpenSidebar={onOpenSidebar}
        onCreate={kind === "members" && data.viewerRole !== "admin" ? undefined : () =>
          kind === "members"
            ? setInviteOpen(true)
            : kind === "customers"
              ? setCustomerOpen(true)
              : onNewTeam()
        }
        createLabel={
          kind === "members"
            ? "Invite members"
            : kind === "customers"
              ? "New customer"
              : "Create new team"
        }
        options={kind === "teams" ? <TeamsOptions /> : undefined}
      />
      {kind === "members" && <MembersDirectory data={data} />}
      {kind === "customers" && (
        <CustomersDirectory
          customers={data.customers ?? []}
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
          data={data}
          onMembers={onNavigateMembers}
          onOpen={onNavigateTeam}
          onProjects={onNavigateTeamProjects}
          onUpdate={onUpdateTeam}
          onDelete={onDeleteTeam}
        />
      )}
      <InviteMembersDialog
        open={inviteOpen}
        workspace={data.workspace}
        teams={data.teams}
        onOpenChange={setInviteOpen}
        onSent={onReload}
      />
      <CustomerDialog
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
  onCreate,
  onOpenSidebar,
  options,
}: {
  title: string;
  count?: number;
  createLabel: string;
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
        onClick={onOpenSidebar}
      >
        <span />
        <span />
        <span />
      </button>}
      <h1>{title}</h1>
      {count !== undefined && <small>{count}</small>}
      {options}
      <button
        className="workspace-directory__icon-button workspace-directory__create"
        type="button"
        aria-label={createLabel}
        title={createLabel}
        onClick={onCreate}
      >
        <Plus />
      </button>
    </header>
  );
}

function TeamsOptions() {
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
        <DropdownMenu.Content
          className="workspace-directory__menu"
          align="start"
          sideOffset={6}
        >
          <DropdownMenu.Item
            onSelect={() =>
              toast.info("Team settings are available from each team menu.")
            }
          >
            <Settings2 />
            <span>Go to teams settings</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function MembersDirectory({ data }: { data: BootstrapData }) {
  const [sort, setSort] = useState<"name" | "status" | "joined">("name");
  const [direction, setDirection] = useState<1 | -1>(1);
  const members = useMemo(
    () =>
      [...data.members].sort((left, right) => {
        const leftValue =
          sort === "name"
            ? left.user.displayName
            : sort === "status"
              ? left.role
              : left.joinedAt;
        const rightValue =
          sort === "name"
            ? right.user.displayName
            : sort === "status"
              ? right.role
              : right.joinedAt;
        return leftValue.localeCompare(rightValue) * direction;
      }),
    [data.members, direction, sort],
  );
  const changeSort = (next: typeof sort) => {
    if (sort === next) setDirection((value) => (value === 1 ? -1 : 1));
    else {
      setSort(next);
      setDirection(1);
    }
  };
  return (
    <div className="workspace-directory__table workspace-members-table">
      <div className="workspace-members-columns">
        <SortHeader
          active={sort === "name"}
          direction={direction}
          label="Name"
          onClick={() => changeSort("name")}
        />
        <SortHeader
          active={sort === "status"}
          direction={direction}
          label="Status"
          onClick={() => changeSort("status")}
        />
        <SortHeader
          active={sort === "joined"}
          direction={direction}
          label="Joined"
          onClick={() => changeSort("joined")}
        />
        <span>Teams</span>
        <span>Last seen</span>
      </div>
      {members.map((member) => {
        const user = member.user;
        const teams = data.teamMembers.filter(value => value.userId === user.id).map(value => data.teams.find(team => team.id === value.teamId)).filter((team): team is Team => Boolean(team));
        return (
        <div
          className="workspace-directory-member-row"
          key={user.id}
          role="button"
          tabIndex={0}
          onClick={() => toast.info(`${user.displayName}'s profile`)}
          onKeyDown={(event) => {
            if (event.key === "Enter")
              toast.info(`${user.displayName}'s profile`);
          }}
        >
          <div className="workspace-member-identity">
            <Avatar user={user} />
            <span>
              <strong>{user.displayName}</strong>
              <small>{user.name || user.email.split("@")[0]}</small>
            </span>
          </div>
          <span
            className={
              member.role === "admin"
                ? "workspace-member-role"
                : ""
            }
          >
            {member.role[0].toUpperCase() + member.role.slice(1)}
          </span>
          <time title="Joined workspace">{new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(new Date(member.joinedAt))}</time>
          <div className="workspace-member-teams">
            {member.status === "active" && teams[0] ? (
              <button
                type="button"
                onClick={(event) => event.stopPropagation()}
              >
                <TeamGlyph color={teams[0].color} />
                {teams[0].key}{teams.length > 1 ? ` +${teams.length - 1}` : ""}
              </button>
            ) : null}
          </div>
          <span className="workspace-member-last-seen">
            {user.id === data.viewer.id ? (
              <>
                <i />
                Online
              </>
            ) : (
              member.status === "suspended" ? "Suspended" : member.lastSeenAt ? new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric" }).format(new Date(member.lastSeenAt)) : "Never"
            )}
          </span>
        </div>
      )})}
    </div>
  );
}

function SortHeader({
  label,
  active,
  direction,
  onClick,
}: {
  label: string;
  active: boolean;
  direction: 1 | -1;
  onClick: () => void;
}) {
  return (
    <button type="button" onClick={onClick} aria-label={`Order by ${label}`}>
      {label}
      {active ? direction === 1 ? <ArrowDown /> : <ArrowUp /> : null}
    </button>
  );
}

function CustomersDirectory({
  customers,
  users,
  onResultCount,
  onCreate,
  onUpdate,
  onDelete,
  onOpen,
}: {
  customers: Customer[];
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
  const filtersActive =
    advanced ||
    ownerIds.size > 0 ||
    statuses.size > 0 ||
    revenue.size > 0 ||
    size.size > 0;
  const visible = customers
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
        compareCustomers(left, right, ordering) * (descending ? -1 : 1),
    );
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
        icon: <Avatar user={user} />,
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
          onDirect={() => undefined}
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
          onDirect={() => undefined}
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
          className="workspace-directory__table workspace-customers-table"
          style={
            {
              "--customer-columns": customerColumns(columns),
            } as React.CSSProperties
          }
        >
          <div className="workspace-customer-columns">
            <span>Name</span>
            {columns.has("requests") && <span>Requests</span>}
            {columns.has("revenue") && <span>Annual revenue</span>}
            {columns.has("size") && <span>Size</span>}
            {columns.has("owner") && <span>Owner</span>}
            {columns.has("status") && <span>Status</span>}
            {columns.has("tier") && <span>Tier</span>}
            {columns.has("domains") && <span>Domains</span>}
            {columns.has("source") && <span>Data source</span>}
            <span />
          </div>
          {visible.map((customer) => (
            <div className="workspace-customer-row" key={customer.id} role="button" tabIndex={0} onClick={() => onOpen(customer)} onKeyDown={event => { if (event.key === 'Enter') onOpen(customer) }}>
              <div>
                <CustomerMark customer={customer} />
                <span>
                  <strong>{customer.name}</strong>
                  <small>{customer.domains[0] ?? "No domain"}</small>
                </span>
              </div>
              {columns.has("requests") && <span>0</span>}
              {columns.has("revenue") && (
                <span>{formatRevenue(customer.annualRevenue)}</span>
              )}
              {columns.has("size") && <span>{customer.size ?? "—"}</span>}
              {columns.has("owner") && (
                <span>
                  {users.find((user) => user.id === customer.ownerId)
                    ?.displayName ?? "No owner"}
                </span>
              )}
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
                  <DropdownMenu.Content
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
          ))}
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
  onUpdate,
  onDelete,
}: {
  data: BootstrapData;
  onMembers: () => void;
  onOpen: (team: Team) => void;
  onProjects: (team: Team) => void;
  onUpdate: (team: Team, input: Partial<Team>) => Promise<void>;
  onDelete: (team: Team) => Promise<void>;
}) {
  const [descending, setDescending] = useState(false);
  const [ordering, setOrdering] = useState<TeamOrdering>("name");
  const [advanced, setAdvanced] = useState(false);
  const [memberIds, setMemberIds] = useState<Set<string>>(new Set());
  const [ownerIds, setOwnerIds] = useState<Set<string>>(new Set());
  const [privateOnly, setPrivateOnly] = useState(false);
  const [createdWindow, setCreatedWindow] = useState<Set<string>>(new Set());
  const [columns, setColumns] = useState<Set<TeamColumn>>(
    new Set(["membership", "members", "projects"]),
  );
  const filtersActive =
    advanced ||
    memberIds.size > 0 ||
    ownerIds.size > 0 ||
    privateOnly ||
    createdWindow.size > 0;
  const teams = data.teams
    .filter((team) => {
      const teamMemberIds = new Set(
        data.users.filter((user) => user.active).map((user) => user.id),
      );
      const createdDays = Number([...createdWindow][0] ?? 0);
      const createdAt = teamCreatedAt(team);
      return (
        (!memberIds.size ||
          [...memberIds].some((id) => teamMemberIds.has(id))) &&
        (!ownerIds.size || ownerIds.has(data.viewer.id)) &&
        !privateOnly &&
        (!createdDays ||
          Date.now() - createdAt.getTime() <= createdDays * 86400000)
      );
    })
    .sort(
      (left, right) =>
        compareTeams(left, right, ordering) * (descending ? -1 : 1),
    );
  const toggleColumn = (column: TeamColumn) =>
    setColumns((current) => {
      const next = new Set(current);
      if (next.has(column)) next.delete(column);
      else next.add(column);
      return next;
    });
  const filterGroups: DirectoryFilterGroup[] = [
    {
      id: "members",
      label: "Members",
      icon: <DirectoryPeopleIcon />,
      choices: data.users.map((user) => ({
        id: user.id,
        label: user.displayName,
        meta: "1 team",
        icon: <Avatar user={user} />,
      })),
    },
    {
      id: "owners",
      label: "Owners",
      icon: <DirectoryPeopleIcon />,
      choices: data.users.map((user) => ({
        id: user.id,
        label: user.displayName,
        icon: <Avatar user={user} />,
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
        { id: "1", label: "1 day ago" },
        { id: "3", label: "3 days ago" },
        { id: "7", label: "1 week ago" },
        { id: "30", label: "1 month ago" },
        { id: "90", label: "3 months ago" },
        { id: "180", label: "6 months ago", meta: "1 team" },
        { id: "365", label: "1 year ago", meta: "1 team" },
        { id: "custom", label: "Custom date or timeframe…" },
      ],
    },
  ];
  const selectedFilters = {
    members: memberIds,
    owners: ownerIds,
    private: privateOnly ? new Set(["private"]) : new Set<string>(),
    created: createdWindow,
  };
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
        if (checked) next.add(choiceId === "custom" ? "365" : choiceId);
        else next.delete(choiceId);
        return next;
      });
    if (groupId === "members") update(setMemberIds);
    if (groupId === "owners") update(setOwnerIds);
    if (groupId === "created") update(setCreatedWindow, true);
  };
  const clearFilters = () => {
    setAdvanced(false);
    setMemberIds(new Set());
    setOwnerIds(new Set());
    setPrivateOnly(false);
    setCreatedWindow(new Set());
  };
  const removeFilter = (id: string) => {
    if (id === "members") setMemberIds(new Set());
    if (id === "owners") setOwnerIds(new Set());
    if (id === "private") setPrivateOnly(false);
    if (id === "created") setCreatedWindow(new Set());
  };
  return (
    <>
      <div className="workspace-directory__toolbar workspace-teams-toolbar">
        <span>
          {!filtersActive &&
            `${teams.length} ${teams.length === 1 ? "team" : "teams"}`}
        </span>
        <span />
        <DirectoryFilterMenu
          groups={filterGroups}
          onAdvanced={() => setAdvanced(true)}
          onChoice={changeFilter}
          onDirect={(groupId) =>
            groupId === "private" && setPrivateOnly((value) => !value)
          }
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
            { id: "updated", label: "Updated" },
            { id: "created", label: "Created" },
          ]}
          properties={columns}
          propertyOptions={[
            { id: "membership", label: "Membership" },
            { id: "owners", label: "Owners" },
            { id: "projects", label: "Projects" },
            { id: "created", label: "Created" },
            { id: "updated", label: "Updated" },
            { id: "members", label: "Members" },
          ]}
        />
      </div>
      {filtersActive && (
        <DirectoryFilterBar
          advanced={advanced}
          chips={teamFilterChips(
            memberIds,
            ownerIds,
            privateOnly,
            createdWindow,
            data.users,
          )}
          groups={filterGroups}
          onAdvanced={() => setAdvanced(true)}
          onChoice={changeFilter}
          onClear={clearFilters}
          onDirect={(groupId) =>
            groupId === "private" && setPrivateOnly((value) => !value)
          }
          onRemoveAdvanced={() => setAdvanced(false)}
          onRemoveChip={removeFilter}
          selected={selectedFilters}
        />
      )}
      {teams.length === 0 ? (
        <DirectoryFilteredEmpty
          hiddenCount={data.teams.length}
          noun="teams"
          onClear={clearFilters}
        />
      ) : (
        <div
          className="workspace-directory__table workspace-teams-table"
          style={
            { "--team-columns": teamColumns(columns) } as React.CSSProperties
          }
        >
          <div className="workspace-team-columns">
            <button
              type="button"
              onClick={() => setDescending((value) => !value)}
            >
              Name{descending ? <ArrowUp /> : <ArrowDown />}
            </button>
            {columns.has("membership") && <span>Membership</span>}
            {columns.has("owners") && <span>Owners</span>}
            {columns.has("members") && <span>Members</span>}
            {columns.has("projects") && <span>Active projects</span>}
            {columns.has("created") && <span>Created</span>}
            {columns.has("updated") && <span>Updated</span>}
            <span />
          </div>
          {teams.map((team) => {
            const projectCount = data.projects.filter(
              (project) =>
                project.teamIds.includes(team.id) &&
                project.status.type !== "completed" &&
                project.status.type !== "canceled",
            ).length;
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
                <div className="workspace-team-identity">
                  <TeamGlyph color={team.color} />
                  <strong>{team.name}</strong>
                  <small>{team.key}</small>
                </div>
                {columns.has("membership") && (
                  <span className="workspace-team-joined">
                    <Check />
                    Joined
                  </span>
                )}
                {columns.has("owners") && (
                  <span className="workspace-team-owner">
                    <Avatar user={data.viewer} />
                    {data.viewer.displayName}
                  </span>
                )}
                {columns.has("members") && (
                  <button
                    className="workspace-team-members"
                    type="button"
                    aria-label="Open team member page"
                    onClick={(event) => {
                      event.stopPropagation();
                      onMembers();
                    }}
                  >
                    {data.users.slice(0, 3).map((user) => (
                      <Avatar key={user.id} user={user} />
                    ))}
                  </button>
                )}
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
                  <time>{formatDirectoryDate(teamCreatedAt(team))}</time>
                )}
                {columns.has("updated") && (
                  <time>{formatDirectoryDate(teamCreatedAt(team))}</time>
                )}
                <TeamRowMenu
                  team={team}
                  onUpdate={onUpdate}
                  onDelete={onDelete}
                />
              </div>
            );
          })}
        </div>
      )}
    </>
  );
}

function TeamRowMenu({
  team,
  onUpdate,
  onDelete,
}: {
  team: Team;
  onUpdate: (team: Team, input: Partial<Team>) => Promise<void>;
  onDelete: (team: Team) => Promise<void>;
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
        <DropdownMenu.Content className="workspace-directory__menu" align="end">
          <DropdownMenu.Item
            onSelect={() => {
              const name = window.prompt("Team name", team.name);
              if (name?.trim()) void onUpdate(team, { name: name.trim() });
            }}
          >
            Edit team
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() =>
              void navigator.clipboard.writeText(
                `${window.location.origin}${window.location.pathname.replace(/\/teams$/, `/team/${team.key}/overview`)}`,
              )
            }
          >
            Copy team link
          </DropdownMenu.Item>
          <DropdownMenu.Separator />
          <DropdownMenu.Item
            className="workspace-directory__danger"
            onSelect={() => void onDelete(team)}
          >
            <Trash2 />
            Delete team
          </DropdownMenu.Item>
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
  onDirect: (groupId: string) => void;
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
        <DirectoryFilterMenu
          groups={groups}
          onAdvanced={onAdvanced}
          onChoice={onChoice}
          onDirect={onDirect}
          selected={selected}
          trigger="add"
        />
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
          <button type="button">+&nbsp; Filter</button>
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
      <h2>No {noun} matching the filters</h2>
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

function InviteMembersDialog({
  open,
  workspace,
  teams,
  onOpenChange,
  onSent,
}: {
  open: boolean;
  workspace: BootstrapData["workspace"];
  teams: Team[];
  onOpenChange: (open: boolean) => void;
  onSent: () => Promise<void>;
}) {
  const [emails, setEmails] = useState("");
  const [error, setError] = useState("");
  const [role, setRole] = useState<"admin"|"member"|"guest">("member");
  const [teamId, setTeamId] = useState(teams[0]?.id ?? "");
  const [sending, setSending] = useState(false);
  useEffect(() => {
    if (!open) setError("");
  }, [open]);
  const send = async () => {
    const values = emails
      .split(/[\s,;]+/)
      .filter((value) => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(value));
    if (!values.length) {
      setError("Enter at least one email address to send an invite");
      return;
    }
    if (role === "guest" && !teamId) { setError("Select a team for guest access"); return; }
    setSending(true);
    try {
      const invitations = await inviteMembers(workspace.urlKey, { emails: values, role, teamIds: teamId ? [teamId] : [] });
      const token = invitations.find(item => item.token)?.token;
      if (token) await navigator.clipboard?.writeText(`${location.origin}/invite/${token}`);
      await onSent();
      toast.success(`${values.length} invitation${values.length === 1 ? "" : "s"} sent`);
      setEmails("");
      onOpenChange(false);
    } catch (error) {
      setError(error instanceof Error ? error.message : "Could not send invitations");
    } finally { setSending(false); }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="workspace-invite-dialog">
        <DialogTitle>
          <span className="workspace-dialog-avatar">
            {workspace.name.slice(0, 2).toUpperCase()}
          </span>
          Invite to your workspace
        </DialogTitle>
        <label>
          Email
          <textarea
            autoFocus
            aria-label="Email"
            aria-invalid={Boolean(error)}
            placeholder="email@foxmail.com, email2@foxmail.com…"
            value={emails}
            onChange={(event) => {
              setEmails(event.target.value);
              setError("");
            }}
            onKeyDown={(event) => {
              if ((event.metaKey || event.ctrlKey) && event.key === "Enter")
                send();
            }}
          />
          {error && <span className="workspace-invite-error">{error}</span>}
        </label>
        <div className="workspace-invite-access"><label>Role<select value={role} onChange={event => setRole(event.target.value as typeof role)}><option value="member">Member</option><option value="admin">Admin</option><option value="guest">Guest</option></select></label><label>Team<select value={teamId} onChange={event => setTeamId(event.target.value)}><option value="">No team</option>{teams.map(team => <option key={team.id} value={team.id}>{team.name}</option>)}</select></label></div>
        <footer>
          <button type="button" disabled={sending} onClick={() => void send()}>
            {sending ? "Sending…" : "Send invites"}
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function Avatar({ user }: { user: User }) {
  return user.avatarUrl ? (
    <img
      className="workspace-directory-avatar"
      src={user.avatarUrl}
      alt={user.email}
    />
  ) : (
    <span
      className="workspace-directory-avatar"
      style={{ background: avatarColor(user.id) }}
    >
      {initials(user.displayName)}
    </span>
  );
}
function CustomerMark({ customer }: { customer: Customer }) {
  return customer.logoUrl ? (
    <img className="workspace-customer-mark" src={customer.logoUrl} alt="" />
  ) : (
    <span className="workspace-customer-mark">{initials(customer.name)}</span>
  );
}
function TeamGlyph({ color }: { color: string }) {
  return (
    <svg
      className="workspace-team-glyph"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={{ color }}
    >
      <use href="/flow-core-icons.svg#Team" />
    </svg>
  );
}
function FlowProjectIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <use href="/flow-core-icons.svg#Project" />
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
  const widths: Record<TeamColumn, string> = {
    membership: "96px",
    owners: "150px",
    projects: "153px",
    created: "105px",
    updated: "105px",
    members: "126px",
  };
  return `minmax(280px,1fr) ${(
    [
      "membership",
      "owners",
      "members",
      "projects",
      "created",
      "updated",
    ] as TeamColumn[]
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
      "owner",
      "status",
      "tier",
      "domains",
      "source",
    ] as CustomerColumn[]
  )
    .filter((column) => columns.has(column))
    .map((column) => widths[column])
    .join(" ")} 38px`;
}

function compareTeams(left: Team, right: Team, ordering: TeamOrdering) {
  if (ordering === "name") return left.name.localeCompare(right.name);
  return teamCreatedAt(left).getTime() - teamCreatedAt(right).getTime();
}

function compareCustomers(
  left: Customer,
  right: Customer,
  ordering: CustomerOrdering,
) {
  if (ordering === "name") return left.name.localeCompare(right.name);
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

function teamCreatedAt(team: Team) {
  const match = /^team_(\d+)$/.exec(team.id);
  if (match) {
    try {
      return new Date(Number(BigInt(match[1]) / 1000000n));
    } catch {
      // Seeded teams use readable ids and fall through to the workspace fixture date.
    }
  }
  return new Date("2026-03-16T00:00:00Z");
}

function formatDirectoryDate(value: Date) {
  return value.toLocaleDateString("en", { month: "short", day: "numeric" });
}

function formatRevenue(value?: number) {
  if (!value) return "—";
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    maximumFractionDigits: 0,
  }).format(value);
}

function namesFor(ids: Set<string>, users: User[]) {
  return [...ids]
    .map((id) => users.find((user) => user.id === id)?.displayName ?? id)
    .join(", ");
}

function teamFilterChips(
  memberIds: Set<string>,
  ownerIds: Set<string>,
  privateOnly: boolean,
  createdWindow: Set<string>,
  users: User[],
): DirectoryFilterChip[] {
  const chips: DirectoryFilterChip[] = [];
  if (memberIds.size)
    chips.push({
      id: "members",
      label: "Members",
      value: namesFor(memberIds, users),
    });
  if (ownerIds.size)
    chips.push({
      id: "owners",
      label: "Owners",
      value: namesFor(ownerIds, users),
    });
  if (privateOnly)
    chips.push({ id: "private", label: "Private", value: "true" });
  if (createdWindow.size)
    chips.push({
      id: "created",
      label: "Created date",
      value: createdWindowLabel([...createdWindow][0]),
    });
  return chips;
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

function createdWindowLabel(value: string) {
  return (
    (
      {
        "1": "1 day ago",
        "3": "3 days ago",
        "7": "1 week ago",
        "30": "1 month ago",
        "90": "3 months ago",
        "180": "6 months ago",
        "365": "1 year ago",
      } as Record<string, string>
    )[value] ?? value
  );
}
