import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  Archive,
  Bell,
  BookOpen,
  CircleCheck,
  CircleHelp,
  Copy,
  Download,
  GripVertical,
  Keyboard,
  LogOut,
  MessageCircle,
  MessageCircleQuestion,
  MoreHorizontal,
  Plus,
  Repeat2,
  Rocket,
  Search,
  Settings,
  Star,
  X,
} from "lucide-react";
import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactElement,
  type ReactNode,
} from "react";
import { NavLink } from "react-router-dom";
import { toast } from "sonner";
import {
  addFavorite,
  addSubscription,
  removeFavorite,
  removeSubscription,
  setTeamMembership,
} from "@/lib/api";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import {
  CycleIcon as FlowCycleIcon,
  MembersIcon as FlowMembersIcon,
  SlackIcon as FlowSlackIcon,
} from "@/components/issue/issue-icons";
import { WorkspaceMenu } from "@/components/workspace/workspace-menu";
import {
  agentPath,
  asksPath,
  currentCyclePath,
  customersPath,
  documentsPath,
  draftsPath,
  inboxPath,
  initiativesPath,
  membersPath,
  myIssuesPath,
  projectsPath,
  pulsePath,
  releasePipelinesPath,
  reviewsPath,
  settingsPath,
  teamArchivePath,
  teamCyclesPath,
  teamHomePath,
  teamInitiativesPath,
  teamIssuesPath,
  teamProjectsPath,
  teamsPath,
  teamViewsPath,
  upcomingCyclePath,
  workspaceViewsPath,
  loopsPath,
} from "@/lib/app-routes";
import type {
  AccountBootstrap,
  BootstrapData,
  Team,
  Workspace,
} from "@/types/flow";
import {
  useSidebarCustomizationState,
  type SidebarBadgeStyle,
  type SidebarEntry,
  type SidebarGroup,
  type SidebarOrder,
  type SidebarPreferences,
  type SidebarVisibility,
} from "./sidebar-customization-state";

import "./sidebar.css";

export type PageId =
  | "inbox"
  | "search"
  | "pulse"
  | "reviews"
  | "my-issues"
  | "workspace-issues"
  | "team-issues"
  | "team-overview"
  | "team-archive"
  | "cycles"
  | "cycle-detail"
  | "projects"
  | "views"
  | "project-detail"
  | "issue-detail"
  | "initiatives"
  | "initiative-detail"
  | "members"
  | "customers"
  | "teams"
  | "new-team"
  | "drafts"
  | "agent"
  | "loops"
  | "releases"
  | "asks"
  | "documents"
  | "analytics"
  | "document-detail"
  | "customer-detail";

export function Sidebar({
  account,
  data,
  page,
  open = false,
  onOpenChange,
  onSearch,
  onCreate,
  onOpenSettings,
  onSwitchWorkspace,
  onCreateWorkspace,
  onLogout,
}: {
  account: AccountBootstrap;
  data: BootstrapData;
  page: PageId | "not-found";
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  onSearch: () => void;
  onCreate: () => void;
  onOpenSettings: (page?: "workspace" | "members") => void;
  onSwitchWorkspace: (workspace: Workspace) => void;
  onCreateWorkspace: () => void;
  onLogout: () => Promise<void>;
}) {
  const close = () => onOpenChange?.(false);
  const workspaceSlug = data.workspace.urlKey;
  const [customizeOpen, setCustomizeOpen] = useState(false);
  const [planOpen, setPlanOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const {
    badgeStyle,
    order: sidebarOrder,
    preferences,
    reorder: reorderSidebar,
    setBadgeStyle,
    setPreferences,
  } = useSidebarCustomizationState();
  const [dismissedTry, setDismissedTry] = useState<string[]>(readDismissedTry);
  const [navOverflowing, setNavOverflowing] = useState(false);
  const navRef = useRef<HTMLElement>(null);
  const featureEnabled = (feature: string) =>
    data.workspaceSettings.featureFlags[feature] !== false;

  useEffect(
    () => persistPreference("flow.sidebar.dismissed-try", dismissedTry),
    [dismissedTry],
  );
  useEffect(() => {
    const openShortcuts = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key === "/") {
        event.preventDefault();
        setShortcutsOpen(true);
      }
    };
    window.addEventListener("keydown", openShortcuts);
    return () => window.removeEventListener("keydown", openShortcuts);
  }, []);
  useEffect(() => {
    const nav = navRef.current;
    if (!nav) return;
    const update = () =>
      setNavOverflowing(nav.scrollHeight > nav.clientHeight + 1);
    const observer = new ResizeObserver(update);
    const mutationObserver = new MutationObserver(update);
    observer.observe(nav);
    mutationObserver.observe(nav, { childList: true, subtree: true });
    update();
    window.addEventListener("resize", update);
    return () => {
      observer.disconnect();
      mutationObserver.disconnect();
      window.removeEventListener("resize", update);
    };
  }, [dismissedTry, preferences, sidebarOrder]);

  const inboxUnread = data.notifications.filter(
    (item) =>
      item.recipientId === data.viewer.id &&
      !item.readAt &&
      !item.archivedAt &&
      !item.deletedAt &&
      (!item.snoozedUntil || new Date(item.snoozedUntil) <= new Date()),
  ).length;
  const reviewCount = data.reviews.filter(
    (item) =>
      item.status !== "merged" &&
      item.status !== "closed" &&
      item.reviewerIds.includes(data.viewer.id),
  ).length;
  const badgeCount = (entry: SidebarEntry) =>
    entry === "inbox"
      ? inboxUnread
      : entry === "reviews"
        ? reviewCount
        : entry === "drafts"
          ? data.drafts.length
          : 0;
  const activeWorkspaceEntry: SidebarEntry | undefined =
    page === "members"
      ? "members"
      : page === "customers" || page === "customer-detail"
        ? "customers"
        : page === "teams" || page === "new-team"
          ? "teams"
          : page === "documents" || page === "document-detail"
            ? "documents"
            : page === "releases"
              ? "releases"
              : page === "loops"
                ? "loops"
                : undefined;
  const show = (entry: SidebarEntry) =>
    preferences[entry] === "always" ||
    (preferences[entry] === "badged" && badgeCount(entry) > 0) ||
    entry === activeWorkspaceEntry;
  const available = (entry: SidebarEntry) =>
    (entry !== "initiatives" || featureEnabled("initiatives")) &&
    (entry !== "customers" || featureEnabled("customer-requests")) &&
    (entry !== "releases" || featureEnabled("releases"));
  const hiddenWorkspaceEntries = sidebarOrder.workspace.filter(
    (entry) =>
      available(entry) &&
      !show(entry) &&
      (data.viewerRole !== "guest" ||
        !["initiatives", "views", "customers"].includes(entry)),
  );
  const dismissTry = (id: string) =>
    setDismissedTry((current) => [...new Set([...current, id])]);
  const personalNavigation: Record<SidebarEntry, ReactNode> = {
    inbox: (
      <Nav
        badge={inboxUnread}
        icon={<FlowIcon name="Inbox" />}
        label="Inbox"
        to={inboxPath(workspaceSlug)}
        onClick={close}
      />
    ),
    reviews: (
      <Nav
        badge={reviewCount}
        active={page === "reviews"}
        icon={<ReviewsIcon />}
        label="Reviews"
        to={reviewsPath(workspaceSlug)}
        onClick={close}
      />
    ),
    myIssues: (
      <Nav
        active={page === "issue-detail"}
        icon={<MyIssuesIcon />}
        label="My issues"
        to={myIssuesPath(workspaceSlug, "assigned")}
        onClick={close}
      />
    ),
    pulse: featureEnabled("pulse") ? (
      <Nav
        icon={<PulseIcon />}
        label="Pulse"
        to={pulsePath(workspaceSlug)}
        onClick={close}
      />
    ) : null,
    drafts: (
      <Nav
        badge={data.drafts.length}
        active={page === "drafts"}
        icon={<DraftIcon />}
        label="Drafts"
        to={draftsPath(workspaceSlug)}
        onClick={close}
      />
    ),
    agent: featureEnabled("ai") ? (
      <Nav
        icon={<AgentIcon />}
        label="Agent"
        to={agentPath(workspaceSlug)}
        onClick={close}
      />
    ) : null,
    initiatives: null,
    projects: null,
    documents: null,
    views: null,
    members: null,
    customers: null,
    teams: null,
    releases: null,
    loops: null,
  };
  const workspaceNavigation: Record<SidebarEntry, ReactNode> = {
    initiatives:
      data.viewerRole === "guest" || !featureEnabled("initiatives") ? null : (
        <Nav
          active={page === "initiative-detail"}
          icon={<InitiativeIcon />}
          label="Initiatives"
          to={initiativesPath(workspaceSlug)}
          onClick={close}
        />
      ),
    projects: (
      <Nav
        active={page === "project-detail"}
        icon={<FlowIcon name="Project" />}
        label="Projects"
        to={projectsPath(workspaceSlug)}
        onClick={close}
      />
    ),
    documents: (
      <Nav
        active={page === "documents" || page === "document-detail"}
        icon={<BookOpen />}
        label="Documents"
        to={documentsPath(workspaceSlug)}
        onClick={close}
      />
    ),
    views:
      data.viewerRole === "guest" ? null : (
        <Nav
          icon={<FlowIcon name="CustomView" />}
          label="Views"
          to={workspaceViewsPath(workspaceSlug)}
          onClick={close}
        />
      ),
    members: (
      <Nav
        active={page === "members"}
        icon={<SidebarMembersIcon />}
        label="Members"
        to={membersPath(workspaceSlug)}
        onClick={close}
      />
    ),
    customers:
      data.viewerRole === "guest" ||
      !featureEnabled("customer-requests") ? null : (
        <Nav
          active={page === "customers"}
          icon={<CustomersIcon />}
          label="Customers"
          to={customersPath(workspaceSlug)}
          onClick={close}
        />
      ),
    teams: (
      <Nav
        active={page === "teams"}
        icon={<FlowIcon name="Team" />}
        label="Teams"
        to={teamsPath(workspaceSlug)}
        onClick={close}
      />
    ),
    releases: featureEnabled("releases") ? (
      <Nav
        active={page === "releases"}
        icon={<Rocket />}
        label="Releases"
        to={releasePipelinesPath(workspaceSlug)}
        onClick={close}
      />
    ) : null,
    loops: featureEnabled("loops") ? (
      <Nav
        active={page === "loops"}
        icon={<Repeat2 />}
        label="Loops"
        to={loopsPath(workspaceSlug)}
        onClick={close}
      />
    ) : null,
    inbox: null,
    reviews: null,
    myIssues: null,
    pulse: null,
    drafts: null,
    agent: null,
  };

  return (
    <>
      <button
        className={`sidebar-scrim ${open ? "open" : ""}`}
        type="button"
        aria-label="Close sidebar"
        onClick={close}
      />
      <aside
        className={`sidebar ${open ? "open" : ""}`}
        aria-label="Workspace navigation"
        data-badge-style={badgeStyle}
      >
        <div className="workspace-row">
          <WorkspaceMenu
            account={account}
            data={data}
            onSettings={onOpenSettings}
            onSwitch={onSwitchWorkspace}
            onCreate={onCreateWorkspace}
            onLogout={onLogout}
          />
          <button
            className="sidebar-top-action"
            type="button"
            aria-label="Search workspace"
            title="Search workspace"
            onClick={onSearch}
          >
            <FlowIcon name="Search" />
          </button>
          <button
            className="sidebar-top-action sidebar-compose"
            type="button"
            aria-label="Create new issue"
            title="Create new issue"
            onClick={onCreate}
          >
            <ComposeIcon />
          </button>
        </div>

        <nav ref={navRef} className="sidebar-nav">
          <div className="sidebar-primary-links">
            {sidebarOrder.personal.map((entry) =>
              show(entry) ? (
                <span className="sidebar-ordered-entry" key={entry}>
                  {personalNavigation[entry]}
                </span>
              ) : null,
            )}
          </div>

          <Section label="Workspace" storageKey="workspace">
            {sidebarOrder.workspace.map((entry) =>
              show(entry) ? (
                <span className="sidebar-ordered-entry" key={entry}>
                  {workspaceNavigation[entry]}
                </span>
              ) : null,
            )}
            <MoreMenu
              entries={hiddenWorkspaceEntries}
              onCustomize={() => setCustomizeOpen(true)}
              workspaceSlug={workspaceSlug}
              asks={featureEnabled("asks")}
            />
          </Section>

          <Section
            label="Your teams"
            storageKey="teams"
            action={
              <NavLink
                className="section-action"
                aria-label="Join a team"
                title="Join a team"
                to={teamsPath(workspaceSlug)}
                onClick={close}
              >
                <Plus />
              </NavLink>
            }
          >
            <div className="sidebar-team-list">
              {data.teams.filter(team=>!team.retiredAt).map((team) => (
                <TeamNavigation
                  key={team.id}
                  cyclesEnabled={Boolean(data.cycleSettings[team.id]?.enabled)}
                  current={data.cycles.some(
                    (cycle) =>
                      cycle.teamId === team.id && cycle.status === "current",
                  )}
                  upcoming={data.cycles.some(
                    (cycle) =>
                      cycle.teamId === team.id && cycle.status === "upcoming",
                  )}
                  initiativesEnabled={
                    data.viewerRole !== "guest" && featureEnabled("initiatives")
                  }
                  team={team}
                  favorite={data.favorites.some(
                    (item) =>
                      item.userId === data.viewer.id &&
                      item.resourceType === "team" &&
                      item.resourceId === team.id,
                  )}
                  subscribed={data.subscriptions.some(
                    (item) =>
                      item.userId === data.viewer.id &&
                      item.resourceType === "team" &&
                      item.resourceId === team.id,
                  )}
                  viewerId={data.viewer.id}
                  canLeave={data.teams.filter(item=>!item.retiredAt).length > 1}
                  workspaceSlug={workspaceSlug}
                  page={page}
                  onNavigate={close}
                />
              ))}
            </div>
          </Section>

          <Section label="Try" storageKey="try">
            {data.viewerRole === "admin" &&
              !dismissedTry.includes("invite") && (
                <TryItem
                  icon={<Plus />}
                  label="Invite people"
                  to={`${membersPath(workspaceSlug)}?invite=1`}
                  onClick={close}
                  onDismiss={() => dismissTry("invite")}
                />
              )}
            {!dismissedTry.includes("cycles") && data.teams[0] ? (
              <TryItem
                icon={<FlowCycleIcon />}
                label="Cycles"
                to={teamCyclesPath(workspaceSlug, data.teams[0].key)}
                onClick={close}
                onDismiss={() => dismissTry("cycles")}
              />
            ) : null}
            {!dismissedTry.includes("github") && (
              <TryItem
                icon={<GitHubIcon />}
                label="Connect GitHub"
                onClick={() =>
                  toast.info("GitHub is not connected in this workspace.")
                }
                onDismiss={() => dismissTry("github")}
              />
            )}
          </Section>
        </nav>

        <footer className="sidebar-footer">
          <HelpMenu onSettings={() => onOpenSettings("workspace")} onShortcuts={() => setShortcutsOpen(true)} />
          {!navOverflowing && (
            <button
              className="plan-pill"
              type="button"
              aria-label="Free plan"
              title="Your workspace is on a free plan"
              onClick={() => setPlanOpen(true)}
            >
              <UpgradeIcon />
              <span>Free plan</span>
            </button>
          )}
        </footer>
      </aside>

      <SidebarCustomization
        open={customizeOpen}
        onOpenChange={setCustomizeOpen}
        preferences={preferences}
        order={sidebarOrder}
        badgeStyle={badgeStyle}
        onBadgeStyleChange={setBadgeStyle}
        onChange={setPreferences}
        onReorder={reorderSidebar}
      />
      <UpgradeDialog open={planOpen} onOpenChange={setPlanOpen} />
      <KeyboardShortcutsDialog open={shortcutsOpen} onOpenChange={setShortcutsOpen}/>
    </>
  );
}

function Section({
  label,
  children,
  action,
  storageKey,
}: {
  label: string;
  children: ReactNode;
  action?: ReactNode;
  storageKey: string;
}) {
  const [expanded, setExpanded] = useState(() =>
    readExpandedSection(storageKey),
  );
  useEffect(
    () => persistPreference(`flow.sidebar.section.${storageKey}`, expanded),
    [expanded, storageKey],
  );
  return (
    <section
      className={`nav-section sidebar-section-${storageKey}`}
      data-expanded={expanded}
    >
      <div className="section-heading">
        <button
          className="section-label"
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          <span>{label}</span>
          <SectionDisclosureIcon expanded={expanded} />
        </button>
        {action}
      </div>
      {expanded ? <div className="section-content">{children}</div> : null}
    </section>
  );
}

function TeamNavigation({
  team,
  workspaceSlug,
  page,
  cyclesEnabled,
  current,
  upcoming,
  initiativesEnabled,
  favorite: initialFavorite,
  subscribed: initialSubscribed,
  viewerId,
  canLeave,
  onNavigate,
}: {
  team: Team;
  workspaceSlug: string;
  page: PageId | "not-found";
  cyclesEnabled: boolean;
  current: boolean;
  upcoming: boolean;
  initiativesEnabled: boolean;
  favorite: boolean;
  subscribed: boolean;
  viewerId: string;
  canLeave: boolean;
  onNavigate: () => void;
}) {
  const [expanded, setExpanded] = useState(() =>
    readExpandedSection(`team.${team.id}`),
  );
  const [favorite, setFavorite] = useState(initialFavorite),
    [subscribed, setSubscribed] = useState(initialSubscribed),
    [query, setQuery] = useState("");
  useEffect(
    () => persistPreference(`flow.sidebar.section.team.${team.id}`, expanded),
    [expanded, team.id],
  );
  const overviewPath = teamHomePath(workspaceSlug, team.key);
  const onOverview =
    typeof location !== "undefined" && location.pathname === overviewPath;
  return (
    <div className="sidebar-team" data-expanded={expanded}>
      <div className="team-heading">
        <button
          type="button"
          className="team-title"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          <FlowIcon name="Team" style={{ color: team.color }} />
          <strong>{team.name}</strong>
          <span className={`team-disclosure ${expanded ? "expanded" : ""}`}>
            <TeamDisclosureIcon />
          </span>
        </button>
        <DropdownMenu.Root
          onOpenChange={(open) => {
            if (!open) setQuery("");
          }}
        >
          <DropdownMenu.Trigger asChild>
            <button
              className="team-menu-trigger"
              type="button"
              aria-label="Team menu"
            >
              <TeamMenuIcon />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content
              className="sidebar-popover sidebar-team-menu"
              side="right"
              align="start"
              sideOffset={4}
            >
              <div className="sidebar-team-menu-search">
                <Search />
                <input
                  aria-label="Filter…"
                  autoFocus
                  placeholder="Filter…"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </div>
              {menuMatches(query, "Favorite") && (
                <DropdownMenu.Item
                  onSelect={() =>
                    void (
                      favorite
                        ? removeFavorite("team", team.id)
                        : addFavorite("team", team.id)
                    ).then(() => setFavorite((value) => !value))
                  }
                >
                  <Star fill={favorite ? "currentColor" : "none"} />
                  <span>{favorite ? "Unfavorite" : "Favorite"}</span>
                </DropdownMenu.Item>
              )}
              <DropdownMenu.Separator />
              {menuMatches(query, "Team settings") && (
                <DropdownMenu.Item asChild>
                  <NavLink to={settingsPath(workspaceSlug, "team", team.key)}>
                    <Settings />
                    <span>Team settings</span>
                  </NavLink>
                </DropdownMenu.Item>
              )}
              {menuMatches(query, "Copy URL") && (
                <DropdownMenu.Item
                  onSelect={() =>
                    void navigator.clipboard.writeText(
                      `${location.origin}${teamHomePath(workspaceSlug, team.key)}`,
                    )
                  }
                >
                  <Copy />
                  <span>Copy URL</span>
                </DropdownMenu.Item>
              )}
              {menuMatches(query, "Open archive") && (
                <DropdownMenu.Item asChild>
                  <NavLink to={teamArchivePath(workspaceSlug, team.key)}>
                    <Archive />
                    <span>Open archive</span>
                  </NavLink>
                </DropdownMenu.Item>
              )}
              <DropdownMenu.Separator />
              {menuMatches(query, "Subscribe") && (
                <DropdownMenu.Item
                  onSelect={() =>
                    void (
                      subscribed
                        ? removeSubscription("team", team.id)
                        : addSubscription("team", team.id, ["updates"])
                    ).then(() => setSubscribed((value) => !value))
                  }
                >
                  <Bell />
                  <span>{subscribed ? "Unsubscribe" : "Subscribe"}</span>
                </DropdownMenu.Item>
              )}
              {menuMatches(query, "Configure Slack notifications") && (
                <DropdownMenu.Item asChild>
                  <NavLink
                    to={settingsPath(
                      workspaceSlug,
                      "team",
                      team.key,
                      "notifications",
                    )}
                  >
                    <SidebarSlackIcon />
                    <span>Configure Slack notifications…</span>
                  </NavLink>
                </DropdownMenu.Item>
              )}
              <DropdownMenu.Separator />
              <DropdownMenu.Item
                disabled={!canLeave}
                title={
                  !canLeave
                    ? "You should be a member of at least one active team"
                    : undefined
                }
                onSelect={() =>
                  canLeave &&
                  void setTeamMembership(
                    workspaceSlug,
                    team.id,
                    viewerId,
                    false,
                  )
                }
              >
                <LogOut />
                <span>Leave team…</span>
              </DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
      {expanded && (
        <div className="team-links">
          <Nav
            active={onOverview}
            icon={<HomeIcon />}
            label="Home"
            to={overviewPath}
            onClick={onNavigate}
          />
          <Nav
            active={page === "team-issues" && !onOverview}
            icon={<IssuesIcon />}
            label="Issues"
            to={teamIssuesPath(workspaceSlug, team.key)}
            onClick={onNavigate}
          />
          {cyclesEnabled && (
            <Nav
              active={page === "cycles" || page === "cycle-detail"}
              icon={<FlowCycleIcon />}
              label="Cycles"
              to={teamCyclesPath(workspaceSlug, team.key)}
              onClick={onNavigate}
            />
          )}
          {cyclesEnabled && (current || upcoming) && (
            <div className="team-cycle-links">
              {current && (
                <SubNav
                  label="Current"
                  to={currentCyclePath(workspaceSlug, team.key)}
                  onClick={onNavigate}
                />
              )}{" "}
              {upcoming && (
                <SubNav
                  label="Upcoming"
                  to={upcomingCyclePath(workspaceSlug, team.key)}
                  onClick={onNavigate}
                />
              )}
              <i aria-hidden="true" />
            </div>
          )}
          {initiativesEnabled && (
            <Nav
              icon={<InitiativeIcon />}
              label="Initiatives"
              to={teamInitiativesPath(workspaceSlug, team.key)}
              onClick={onNavigate}
            />
          )}
          <Nav
            icon={<FlowIcon name="Project" />}
            label="Projects"
            to={teamProjectsPath(workspaceSlug, team.key)}
            onClick={onNavigate}
          />
          <Nav
            icon={<FlowIcon name="CustomView" />}
            label="Views"
            to={teamViewsPath(workspaceSlug, team.key)}
            onClick={onNavigate}
          />
        </div>
      )}
    </div>
  );
}

function MoreMenu({
  entries,
  onCustomize,
  workspaceSlug,
  asks,
}: {
  entries: SidebarEntry[];
  onCustomize: () => void;
  workspaceSlug: string;
  asks: boolean;
}) {
  const items: Partial<
    Record<
      SidebarEntry,
      { label: string; icon: ReactElement; onSelect?: () => void; to?: string }
    >
  > = {
    initiatives: {
      label: "Initiatives",
      icon: <InitiativeIcon />,
      to: initiativesPath(workspaceSlug),
    },
    projects: {
      label: "Projects",
      icon: <FlowIcon name="Project" />,
      to: projectsPath(workspaceSlug),
    },
    views: {
      label: "Views",
      icon: <FlowIcon name="CustomView" />,
      to: workspaceViewsPath(workspaceSlug),
    },
    members: {
      label: "Members",
      icon: <SidebarMembersIcon />,
      to: membersPath(workspaceSlug),
    },
    customers: {
      label: "Customers",
      icon: <CustomersIcon />,
      to: customersPath(workspaceSlug),
    },
    teams: {
      label: "Teams",
      icon: <FlowIcon name="Team" />,
      to: teamsPath(workspaceSlug),
    },
    releases: {
      label: "Releases",
      icon: <Rocket />,
      to: releasePipelinesPath(workspaceSlug),
    },
    loops: { label: "Loops", icon: <Repeat2 />, to: loopsPath(workspaceSlug) },
    documents: {
      label: "Documents",
      icon: <BookOpen />,
      to: documentsPath(workspaceSlug),
    },
  };
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          className="nav-item sidebar-more-trigger"
          type="button"
          aria-label="Show more links"
        >
          <MoreHorizontal />
          <span>More</span>
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="sidebar-popover sidebar-more-menu"
          side="bottom"
          align="start"
          sideOffset={6.5}
        >
          {entries.map((entry) => {
            const item = items[entry];
            if (!item) return null;
            if (item.to)
              return (
                <DropdownMenu.Item key={entry} asChild>
                  <NavLink to={item.to}>
                    {item.icon}
                    <span>{item.label}</span>
                  </NavLink>
                </DropdownMenu.Item>
              );
            return (
              <DropdownMenu.Item key={entry} onSelect={item.onSelect}>
                {item.icon}
                <span>{item.label}</span>
              </DropdownMenu.Item>
            );
          })}
          {asks && (
            <DropdownMenu.Item asChild>
              <NavLink to={asksPath(workspaceSlug)}>
                <MessageCircleQuestion />
                <span>Asks</span>
              </NavLink>
            </DropdownMenu.Item>
          )}
          <DropdownMenu.Separator />
          <DropdownMenu.Item onSelect={onCustomize}>
            <CustomizeIcon />
            <span>Customize sidebar</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function TryItem({
  icon,
  label,
  onClick,
  onDismiss,
  to,
}: {
  icon: ReactNode;
  label: string;
  onClick?: () => void;
  onDismiss: () => void;
  to?: string;
}) {
  const content = (
    <>
      {icon}
      <span>{label}</span>
    </>
  );
  return (
    <div className="try-item">
      {to ? (
        <NavLink to={to} onClick={onClick}>
          {content}
        </NavLink>
      ) : (
        <button type="button" onClick={onClick}>
          {content}
        </button>
      )}
      <button
        className="try-dismiss"
        type="button"
        aria-label={`Dismiss ${label}`}
        onClick={onDismiss}
      >
        <X />
      </button>
    </div>
  );
}

function Nav({
  icon,
  label,
  badge,
  active,
  onClick,
  to,
}: {
  icon: ReactElement;
  label: string;
  badge?: number;
  active?: boolean;
  onClick?: () => void;
  to?: string;
}) {
  const content = (
    <>
      <span className="nav-icon">{icon}</span>
      <span className="nav-label">{label}</span>
      {badge ? (
        <span className="nav-unread" aria-label={`${badge} unread`}>
          {badge > 99 ? "99+" : badge}
        </span>
      ) : null}
    </>
  );
  if (!to)
    return (
      <button
        type="button"
        className={`nav-item ${active ? "active" : ""}`}
        onClick={onClick}
      >
        {content}
      </button>
    );
  return (
    <NavLink
      end={false}
      className={({ isActive }) =>
        `nav-item ${active || isActive ? "active" : ""}`
      }
      to={to}
      onClick={onClick}
    >
      {content}
    </NavLink>
  );
}

function SubNav({
  label,
  to,
  onClick,
}: {
  label: string;
  to: string;
  onClick?: () => void;
}) {
  return (
    <NavLink
      end={false}
      className={({ isActive }) =>
        `nav-item team-sub-link ${isActive ? "active" : ""}`
      }
      to={to}
      onClick={onClick}
    >
      <span className="nav-label">{label}</span>
    </NavLink>
  );
}

export function SidebarCustomization({
  open,
  onOpenChange,
  preferences,
  order,
  badgeStyle,
  onBadgeStyleChange,
  onChange,
  onReorder,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  preferences: SidebarPreferences;
  order: SidebarOrder;
  badgeStyle: SidebarBadgeStyle;
  onBadgeStyleChange: (style: SidebarBadgeStyle) => void;
  onChange: (preferences: SidebarPreferences) => void;
  onReorder: (
    group: SidebarGroup,
    active: SidebarEntry,
    target: SidebarEntry,
  ) => void;
}) {
  const personal: Record<SidebarEntry, [string, ReactElement]> = {
    inbox: ["Inbox", <FlowIcon key="inbox" name="Inbox" />],
    reviews: ["Reviews", <ReviewsIcon key="reviews" />],
    myIssues: ["My issues", <MyIssuesIcon key="my-issues" />],
    pulse: ["Pulse", <PulseIcon key="pulse" />],
    drafts: ["Drafts", <DraftIcon key="drafts" />],
    agent: ["Agent", <AgentIcon key="agent" />],
    initiatives: ["", <></>],
    projects: ["", <></>],
    documents: ["", <></>],
    views: ["", <></>],
    members: ["", <></>],
    customers: ["", <></>],
    teams: ["", <></>],
    releases: ["", <></>],
    loops: ["", <></>],
  };
  const workspace: Record<SidebarEntry, [string, ReactElement]> = {
    initiatives: ["Initiatives", <InitiativeIcon key="initiatives" />],
    projects: ["Projects", <FlowIcon key="projects" name="Project" />],
    documents: ["Documents", <BookOpen key="documents" />],
    views: ["Views", <FlowIcon key="views" name="CustomView" />],
    members: ["Members", <SidebarMembersIcon key="members" />],
    customers: ["Customers", <CustomersIcon key="customers" />],
    teams: ["Teams", <FlowIcon key="teams" name="Team" />],
    releases: ["Releases", <Rocket key="releases" />],
    loops: ["Loops", <Repeat2 key="loops" />],
    inbox: ["", <></>],
    reviews: ["", <></>],
    myIssues: ["", <></>],
    pulse: ["", <></>],
    drafts: ["", <></>],
    agent: ["", <></>],
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sidebar-customize-dialog">
        <DialogTitle>Customize sidebar</DialogTitle>
        <label className="sidebar-badge-style">
          <span>Default badge style</span>
          <span className="badge-preview">1</span>
          <select
            aria-label="Default badge style"
            value={badgeStyle}
            onChange={(event) =>
              onBadgeStyleChange(event.target.value as SidebarBadgeStyle)
            }
          >
            <option value="count">Count</option>
            <option value="dot">Dot</option>
          </select>
        </label>
        <CustomizationGroup
          group="personal"
          label="Personal"
          entries={order.personal.map((id) => [id, ...personal[id]])}
          preferences={preferences}
          onChange={onChange}
          onReorder={onReorder}
        />
        <CustomizationGroup
          group="workspace"
          label="Workspace"
          entries={order.workspace.map((id) => [id, ...workspace[id]])}
          preferences={preferences}
          onChange={onChange}
          onReorder={onReorder}
        />
      </DialogContent>
    </Dialog>
  );
}

function CustomizationGroup({
  group,
  label,
  entries,
  preferences,
  onChange,
  onReorder,
}: {
  group: SidebarGroup;
  label: string;
  entries: Array<[SidebarEntry, string, ReactElement]>;
  preferences: SidebarPreferences;
  onChange: (preferences: SidebarPreferences) => void;
  onReorder: (
    group: SidebarGroup,
    active: SidebarEntry,
    target: SidebarEntry,
  ) => void;
}) {
  const draggingRef = useRef<SidebarEntry | null>(null);
  const [dragging, setDragging] = useState<SidebarEntry | null>(null);
  const [keyboardDragging, setKeyboardDragging] = useState<SidebarEntry | null>(
    null,
  );
  const finishDragging = () => {
    draggingRef.current = null;
    setDragging(null);
  };
  const moveToPointer = (event: React.PointerEvent<HTMLButtonElement>) => {
    const active = draggingRef.current;
    if (!active) return;
    const targetRow = document
      .elementFromPoint(event.clientX, event.clientY)
      ?.closest<HTMLElement>("[data-sidebar-customize-entry]");
    if (!targetRow || targetRow.dataset.sidebarCustomizeGroup !== group) return;
    const target = targetRow.dataset.sidebarCustomizeEntry as SidebarEntry;
    if (target !== active) onReorder(group, active, target);
  };
  const handleKeyboard = (
    event: React.KeyboardEvent<HTMLButtonElement>,
    id: SidebarEntry,
  ) => {
    if (event.key === " " || event.key === "Enter") {
      event.preventDefault();
      setKeyboardDragging((current) => (current === id ? null : id));
      return;
    }
    if (event.key === "Escape" && keyboardDragging === id) {
      event.preventDefault();
      setKeyboardDragging(null);
      return;
    }
    if (
      keyboardDragging !== id ||
      (event.key !== "ArrowUp" && event.key !== "ArrowDown")
    )
      return;
    event.preventDefault();
    const index = entries.findIndex(([entry]) => entry === id);
    const targetIndex = event.key === "ArrowUp" ? index - 1 : index + 1;
    const target = entries[targetIndex]?.[0];
    if (target) onReorder(group, id, target);
  };
  return (
    <section className="sidebar-customize-group">
      <h3>{label}</h3>
      <div>
        {entries.map(([id, name, icon]) => (
          <div
            className="sidebar-customize-row"
            data-dragging={dragging === id || keyboardDragging === id}
            data-sidebar-customize-entry={id}
            data-sidebar-customize-group={group}
            key={id}
            role="button"
            tabIndex={-1}
            aria-label={name}
            aria-roledescription="sortable"
          >
            <button
              className="customize-drag-handle"
              type="button"
              aria-label={`Reorder ${name}`}
              aria-pressed={keyboardDragging === id}
              title={`Drag to reorder ${name}`}
              onBlur={() => setKeyboardDragging(null)}
              onKeyDown={(event) => handleKeyboard(event, id)}
              onPointerDown={(event) => {
                if (event.button !== 0) return;
                event.preventDefault();
                event.currentTarget.focus();
                event.currentTarget.setPointerCapture(event.pointerId);
                draggingRef.current = id;
                setDragging(id);
              }}
              onPointerMove={moveToPointer}
              onPointerUp={finishDragging}
              onPointerCancel={finishDragging}
            >
              <GripVertical />
            </button>
            <span className="customize-row-icon">{icon}</span>
            <span>{name}</span>
            <select
              aria-label={`${name} visibility`}
              value={preferences[id]}
              onChange={(event) =>
                onChange({
                  ...preferences,
                  [id]: event.target.value as SidebarVisibility,
                })
              }
            >
              <option value="always">Always show</option>
              <option value="badged">Show when badged</option>
              <option value="never">Don't show</option>
            </select>
          </div>
        ))}
      </div>
    </section>
  );
}

function HelpMenu({ onSettings, onShortcuts }: { onSettings: () => void; onShortcuts:()=>void }) {
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          className="sidebar-help"
          type="button"
          aria-label="Open Help menu"
        >
          <CircleHelp />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="sidebar-popover sidebar-help-menu"
          side="top"
          align="start"
          sideOffset={4}
        >
          <DropdownMenu.Item
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/tree/main/docs")}
          >
            <Search />
            Search for help…
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/tree/main/docs")}
          >
            <BookOpen />
            Docs
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/issues/new/choose")}
          >
            <MessageCircle />
            Contact us
          </DropdownMenu.Item>
          <DropdownMenu.Item onSelect={onShortcuts}>
            <Keyboard />
            Keyboard shortcuts<kbd className="menu-end">⌘ /</kbd>
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() => openExternal(`${location.origin}/api/health`)}
          >
            <CircleCheck />
            Flow status
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/releases")}
          >
            <Download />
            Download apps
          </DropdownMenu.Item>
          <DropdownMenu.Item onSelect={onSettings}>
            <Settings />
            Settings<kbd className="menu-end">G then S</kbd>
          </DropdownMenu.Item>
          <DropdownMenu.Label className="sidebar-help-label">
            What’s new
          </DropdownMenu.Label>
          <DropdownMenu.Item
            className="sidebar-news-item"
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/blob/main/CHANGELOG.md")}
          >
            <NewsDot />
            Team initiatives
          </DropdownMenu.Item>
          <DropdownMenu.Item
            className="sidebar-news-item"
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/blob/main/CHANGELOG.md")}
          >
            <NewsDot />
            Coding sessions on mobile
          </DropdownMenu.Item>
          <DropdownMenu.Item
            className="sidebar-news-item"
            onSelect={() => openExternal("https://github.com/leozhengliu-pixel/flow/blob/main/CHANGELOG.md")}
          >
            <NewsDot />
            Full changelog
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function KeyboardShortcutsDialog({open,onOpenChange}:{open:boolean;onOpenChange:(open:boolean)=>void}){
  const groups=[
    ["Navigation",[["Search workspace","⌘ K"],["Go to Inbox","G then I"],["Go to My issues","G then M"],["Open settings","G then S"]]],
    ["Create",[["Create issue","C"],["Create project","N then P"],["Create initiative","N then I"]]],
    ["General",[["Close menu or dialog","Esc"],["Move through menus","↑ / ↓"],["Choose focused item","Enter"]]],
  ] as const;
  return <Dialog open={open} onOpenChange={onOpenChange}><DialogContent className="sidebar-shortcuts-dialog"><DialogTitle>Keyboard shortcuts</DialogTitle>{groups.map(([title,items])=><section key={title}><h3>{title}</h3>{items.map(([label,keys])=><div key={label}><span>{label}</span><kbd>{keys}</kbd></div>)}</section>)}</DialogContent></Dialog>
}

function UpgradeDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const rows = [
    ["Used 0 of 250 free issues", "Unlimited issues"],
    ["Only 2 teams", "3 additional teams"],
    ["10MB file limit", "Upload any file"],
    ["Admin roles", "Admin roles"],
  ];
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sidebar-upgrade-dialog">
        <DialogTitle className="sr-only">
          Your workspace is on the free plan
        </DialogTitle>
        <UpgradeIcon />
        <h2>Your workspace is on the free plan</h2>
        <p>Upgrade to keep creating issues and access more features.</p>
        <div className="upgrade-columns">
          <div>
            <strong>Current plan</strong>
            <span>Free</span>
          </div>
          <div>
            <strong>Basic plan</strong>
            <span>$10 per user/month, billed yearly</span>
          </div>
        </div>
        <div className="upgrade-table">
          {rows.map(([free, basic]) => (
            <div key={free}>
              <span>
                <i>×</i>
                {free}
              </span>
              <span>
                <i>✓</i>
                {basic}
              </span>
            </div>
          ))}
        </div>
        <footer>
          <button
            type="button"
            onClick={() => window.open("https://flow.app/pricing", "_blank")}
          >
            See all plans
          </button>
          <button
            className="upgrade-primary"
            type="button"
            onClick={() => window.open("https://flow.app/pricing", "_blank")}
          >
            Upgrade to Basic
          </button>
        </footer>
      </DialogContent>
    </Dialog>
  );
}

function FlowIcon({ name, style }: { name: string; style?: CSSProperties }) {
  return (
    <svg
      className="flow-sprite-icon"
      viewBox="0 0 16 16"
      aria-hidden="true"
      style={style}
    >
      <use href={`#${name}`} />
    </svg>
  );
}
function ReviewsIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M12.5 10C13.8807 10 15 11.1193 15 12.5C15 13.8807 13.8807 15 12.5 15C11.1193 15 10 13.8807 10 12.5C10 11.1193 11.1193 10 12.5 10ZM12.5 11.5C11.9477 11.5 11.5 11.9477 11.5 12.5C11.5 13.0523 11.9477 13.5 12.5 13.5C13.0523 13.5 13.5 13.0523 13.5 12.5C13.5 11.9477 13.0523 11.5 12.5 11.5Z" />
      <path d="M3.5 4.5C3.91414 4.50009 4.25 4.83584 4.25 5.25V14.249C4.24982 14.663 3.91403 14.9989 3.5 14.999C3.0859 14.999 2.75018 14.6631 2.75 14.249V5.25C2.75 4.83579 3.08579 4.5 3.5 4.5Z" />
      <path d="M10 2.75C11.7949 2.75 13.25 4.20507 13.25 6V10.75C13.25 11.1642 12.9142 11.5 12.5 11.5C12.0858 11.5 11.75 11.1642 11.75 10.75V6C11.75 5.0335 10.9665 4.25 10 4.25H8C7.58579 4.25 7.25 3.91421 7.25 3.5C7.25 3.08579 7.58579 2.75 8 2.75H10Z" />
    </svg>
  );
}
function MyIssuesIcon() {
  return <FlowIcon name="MyIssues" />;
}
function PulseIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M11.3354 1.08228C11.7059 1.27438 11.8561 1.74156 11.6708 2.12576L9.5593 6.50508C9.39923 6.83707 9.64113 7.22224 10.0097 7.22224H14C14.4142 7.22224 14.75 7.57046 14.75 8C14.75 8.42954 14.4142 8.77776 14 8.77776C10.4465 8.77776 7.32888 11.131 5.67083 14.5699C5.48559 14.9541 5.03507 15.1098 4.66459 14.9177C4.29411 14.7256 4.14394 14.2584 4.32918 13.8742L6.44081 9.49467C6.60085 9.16275 6.35907 8.77764 5.99059 8.77759H1.75C1.33579 8.77759 1 8.42937 1 7.99982C1 7.57028 1.33579 7.22206 1.75 7.22206C5.55362 7.22206 8.67116 4.86885 10.3292 1.43003C10.5145 1.04588 10.965 0.890196 11.3354 1.08228Z" />
    </svg>
  );
}
function AgentIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4.07132 3.8283C4.04394 3.81721 4.01406 3.81379 3.98488 3.8184C3.95566 3.82301 3.92826 3.83551 3.90561 3.85453C3.88297 3.87356 3.86594 3.8984 3.85636 3.92639C3.84678 3.95437 3.84501 3.98443 3.85124 4.01335L5.80802 13.1405C5.81898 13.1915 5.83884 13.2155 5.85542 13.2298C5.87605 13.2476 5.9078 13.2631 5.94754 13.268C5.98728 13.2729 6.0217 13.2654 6.04578 13.2532C6.06507 13.2434 6.08993 13.2252 6.11273 13.1784L7.83779 9.64746C8.05513 9.20258 8.45077 8.87059 8.92663 8.73378L12.7035 7.64791C12.7535 7.63353 12.776 7.61215 12.789 7.59475C12.8052 7.57307 12.8186 7.54044 12.8207 7.50049C12.8228 7.46054 12.813 7.42669 12.7992 7.40342C12.788 7.38476 12.7681 7.36116 12.7199 7.34158L4.07132 3.8283ZM3.75083 2.33677C4.04945 2.2896 4.35527 2.32474 4.63541 2.43841L13.2843 5.95183C14.747 6.54596 14.6351 8.65343 13.1179 9.08953L9.34109 10.1754C9.27311 10.1949 9.21659 10.2424 9.18554 10.3059L7.46077 13.8363C6.76755 15.2562 4.67275 14.9979 4.34147 13.4555L2.38492 4.3294C2.32134 4.03401 2.33935 3.72642 2.43722 3.44054C2.53514 3.15452 2.70919 2.90061 2.94065 2.70612C3.17211 2.51164 3.45221 2.38394 3.75083 2.33677Z" />
    </svg>
  );
}
function ComposeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M7.25 1C7.66414 1 7.99988 1.33589 8 1.75C8 2.16421 7.66421 2.5 7.25 2.5H4.75C3.50745 2.5 2.50012 3.50744 2.5 4.75V11.25C2.5 12.4926 3.50736 13.5 4.75 13.5H11.25C12.4926 13.5 13.5 12.4926 13.5 11.25V8.75C13.5001 8.33589 13.8359 8 14.25 8C14.6641 8 14.9999 8.33589 15 8.75V11.25C15 13.3211 13.3211 15 11.25 15H4.75C2.67893 15 1 13.3211 1 11.25V4.75C1.00012 2.67905 2.67899 1 4.75 1H7.25Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M13.4326 1.26953C13.7913.910937 14.3728.910883 14.7314 1.26953C15.0897 1.6282 15.0899 2.20981 14.7314 2.56836L9.2373 8.06152C8.68101 8.6177 7.94043 8.95161 7.15527 9C7.06754 9.0052 6.99468 8.93248 7 8.84473C7.04847 8.05961 7.38232 7.31897 7.93848 6.7627L13.4326 1.26953Z"
      />
    </svg>
  );
}
function IssuesIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <rect
        x="2"
        y="2.5"
        width="8.5"
        height="8.5"
        rx="2"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
      />
      <path
        d="M6 13.5h5.5a2 2 0 0 0 2-2V6"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}
function HomeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M2.75 14.25V6.48a.75.75 0 0 1 .242-.553l4.5-4.136a.75.75 0 0 1 1.016 0l4.5 4.136a.75.75 0 0 1 .242.553v7.77h-3.5v-3.267a1.75 1.75 0 0 0-3.5 0v3.267h-3.5Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}
function InitiativeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M7.4145 8.3381C7.68162 7.8873 8.31838 7.8873 8.5855 8.3381L11.896 13.925C12.2589 14.5374 11.6035 15.2506 10.9879 14.9132L8.10753 13.3343C8.04032 13.2975 7.95967 13.2975 7.89247 13.3343L5.0121 14.9132C4.39652 15.2506 3.74112 14.5374 4.10401 13.925L7.4145 8.3381Z" />
      <path d="M13.5 8C13.5 4.96243 11.0376 2.5 8 2.5C4.96243 2.5 2.5 4.96243 2.5 8C2.5 8.96927 2.75037 9.87822 3.18945 10.668L3.38867 10.999C3.60231 11.4033 3.4953 11.825 3.16992 12.0371C2.84468 12.249 2.41642 12.1766 2.17773 11.8809L2.00195 11.6104C1.36597 10.5558 1 9.31963 1 8C1 4.13401 4.13401 1 8 1C11.866 1 15 4.13401 15 8C15 9.40749 14.5834 10.7198 13.8672 11.8184C13.5836 12.1766 13.1553 12.249 12.8301 12.0371C12.4831 11.8109 12.3851 11.346 12.6113 10.999L12.8105 10.668C13.2496 9.87822 13.5 8.96927 13.5 8Z" />
    </svg>
  );
}
function GitHubIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M8 1.4a6.75 6.75 0 0 0-2.13 13.15c.34.06.46-.14.46-.32v-1.3c-1.88.41-2.28-.8-2.28-.8-.31-.78-.75-.98-.75-.98-.61-.42.05-.41.05-.41.68.05 1.03.69 1.03.69.6 1.03 1.57.73 1.95.56.06-.43.24-.73.43-.9-1.5-.17-3.08-.75-3.08-3.34 0-.74.26-1.34.69-1.81-.07-.17-.3-.86.07-1.79 0 0 .57-.18 1.86.69A6.4 6.4 0 0 1 8 4.61c.58 0 1.15.08 1.69.23 1.29-.87 1.85-.69 1.85-.69.37.93.14 1.62.07 1.79.43.47.69 1.07.69 1.81 0 2.6-1.58 3.16-3.09 3.33.24.21.46.63.46 1.27v1.88c0 .18.12.39.46.32A6.75 6.75 0 0 0 8 1.4Z"
        fill="currentColor"
      />
    </svg>
  );
}
function SidebarMembersIcon() {
  return <FlowMembersIcon />;
}
function CustomersIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M3 2.5h7a2 2 0 0 1 2 2v7H5a2 2 0 0 1-2-2v-7Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.3"
      />
      <path
        d="M6 5.5h4M6 8h3M12 5h1a1 1 0 0 1 1 1v7H7"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.3"
        strokeLinecap="round"
      />
    </svg>
  );
}
function DraftIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M3 2.5h8a1.5 1.5 0 0 1 1.5 1.5v8A1.5 1.5 0 0 1 11 13.5H3A1.5 1.5 0 0 1 1.5 12V4A1.5 1.5 0 0 1 3 2.5Z"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.3"
      />
      <path d="m5 10 1-2.5 3.2-3.2 1.5 1.5L7.5 9 5 10Z" fill="currentColor" />
    </svg>
  );
}
function CustomizeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M10.5 2.5h3v3M13.2 2.8 8.7 7.3M7 3H3.5A1.5 1.5 0 0 0 2 4.5v8A1.5 1.5 0 0 0 3.5 14h8a1.5 1.5 0 0 0 1.5-1.5V9"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.35"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
function UpgradeIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <circle
        cx="8"
        cy="8"
        r="6.2"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      />
      <path
        d="m5.5 8 2.5-2.5L10.5 8M8 5.7v4.8"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
function TeamDisclosureIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M7.00194 10.6239C6.66861 10.8183 6.25 10.5779 6.25 10.192V5.80802C6.25 5.42212 6.66861 5.18169 7.00194 5.37613L10.7596 7.56811C11.0904 7.76105 11.0904 8.23895 10.7596 8.43189L7.00194 10.6239Z" />
    </svg>
  );
}
function SectionDisclosureIcon({ expanded }: { expanded: boolean }) {
  return (
    <svg
      className={expanded ? "expanded" : ""}
      viewBox="0 0 16 16"
      aria-hidden="true"
    >
      <path d="M7.00194 10.6239C6.66861 10.8183 6.25 10.5779 6.25 10.192V5.80802C6.25 5.42212 6.66861 5.18169 7.00194 5.37613L10.7596 7.56811C11.0904 7.76105 11.0904 8.23895 10.7596 8.43189L7.00194 10.6239Z" />
    </svg>
  );
}
function TeamMenuIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z" />
    </svg>
  );
}
function SidebarSlackIcon() {
  return <FlowSlackIcon className="slack-icon" />;
}
function NewsDot() {
  return <span className="news-dot" aria-hidden="true" />;
}

function readDismissedTry(): string[] {
  try {
    const value = JSON.parse(
      localStorage.getItem("flow.sidebar.dismissed-try") ?? "[]",
    );
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}
function readExpandedSection(key: string) {
  try {
    return localStorage.getItem(`flow.sidebar.section.${key}`) !== "false";
  } catch {
    return true;
  }
}
function persistPreference(key: string, value: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    /* Preferences remain in memory. */
  }
}
function openExternal(url: string) {
  window.open(url, "_blank", "noopener,noreferrer");
}
function menuMatches(query: string, label: string) {
  return (
    !query.trim() || label.toLowerCase().includes(query.trim().toLowerCase())
  );
}
