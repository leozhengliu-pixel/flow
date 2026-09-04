import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as ContextMenu from "@radix-ui/react-context-menu";
import * as Select from "@radix-ui/react-select";
import {
  Archive,
  Bell,
  BookOpen,
  Check,
  CircleCheck,
  CircleHelp,
  Copy,
  ChevronRight,
  Download,
  Folder,
  FolderOpen,
  Keyboard,
  LogOut,
  MessageCircle,
  MessageCircleQuestion,
  MoreHorizontal,
  Plus,
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
  createFavoriteFolder,
  deleteFavoriteFolder,
  removeFavorite,
  removeSubscription,
  setTeamMembership,
  updateFavorite,
  updateFavoriteFolder,
} from "@/lib/api";

import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { DocumentGlyph } from "@/components/documents/document-icon";
import {
  CycleIcon as FlowCycleIcon,
  StatusIcon,
  SlackIcon as FlowSlackIcon,
} from "@/components/issue/issue-icons";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { WorkspaceMenu } from "@/components/workspace/workspace-menu";
import {
  agentPath,
  asksPath,
  currentCyclePath,
  cyclePath,
  customerPath,
  customersPath,
  dashboardsPath,
  documentPath,
  documentsPath,
  draftsPath,
  inboxPath,
  initiativePath,
  initiativesPath,
  issuePath,
  membersPath,
  myIssuesPath,
  projectsPath,
  projectPath,
  projectSavedViewPath,
  pulsePath,
  releasePipelinesPath,
  releasePipelinePath,
  releasePath,
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
  workspaceSavedViewPath,
  savedViewPathId,
  workspaceViewsPath,
  loopsPath,
} from "@/lib/app-routes";
import { useI18n } from "@/i18n/i18n";
import type {
  AccountBootstrap,
  BootstrapData,
  Favorite,
  FavoriteFolder,
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
  onReload,
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
  onReload?: () => Promise<void>;
}) {
  const close = () => onOpenChange?.(false);
  const workspaceSlug = data.workspace.urlKey;
  const [customizeOpen, setCustomizeOpen] = useState(false);
  const [shortcutsOpen, setShortcutsOpen] = useState(false);
  const [favorites, setFavorites] = useState<Favorite[]>(() =>
    data.favorites.filter((item) => item.userId === data.viewer.id),
  );
  const [favoriteFolders, setFavoriteFolders] = useState<FavoriteFolder[]>(() =>
    (data.favoriteFolders ?? []).filter(
      (item) => item.userId === data.viewer.id,
    ),
  );
  const {
    badgeStyle,
    order: sidebarOrder,
    preferences,
    reorder: reorderSidebar,
    setBadgeStyle,
    setPreferences,
  } = useSidebarCustomizationState();
  const [dismissedTry, setDismissedTry] = useState<string[]>(readDismissedTry);
  const featureEnabled = (feature: string) =>
    data.workspaceSettings.featureFlags[feature] !== false;

  useEffect(
    () =>
      setFavorites(
        data.favorites.filter((item) => item.userId === data.viewer.id),
      ),
    [data.favorites, data.viewer.id],
  );
  useEffect(
    () =>
      setFavoriteFolders(
        (data.favoriteFolders ?? []).filter(
          (item) => item.userId === data.viewer.id,
        ),
      ),
    [data.favoriteFolders, data.viewer.id],
  );

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
          ? data.drafts.filter(
              (draft) =>
                draft.type !== "loop" ||
                Boolean(
                  draft.title.trim() ||
                  draft.body.trim() ||
                  (typeof draft.metadata?.name === "string" &&
                    draft.metadata.name.trim()) ||
                  (typeof draft.metadata?.instructions === "string" &&
                    draft.metadata.instructions.trim()),
                ),
            ).length
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
  const reloadFavorites = async () => {
    await onReload?.();
  };
  const toggleSidebarFavorite = async (
    resourceType: string,
    resourceId: string,
  ) => {
    const existing = favorites.find(
      (item) =>
        item.resourceType === resourceType && item.resourceId === resourceId,
    );
    try {
      if (existing) {
        setFavorites((items) =>
          items.filter((item) => item.id !== existing.id),
        );
        await removeFavorite(resourceType, resourceId);
      } else {
        const created = await addFavorite(resourceType, resourceId);
        setFavorites((items) => [
          created,
          ...items.filter((item) => item.id !== created.id),
        ]);
      }
      await reloadFavorites();
    } catch (error) {
      setFavorites(
        data.favorites.filter((item) => item.userId === data.viewer.id),
      );
      toast.error(
        error instanceof Error ? error.message : "Could not update favorite",
      );
    }
  };
  const moveSidebarFavorite = async (
    favorite: Favorite,
    folderId: string,
    position?: number,
  ) => {
    const targetSiblings = favorites.filter(
      (item) =>
        (item.folderId ?? "") === (folderId || "") && item.id !== favorite.id,
    );
    const targetPosition =
      position ??
      targetSiblings.reduce(
        (maximum, item) => Math.max(maximum, item.position),
        -1,
      ) + 1;
    const previous = favorites;
    setFavorites((items) =>
      items.map((item) =>
        item.id === favorite.id
          ? {
              ...item,
              folderId: folderId || undefined,
              position: targetPosition,
            }
          : item,
      ),
    );
    try {
      const updated = await updateFavorite(
        favorite.resourceType,
        favorite.resourceId,
        { folderId, position: targetPosition },
      );
      setFavorites((items) =>
        items.map((item) => (item.id === updated.id ? updated : item)),
      );
      await reloadFavorites();
    } catch (error) {
      setFavorites(previous);
      toast.error(
        error instanceof Error ? error.message : "Could not update favorite",
      );
    }
  };
  const moveSidebarFolder = async (
    folder: FavoriteFolder,
    position: number,
  ) => {
    const previous = favoriteFolders;
    setFavoriteFolders((items) =>
      items.map((item) =>
        item.id === folder.id ? { ...item, position } : item,
      ),
    );
    try {
      const updated = await updateFavoriteFolder(folder.id, { position });
      setFavoriteFolders((items) =>
        items.map((item) => (item.id === updated.id ? updated : item)),
      );
      await reloadFavorites();
    } catch (error) {
      setFavoriteFolders(previous);
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not reorder favorite folders",
      );
    }
  };
  const createSidebarFolder = async (name: string) => {
    try {
      const created = await createFavoriteFolder(name);
      setFavoriteFolders((items) => [...items, created]);
      await reloadFavorites();
    } catch (error) {
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not create favorite folder",
      );
      throw error;
    }
  };
  const renameSidebarFolder = async (folder: FavoriteFolder, name: string) => {
    const previous = favoriteFolders;
    setFavoriteFolders((items) =>
      items.map((item) => (item.id === folder.id ? { ...item, name } : item)),
    );
    try {
      const updated = await updateFavoriteFolder(folder.id, { name });
      setFavoriteFolders((items) =>
        items.map((item) => (item.id === updated.id ? updated : item)),
      );
      await reloadFavorites();
    } catch (error) {
      setFavoriteFolders(previous);
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not rename favorite folder",
      );
    }
  };
  const removeSidebarFolder = async (folder: FavoriteFolder) => {
    const previousFolders = favoriteFolders,
      previousFavorites = favorites;
    setFavoriteFolders((items) =>
      items.filter((item) => item.id !== folder.id),
    );
    setFavorites((items) =>
      items.map((item) =>
        item.folderId === folder.id ? { ...item, folderId: undefined } : item,
      ),
    );
    try {
      await deleteFavoriteFolder(folder.id);
      await reloadFavorites();
    } catch (error) {
      setFavoriteFolders(previousFolders);
      setFavorites(previousFavorites);
      toast.error(
        error instanceof Error
          ? error.message
          : "Could not delete favorite folder",
      );
    }
  };
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
        badge={badgeCount("drafts")}
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
        icon={<ReleasesIcon />}
        label="Releases"
        to={releasePipelinesPath(workspaceSlug)}
        onClick={close}
      />
    ) : null,
    loops: featureEnabled("loops") ? (
      <Nav
        active={page === "loops"}
        icon={<LoopsIcon />}
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

        <nav className="sidebar-nav">
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

          {(favorites.length > 0 || favoriteFolders.length > 0) && (
            <FavoritesSection
              data={data}
              favorites={favorites}
              folders={favoriteFolders}
              workspaceSlug={workspaceSlug}
              onCreateFolder={createSidebarFolder}
              onMoveFavorite={moveSidebarFavorite}
              onMoveFolder={moveSidebarFolder}
              onRemoveFavorite={(favorite) =>
                void toggleSidebarFavorite(
                  favorite.resourceType,
                  favorite.resourceId,
                )
              }
              onRenameFolder={renameSidebarFolder}
              onRemoveFolder={removeSidebarFolder}
              onNavigate={close}
            />
          )}

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
              {data.teams
                .filter((team) => !team.retiredAt)
                .map((team) => (
                  <TeamNavigation
                    key={team.id}
                    cyclesEnabled={Boolean(
                      data.cycleSettings[team.id]?.enabled,
                    )}
                    current={data.cycles.some(
                      (cycle) =>
                        cycle.teamId === team.id && cycle.status === "current",
                    )}
                    upcoming={data.cycles.some(
                      (cycle) =>
                        cycle.teamId === team.id && cycle.status === "upcoming",
                    )}
                    initiativesEnabled={
                      data.viewerRole !== "guest" &&
                      featureEnabled("initiatives")
                    }
                    team={team}
                    favorite={favorites.some(
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
                    subscriptionEvents={data.subscriptions.find(
                      (item) =>
                        item.userId === data.viewer.id &&
                        item.resourceType === "team" &&
                        item.resourceId === team.id,
                    )?.events}
                    onSubscriptionEvents={async (events) => {
                      if (events.length) {
                        await addSubscription("team", team.id, events);
                      } else {
                        await removeSubscription("team", team.id);
                      }
                      await onReload?.();
                    }}
                    viewerId={data.viewer.id}
                    canLeave={
                      data.teams.filter((item) => !item.retiredAt).length > 1
                    }
                    workspaceSlug={workspaceSlug}
                    page={page}
                    onNavigate={close}
                    onFavoriteToggle={() =>
                      void toggleSidebarFavorite("team", team.id)
                    }
                  />
                ))}
            </div>
          </Section>

          <Section label="Try" storageKey="try">
            {(data.viewerRole === "admin" || data.viewerRole === "owner") &&
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
          <HelpMenu
            onSettings={() => onOpenSettings("workspace")}
            onShortcuts={() => setShortcutsOpen(true)}
          />
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
      <KeyboardShortcutsDialog
        open={shortcutsOpen}
        onOpenChange={setShortcutsOpen}
      />
    </>
  );
}

export function FavoritesSection({
  data,
  favorites,
  folders,
  onCreateFolder,
  onMoveFavorite,
  onMoveFolder,
  onNavigate,
  onRemoveFavorite,
  onRemoveFolder,
  onRenameFolder,
  workspaceSlug,
}: {
  data: BootstrapData;
  favorites: Favorite[];
  folders: FavoriteFolder[];
  onCreateFolder: (name: string) => Promise<void>;
  onMoveFavorite: (
    favorite: Favorite,
    folderId: string,
    position?: number,
  ) => Promise<void>;
  onMoveFolder: (folder: FavoriteFolder, position: number) => Promise<void>;
  onNavigate: () => void;
  onRemoveFavorite: (favorite: Favorite) => void;
  onRemoveFolder: (folder: FavoriteFolder) => Promise<void>;
  onRenameFolder: (folder: FavoriteFolder, name: string) => Promise<void>;
  workspaceSlug: string;
}) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(() =>
    readExpandedSection("favorites"),
  );
  const [creating, setCreating] = useState(false),
    [folderName, setFolderName] = useState("");
  const [openFolders, setOpenFolders] = useState<Set<string>>(
    () => new Set(folders.map((folder) => folder.id)),
  );
  const [renaming, setRenaming] = useState<string>(),
    [renameValue, setRenameValue] = useState("");
  const descriptors = favorites
    .map((favorite) => favoriteDescriptor(data, favorite, workspaceSlug))
    .filter((item): item is FavoriteDescriptor => Boolean(item))
    .sort(
      (a, b) =>
        a.favorite.position - b.favorite.position ||
        Date.parse(a.favorite.createdAt) - Date.parse(b.favorite.createdAt),
    );
  const orderedFolders = [...folders].sort(
    (a, b) => a.position - b.position || a.name.localeCompare(b.name),
  );
  useEffect(
    () => persistPreference("flow.sidebar.section.favorites", expanded),
    [expanded],
  );
  useEffect(
    () =>
      setOpenFolders(
        (current) =>
          new Set([...current, ...folders.map((folder) => folder.id)]),
      ),
    [folders],
  );
  const submitFolder = async () => {
    const name = folderName.trim();
    if (!name) return;
    await onCreateFolder(name);
    setFolderName("");
    setCreating(false);
  };
  const dropFavorite = (
    event: React.DragEvent,
    folderId: string,
    beforePosition?: number,
  ) => {
    event.preventDefault();
    const id = event.dataTransfer.getData("application/x-flow-favorite");
    const favorite = favorites.find((item) => item.id === id);
    if (!favorite) return;
    const siblings = favorites.filter(
      (item) => (item.folderId ?? "") === folderId && item.id !== favorite.id,
    );
    const position =
      beforePosition === undefined
        ? siblings.reduce(
            (maximum, item) => Math.max(maximum, item.position),
            -1,
          ) + 1
        : beforePosition - 0.5;
    void onMoveFavorite(favorite, folderId, position);
  };
  const dropFolder = (event: React.DragEvent, beforePosition?: number) => {
    event.preventDefault();
    event.stopPropagation();
    const id = event.dataTransfer.getData("application/x-flow-favorite-folder");
    const folder = folders.find((item) => item.id === id);
    if (!folder) return;
    const siblings = folders.filter((item) => item.id !== folder.id);
    const position =
      beforePosition === undefined
        ? siblings.reduce(
            (maximum, item) => Math.max(maximum, item.position),
            -1,
          ) + 1
        : beforePosition - 0.5;
    void onMoveFolder(folder, position);
  };
  const dropRoot = (event: React.DragEvent) => {
    if (event.dataTransfer.types.includes("application/x-flow-favorite-folder"))
      dropFolder(event);
    else dropFavorite(event, "");
  };
  return (
    <section className="nav-section sidebar-favorites" data-expanded={expanded}>
      <div className="section-heading">
        <button
          aria-expanded={expanded}
          className="section-label"
          onClick={() => setExpanded((value) => !value)}
          type="button"
        >
          <span>{t("Favorites")}</span>
          <SectionDisclosureIcon expanded={expanded} />
        </button>
        <button
          aria-label={t("Create new folder for favorites")}
          className="section-action"
          onClick={() => {
            setExpanded(true);
            setCreating(true);
          }}
          type="button"
        >
          <Plus />
        </button>
      </div>
      {expanded && (
        <div
          className="sidebar-favorites-content"
          onDragOver={(event) => event.preventDefault()}
          onDrop={dropRoot}
        >
          {creating && (
            <input
              aria-label={t("Folder name…")}
              autoFocus
              className="sidebar-favorite-folder-input"
              maxLength={80}
              placeholder={t("Folder name…")}
              value={folderName}
              onBlur={() => {
                if (!folderName.trim()) setCreating(false);
              }}
              onChange={(event) => setFolderName(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void submitFolder();
                } else if (event.key === "Escape") {
                  setFolderName("");
                  setCreating(false);
                }
              }}
            />
          )}
          {descriptors
            .filter((item) => !item.favorite.folderId)
            .map((item) => (
              <FavoriteLink
                item={item}
                folders={orderedFolders}
                key={item.favorite.id}
                onDropFavorite={(event) =>
                  dropFavorite(event, "", item.favorite.position)
                }
                onMove={onMoveFavorite}
                onNavigate={onNavigate}
                onRemove={onRemoveFavorite}
              />
            ))}
          {orderedFolders.map((folder) => {
            const folderItems = descriptors.filter(
              (item) => item.favorite.folderId === folder.id,
            );
            const folderOpen = openFolders.has(folder.id);
            return (
              <div
                className="sidebar-favorite-folder"
                key={folder.id}
                onDragOver={(event) => event.preventDefault()}
                onDrop={(event) => {
                  event.stopPropagation();
                  dropFavorite(event, folder.id);
                }}
              >
                <div
                  className="sidebar-favorite-folder-heading"
                  draggable
                  onDragStart={(event) => {
                    event.dataTransfer.effectAllowed = "move";
                    event.dataTransfer.setData(
                      "application/x-flow-favorite-folder",
                      folder.id,
                    );
                  }}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => dropFolder(event, folder.position)}
                >
                  {renaming === folder.id ? (
                    <input
                      aria-label={t("Folder name…")}
                      autoFocus
                      maxLength={80}
                      value={renameValue}
                      onBlur={() => setRenaming(undefined)}
                      onChange={(event) => setRenameValue(event.target.value)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" && renameValue.trim()) {
                          void onRenameFolder(folder, renameValue.trim());
                          setRenaming(undefined);
                        } else if (event.key === "Escape")
                          setRenaming(undefined);
                      }}
                    />
                  ) : (
                    <button
                      aria-expanded={folderOpen}
                      onClick={() =>
                        setOpenFolders((current) => {
                          const next = new Set(current);
                          if (next.has(folder.id)) next.delete(folder.id);
                          else next.add(folder.id);
                          return next;
                        })
                      }
                      type="button"
                    >
                      {folderOpen ? <FolderOpen /> : <Folder />}
                      <span data-i18n-ignore>{folder.name}</span>
                      <SectionDisclosureIcon expanded={folderOpen} />
                    </button>
                  )}
                  <DropdownMenu.Root>
                    <DropdownMenu.Trigger asChild>
                      <button
                        aria-label={t("Favorite folder actions")}
                        className="sidebar-favorite-folder-menu"
                        type="button"
                      >
                        <MoreHorizontal />
                      </button>
                    </DropdownMenu.Trigger>
                    <DropdownMenu.Portal>
                      <DropdownMenu.Content
                        align="start"
                        className="sidebar-popover sidebar-favorite-folder-popover"
                        side="right"
                        sideOffset={4}
                      >
                        <DropdownMenu.Item
                          onSelect={() => {
                            setRenameValue(folder.name);
                            setRenaming(folder.id);
                          }}
                        >
                          {t("Rename…")}
                        </DropdownMenu.Item>
                        <DropdownMenu.Separator />
                        <DropdownMenu.Item
                          className="danger"
                          onSelect={() => void onRemoveFolder(folder)}
                        >
                          {t("Delete folder")}
                        </DropdownMenu.Item>
                      </DropdownMenu.Content>
                    </DropdownMenu.Portal>
                  </DropdownMenu.Root>
                </div>
                {folderOpen && (
                  <div className="sidebar-favorite-folder-items">
                    {folderItems.map((item) => (
                      <FavoriteLink
                        item={item}
                        folders={orderedFolders}
                        key={item.favorite.id}
                        onDropFavorite={(event) =>
                          dropFavorite(event, folder.id, item.favorite.position)
                        }
                        onMove={onMoveFavorite}
                        onNavigate={onNavigate}
                        onRemove={onRemoveFavorite}
                      />
                    ))}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

interface FavoriteDescriptor {
  favorite: Favorite;
  href: string;
  icon: ReactNode;
  title: string;
}

function favoriteDescriptor(
  data: BootstrapData,
  favorite: Favorite,
  workspaceSlug: string,
): FavoriteDescriptor | undefined {
  if (favorite.resourceType === "issue") {
    const issue = data.issues.find((item) => item.id === favorite.resourceId);
    if (issue)
      return {
        favorite,
        href: issuePath(workspaceSlug, issue),
        icon: <StatusIcon state={issue.state} size={14} />,
        title: issue.title,
      };
  }
  if (favorite.resourceType === "project") {
    const project = data.projects.find(
      (item) => item.id === favorite.resourceId,
    );
    if (project)
      return {
        favorite,
        href: projectPath(workspaceSlug, project, "overview"),
        icon: <FlowIcon name="Project" style={{ color: project.color }} />,
        title: project.name,
      };
  }
  if (favorite.resourceType === "team") {
    const team = data.teams.find((item) => item.id === favorite.resourceId);
    if (team)
      return {
        favorite,
        href: teamHomePath(workspaceSlug, team.key),
        icon: <FlowIcon name="Team" style={{ color: team.color }} />,
        title: team.name,
      };
  }
  if (favorite.resourceType === "document") {
    const document = data.documents.find(
      (item) => item.id === favorite.resourceId,
    );
    if (document)
      return {
        favorite,
        href: documentPath(workspaceSlug, document),
        icon: <DocumentGlyph document={document} />,
        title: document.title,
      };
  }
  if (favorite.resourceType === "label") {
    const label = data.labels.find((item) => item.id === favorite.resourceId);
    if (label)
      return {
        favorite,
        href: settingsPath(
          workspaceSlug,
          label.resourceType === "project" ? "project-labels" : "issue-labels",
        ),
        icon: (
          <span
            className="sidebar-favorite-label-dot"
            style={{ backgroundColor: label.color }}
          />
        ),
        title: label.name,
      };
  }
  if (favorite.resourceType === "cycle") {
    const cycle = data.cycles.find((item) => item.id === favorite.resourceId),
      team = data.teams.find((item) => item.id === cycle?.teamId);
    if (cycle && team)
      return {
        favorite,
        href: cyclePath(workspaceSlug, team.key, cycle),
        icon: <FlowCycleIcon />,
        title: cycle.name || `Cycle ${cycle.number}`,
      };
  }
  if (favorite.resourceType === "view") {
    const view = data.savedViews.find(
      (item) => item.id === favorite.resourceId,
    );
    if (view) {
      const project = view.projectId
        ? data.projects.find((item) => item.id === view.projectId)
        : undefined;
      return {
        favorite,
        href: project
          ? projectSavedViewPath(
              workspaceSlug,
              project.slugId,
              savedViewPathId(view),
            )
          : workspaceSavedViewPath(workspaceSlug, savedViewPathId(view)),
        icon: <ViewGlyph color={view.color} icon={view.icon} />,
        title: view.name,
      };
    }
  }
  if (favorite.resourceType === "initiative") {
    const initiative = data.initiatives.find(
      (item) => item.id === favorite.resourceId,
    );
    if (initiative)
      return {
        favorite,
        href: initiativePath(workspaceSlug, initiative),
        icon: <InitiativeIcon />,
        title: initiative.name,
      };
  }
  if (favorite.resourceType === "customer") {
    const customer = data.customers.find(
      (item) => item.id === favorite.resourceId,
    );
    if (customer)
      return {
        favorite,
        href: customerPath(workspaceSlug, customer),
        icon: <CustomersIcon />,
        title: customer.name,
      };
  }
  if (favorite.resourceType === "release_pipeline") {
    const pipeline = data.releasePipelines.find(
      (item) => item.id === favorite.resourceId,
    );
    if (pipeline)
      return {
        favorite,
        href: releasePipelinePath(workspaceSlug, pipeline.slugId),
        icon: <ReleasesIcon />,
        title: pipeline.name,
      };
  }
  if (favorite.resourceType === "release") {
    const release = data.releases.find(
        (item) => item.id === favorite.resourceId,
      ),
      pipeline = data.releasePipelines.find(
        (item) => item.id === release?.pipelineId,
      );
    if (release && pipeline)
      return {
        favorite,
        href: releasePath(workspaceSlug, pipeline.slugId, release.slugId),
        icon: <ReleasesIcon />,
        title: release.name,
      };
  }
  if (favorite.resourceType === "dashboard")
    return {
      favorite,
      href: dashboardsPath(workspaceSlug, favorite.resourceId),
      icon: <FlowIcon name="CustomView" />,
      title: "Dashboard",
    };
  return undefined;
}

function FavoriteLink({
  folders,
  item,
  onDropFavorite,
  onMove,
  onNavigate,
  onRemove,
}: {
  folders: FavoriteFolder[];
  item: FavoriteDescriptor;
  onDropFavorite: (event: React.DragEvent) => void;
  onMove: (favorite: Favorite, folderId: string) => Promise<void>;
  onNavigate: () => void;
  onRemove: (favorite: Favorite) => void;
}) {
  const { t } = useI18n();
  const link = (
    <NavLink
      className="sidebar-favorite-link"
      draggable
      onClick={onNavigate}
      onDragOver={(event) => event.preventDefault()}
      onDrop={(event) => {
        event.stopPropagation();
        onDropFavorite(event);
      }}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = "move";
        event.dataTransfer.setData(
          "application/x-flow-favorite",
          item.favorite.id,
        );
      }}
      to={item.href}
    >
      <span className="sidebar-favorite-icon">{item.icon}</span>
      <span
        className="sidebar-favorite-title"
        data-i18n-ignore
        title={item.title}
      >
        {item.title}
      </span>
      <span
        aria-label={t("Remove favorite")}
        className="sidebar-favorite-remove"
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          onRemove(item.favorite);
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            onRemove(item.favorite);
          }
        }}
        role="button"
        tabIndex={0}
      >
        <X />
      </span>
    </NavLink>
  );
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger asChild>{link}</ContextMenu.Trigger>
      <ContextMenu.Portal>
        <ContextMenu.Content className="sidebar-popover sidebar-favorite-context-menu">
          <ContextMenu.Item
            onSelect={() =>
              void navigator.clipboard.writeText(
                new URL(item.href, location.origin).href,
              )
            }
          >
            <Copy />
            {t("Copy URL")}
          </ContextMenu.Item>
          {folders.length > 0 && (
            <ContextMenu.Sub>
              <ContextMenu.SubTrigger>
                <Folder />
                <span>{t("Move to folder")}</span>
                <TeamDisclosureIcon />
              </ContextMenu.SubTrigger>
              <ContextMenu.Portal>
                <ContextMenu.SubContent
                  className="sidebar-popover sidebar-favorite-context-menu"
                  sideOffset={4}
                >
                  <ContextMenu.Item
                    disabled={!item.favorite.folderId}
                    onSelect={() => void onMove(item.favorite, "")}
                  >
                    {t("No folder")}
                  </ContextMenu.Item>
                  {folders.map((folder) => (
                    <ContextMenu.Item
                      key={folder.id}
                      onSelect={() => void onMove(item.favorite, folder.id)}
                    >
                      <span data-i18n-ignore>{folder.name}</span>
                    </ContextMenu.Item>
                  ))}
                </ContextMenu.SubContent>
              </ContextMenu.Portal>
            </ContextMenu.Sub>
          )}
          <ContextMenu.Separator />
          <ContextMenu.Item onSelect={() => onRemove(item.favorite)}>
            <X />
            {t("Remove favorite")}
          </ContextMenu.Item>
        </ContextMenu.Content>
      </ContextMenu.Portal>
    </ContextMenu.Root>
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

const TEAM_SUBSCRIPTION_OPTIONS = [
  ["issueAdded", "An issue is added to the team"],
  ["issueCompleted", "An issue is marked completed or canceled"],
  ["triage", "An issue is added to the triage queue"],
  ["pulse", "A team project update is posted"],
] as const;

function initialTeamSubscriptionEvents(subscribed: boolean, events?: string[]) {
  if (!subscribed) return [];
  if (!events?.length) return ["pulse"];
  return events.map((event) => (event === "updates" ? "pulse" : event));
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
  subscriptionEvents: initialSubscriptionEvents,
  onSubscriptionEvents,
  viewerId,
  canLeave,
  onNavigate,
  onFavoriteToggle,
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
  subscriptionEvents?: string[];
  onSubscriptionEvents: (events: string[]) => Promise<void>;
  viewerId: string;
  canLeave: boolean;
  onNavigate: () => void;
  onFavoriteToggle: () => void;
}) {
  const [expanded, setExpanded] = useState(() =>
    readExpandedSection(`team.${team.id}`),
  );
  const [favorite, setFavorite] = useState(initialFavorite),
    [subscriptionEvents, setSubscriptionEvents] = useState(() =>
      initialTeamSubscriptionEvents(initialSubscribed, initialSubscriptionEvents),
    ),
    [query, setQuery] = useState("");
  useEffect(
    () => persistPreference(`flow.sidebar.section.team.${team.id}`, expanded),
    [expanded, team.id],
  );
  useEffect(() => {
    setSubscriptionEvents(
      initialTeamSubscriptionEvents(initialSubscribed, initialSubscriptionEvents),
    );
  }, [initialSubscribed, initialSubscriptionEvents, team.id]);
  const updateSubscriptionEvent = async (event: string, checked: boolean) => {
    const previous = subscriptionEvents;
    const next = checked
      ? [...new Set([...previous, event])]
      : previous.filter((value) => value !== event);
    setSubscriptionEvents(next);
    try {
      await onSubscriptionEvents(next);
    } catch {
      setSubscriptionEvents(previous);
    }
  };
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
              sideOffset={-24}
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
                  onSelect={() => {
                    setFavorite((value) => !value);
                    onFavoriteToggle();
                  }}
                >
                  <Star fill={favorite ? "currentColor" : "none"} />
                  <span>{favorite ? "Unfavorite" : "Favorite"}</span>
                  <kbd>⌥ F</kbd>
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
                  <kbd>⌘ ⇧ ,</kbd>
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
                <DropdownMenu.Sub>
                  <DropdownMenu.SubTrigger>
                    <Bell />
                    <span>Subscribe</span>
                    <ChevronRight className="menu-chevron" />
                  </DropdownMenu.SubTrigger>
                  <DropdownMenu.Portal>
                    <DropdownMenu.SubContent
                      className="sidebar-popover sidebar-team-subscribe-menu"
                      sideOffset={-2}
                    >
                      <DropdownMenu.Label>Inbox notifications</DropdownMenu.Label>
                      {TEAM_SUBSCRIPTION_OPTIONS.slice(0, 3).map(([event, label]) => (
                        <DropdownMenu.CheckboxItem
                          checked={subscriptionEvents.includes(event)}
                          key={event}
                          onCheckedChange={(checked) =>
                            void updateSubscriptionEvent(event, checked)
                          }
                          onSelect={(event) => event.preventDefault()}
                        >
                          <span className="sidebar-team-subscribe-check">
                            <DropdownMenu.ItemIndicator>
                              <Check size={10} />
                            </DropdownMenu.ItemIndicator>
                          </span>
                          <span>{label}</span>
                        </DropdownMenu.CheckboxItem>
                      ))}
                      <DropdownMenu.Label>Pulse updates</DropdownMenu.Label>
                      <DropdownMenu.CheckboxItem
                        checked={subscriptionEvents.includes("pulse")}
                        onCheckedChange={(checked) =>
                          void updateSubscriptionEvent("pulse", checked)
                        }
                        onSelect={(event) => event.preventDefault()}
                      >
                        <span className="sidebar-team-subscribe-check">
                          <DropdownMenu.ItemIndicator>
                            <Check size={10} />
                          </DropdownMenu.ItemIndicator>
                        </span>
                        <span>A team project update is posted</span>
                      </DropdownMenu.CheckboxItem>
                    </DropdownMenu.SubContent>
                  </DropdownMenu.Portal>
                </DropdownMenu.Sub>
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
      icon: <ReleasesIcon />,
      to: releasePipelinesPath(workspaceSlug),
    },
    loops: {
      label: "Loops",
      icon: <LoopsIcon />,
      to: loopsPath(workspaceSlug),
    },
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
  const { t } = useI18n();
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
    releases: ["Releases", <ReleasesIcon key="releases" />],
    loops: ["Loops", <LoopsIcon key="loops" />],
    inbox: ["", <></>],
    reviews: ["", <></>],
    myIssues: ["", <></>],
    pulse: ["", <></>],
    drafts: ["", <></>],
    agent: ["", <></>],
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="sidebar-customize-dialog"
        closeLabel={t("Close modal dialog")}
        overlayClassName="sidebar-customize-overlay"
      >
        <header className="sidebar-customize-header">
          <DialogTitle>{t("Customize sidebar")}</DialogTitle>
        </header>
        <div className="sidebar-customize-body">
          <div className="sidebar-badge-style">
            <span>{t("Default badge style")}</span>
            <SidebarSelect
              ariaLabel={t("Default badge style")}
              compact
              options={[
                { value: "count", label: t("Count") },
                { value: "dot", label: t("Dot") },
              ]}
              value={badgeStyle}
              onChange={(value) =>
                onBadgeStyleChange(value as SidebarBadgeStyle)
              }
              renderValue={(value) => (
                <span className="sidebar-badge-value">
                  <span className={`badge-preview is-${value}`}>
                    {value === "count" ? "1" : ""}
                  </span>
                  {value === "count" ? t("Count") : t("Dot")}
                </span>
              )}
            />
          </div>
          <CustomizationGroup
            group="personal"
            label={t("Personal")}
            entries={order.personal.map((id) => [id, ...personal[id]])}
            preferences={preferences}
            onChange={onChange}
            onReorder={onReorder}
          />
          <CustomizationGroup
            group="workspace"
            label={t("Workspace")}
            entries={order.workspace.map((id) => [id, ...workspace[id]])}
            preferences={preferences}
            onChange={onChange}
            onReorder={onReorder}
          />
        </div>
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
  const { locale, t } = useI18n();
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
        {entries.map(([id, name, icon]) => {
          const localizedName = t(name);
          const reorderLabel =
            locale === "zh-CN" ? `重新排序${localizedName}` : `Reorder ${name}`;
          const reorderTitle =
            locale === "zh-CN"
              ? `拖动以重新排序${localizedName}`
              : `Drag to reorder ${name}`;
          const visibilityLabel =
            locale === "zh-CN"
              ? `${localizedName}的可见性`
              : `${name} visibility`;
          const visibilityOptions =
            id === "inbox"
              ? [
                  { value: "always", label: t("Always show") },
                  { value: "badged", label: t("Show when badged") },
                ]
              : ["reviews", "pulse", "drafts"].includes(id)
                ? [
                    { value: "always", label: t("Always show") },
                    { value: "badged", label: t("Show when badged") },
                    { value: "never", label: t("Don't show") },
                  ]
                : [
                    { value: "always", label: t("Always show") },
                    { value: "never", label: t("Don't show") },
                  ];
          return (
            <div
              className="sidebar-customize-row"
              data-dragging={dragging === id || keyboardDragging === id}
              data-sidebar-customize-entry={id}
              data-sidebar-customize-group={group}
              key={id}
              role="button"
              tabIndex={-1}
              aria-label={localizedName}
              aria-roledescription={locale === "zh-CN" ? "可排序" : "sortable"}
            >
              <button
                className="customize-drag-handle"
                type="button"
                aria-label={reorderLabel}
                aria-pressed={keyboardDragging === id}
                title={reorderTitle}
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
                <SidebarDragHandleIcon />
              </button>
              <span className="customize-row-icon">{icon}</span>
              <span data-i18n-ignore={name === "Loops" || undefined}>
                {localizedName}
              </span>
              <SidebarSelect
                ariaLabel={visibilityLabel}
                options={visibilityOptions}
                value={preferences[id]}
                onChange={(value) =>
                  onChange({
                    ...preferences,
                    [id]: value as SidebarVisibility,
                  })
                }
              />
            </div>
          );
        })}
      </div>
    </section>
  );
}

function SidebarSelect({
  ariaLabel,
  compact = false,
  onChange,
  options,
  renderValue,
  value,
}: {
  ariaLabel: string;
  compact?: boolean;
  onChange: (value: string) => void;
  options: Array<{ value: string; label: string }>;
  renderValue?: (value: string) => ReactNode;
  value: string;
}) {
  const selected =
    options.find((option) => option.value === value) ?? options[0];
  const selectedIndex = Math.max(
    0,
    options.findIndex((option) => option.value === selected.value),
  );
  return (
    <Select.Root value={selected.value} onValueChange={onChange}>
      <Select.Trigger
        aria-label={ariaLabel}
        className="sidebar-customize-select"
      >
        <Select.Value asChild>
          <span className="sidebar-customize-select-value">
            {renderValue ? renderValue(selected.value) : selected.label}
          </span>
        </Select.Value>
        <Select.Icon asChild>
          <SidebarChevronIcon />
        </Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content
          align="start"
          alignOffset={-2}
          className={`sidebar-customize-select-menu${compact ? " is-compact" : ""}`}
          position="popper"
          side="bottom"
          sideOffset={-35 - selectedIndex * 32}
        >
          <Select.Viewport>
            {options.map((option) => (
              <Select.Item
                className="sidebar-customize-select-option"
                key={option.value}
                value={option.value}
              >
                <Select.ItemText>{option.label}</Select.ItemText>
                <Select.ItemIndicator className="sidebar-customize-select-indicator">
                  <Check />
                </Select.ItemIndicator>
              </Select.Item>
            ))}
          </Select.Viewport>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  );
}

function SidebarChevronIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 9 5">
      <path d="M1.915.557a.667.667 0 0 0-.943.943l2.862 2.862a.942.942 0 0 0 1.333 0L8.028 1.5a.667.667 0 0 0-.943-.943L4.5 3.14 1.915.557Z" />
    </svg>
  );
}

function SidebarDragHandleIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 6 10">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M1 8C1.55228 8 2 8.44772 2 9C2 9.55228 1.55228 10 1 10C.447715 10 0 9.55228 0 9C0 8.44772.447715 8 1 8Zm4 0C5.55228 8 6 8.44772 6 9c0 .55228-.44772 1-1 1s-1-.44772-1-1c0-.55228.44772-1 1-1ZM1 4c.55228 0 1 .44772 1 1S1.55228 6 1 6 0 5.55228 0 5s.44772-1 1-1Zm4 0c.55228 0 1 .44772 1 1S5.55228 6 5 6 4 5.55228 4 5s.44772-1 1-1ZM1 0c.55228 0 1 .447715 1 1s-.44772 1-1 1S0 1.55228 0 1 .447715 0 1 0Zm4 0c.55228 0 1 .447715 1 1s-.44772 1-1 1-1-.447715-1-1 .44772-1 1-1Z"
      />
    </svg>
  );
}

function HelpMenu({
  onSettings,
  onShortcuts,
}: {
  onSettings: () => void;
  onShortcuts: () => void;
}) {
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
            onSelect={() =>
              openExternal(
                "https://github.com/leozhengliu-pixel/flow/tree/main/docs",
              )
            }
          >
            <Search />
            Search for help…
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() =>
              openExternal(
                "https://github.com/leozhengliu-pixel/flow/tree/main/docs",
              )
            }
          >
            <BookOpen />
            Docs
          </DropdownMenu.Item>
          <DropdownMenu.Item
            onSelect={() =>
              openExternal(
                "https://github.com/leozhengliu-pixel/flow/issues/new/choose",
              )
            }
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
            onSelect={() =>
              openExternal("https://github.com/leozhengliu-pixel/flow/releases")
            }
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
            onSelect={() =>
              openExternal(
                "https://github.com/leozhengliu-pixel/flow/blob/main/CHANGELOG.md",
              )
            }
          >
            <NewsDot />
            Team initiatives
          </DropdownMenu.Item>
          <DropdownMenu.Item
            className="sidebar-news-item"
            onSelect={() =>
              openExternal(
                "https://github.com/leozhengliu-pixel/flow/blob/main/CHANGELOG.md",
              )
            }
          >
            <NewsDot />
            Coding sessions on mobile
          </DropdownMenu.Item>
          <DropdownMenu.Item
            className="sidebar-news-item"
            onSelect={() =>
              openExternal(
                "https://github.com/leozhengliu-pixel/flow/blob/main/CHANGELOG.md",
              )
            }
          >
            <NewsDot />
            Full changelog
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function KeyboardShortcutsDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const groups = [
    [
      "Navigation",
      [
        ["Search workspace", "⌘ K"],
        ["Go to Inbox", "G then I"],
        ["Go to My issues", "G then M"],
        ["Open settings", "G then S"],
      ],
    ],
    [
      "Create",
      [
        ["Create issue", "C"],
        ["Create project", "N then P"],
        ["Create initiative", "N then I"],
      ],
    ],
    [
      "General",
      [
        ["Close menu or dialog", "Esc"],
        ["Move through menus", "↑ / ↓"],
        ["Choose focused item", "Enter"],
      ],
    ],
  ] as const;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sidebar-shortcuts-dialog">
        <DialogTitle>Keyboard shortcuts</DialogTitle>
        {groups.map(([title, items]) => (
          <section key={title}>
            <h3>{title}</h3>
            {items.map(([label, keys]) => (
              <div key={label}>
                <span>{label}</span>
                <kbd>{keys}</kbd>
              </div>
            ))}
          </section>
        ))}
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
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M12.5 10C13.8807 10 15 11.1193 15 12.5C15 13.8807 13.8807 15 12.5 15C11.1193 15 10 13.8807 10 12.5C10 11.1193 11.1193 10 12.5 10ZM12.5 11.5C11.9477 11.5 11.5 11.9477 11.5 12.5C11.5 13.0523 11.9477 13.5 12.5 13.5C13.0523 13.5 13.5 13.0523 13.5 12.5C13.5 11.9477 13.0523 11.5 12.5 11.5Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M3.5 4.5C3.91414 4.50009 4.25 4.83584 4.25 5.25V14.249C4.24982 14.663 3.91403 14.9989 3.5 14.999C3.0859 14.999 2.75018 14.6631 2.75 14.249V5.25C2.75 4.83579 3.08579 4.5 3.5 4.5Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M10 2.75C11.7949 2.75 13.25 4.20507 13.25 6V10.75C13.25 11.1642 12.9142 11.5 12.5 11.5C12.0858 11.5 11.75 11.1642 11.75 10.75V6C11.75 5.0335 10.9665 4.25 10 4.25H8C7.58579 4.25 7.25 3.91421 7.25 3.5C7.25 3.08579 7.58579 2.75 8 2.75H10Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M3.5 1C4.88071 1 6 2.11929 6 3.5C6 4.88071 4.88071 6 3.5 6C2.11929 6 1 4.88071 1 3.5C1 2.11929 2.11929 1 3.5 1ZM3.5 2.5C2.94772 2.5 2.5 2.94772 2.5 3.5C2.5 4.05228 2.94772 4.5 3.5 4.5C4.05228 4.5 4.5 4.05228 4.5 3.5C4.5 2.94772 4.05228 2.5 3.5 2.5Z"
      />
    </svg>
  );
}
function MyIssuesIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M14.2458 10C14.6255 10 14.9393 10.2822 14.9889 10.6482L14.9958 10.75V12.2475C14.9958 13.7083 13.8567 14.9034 12.4177 14.9922L12.2504 14.9975L10.7513 15C10.3371 15.0007 10.0007 14.6655 10 14.2513C9.99936 13.8716 10.281 13.5573 10.647 13.507L10.7487 13.5L12.2479 13.4975C12.8943 13.4964 13.4255 13.0047 13.4893 12.3751L13.4958 12.2475V10.75C13.4958 10.3358 13.8316 10 14.2458 10ZM1.75 10C2.16421 10 2.5 10.3358 2.5 10.75V12.2475C2.5 12.937 3.05836 13.4963 3.74789 13.4975L5.24703 13.5C5.66125 13.5007 5.99646 13.8371 5.99576 14.2513C5.99506 14.6655 5.65871 15.0007 5.2445 15L3.74535 14.9975C2.22839 14.9949 1 13.7644 1 12.2475V10.75C1 10.3358 1.33579 10 1.75 10ZM8 6C9.10457 6 10 6.89543 10 8C10 9.10457 9.10457 10 8 10C6.89543 10 6 9.10457 6 8C6 6.89543 6.89543 6 8 6ZM10.7513 1L12.2504 1.00254C13.7674 1.0051 14.9958 2.23556 14.9958 3.75253V5.25C14.9958 5.66422 14.66 6 14.2458 6C13.8316 6 13.4958 5.66422 13.4958 5.25V3.75253C13.4958 3.063 12.9374 2.5037 12.2479 2.50253L10.7487 2.5C10.3345 2.4993 9.9993 2.16295 10 1.74873C10.0007 1.33452 10.3371 0.999302 10.7513 1ZM5.24873 1C5.66295 0.999303 5.9993 1.33452 6 1.74873C6.0007 2.16295 5.66548 2.4993 5.25127 2.5L3.75212 2.50253C3.06259 2.5037 2.50424 3.063 2.50424 3.75253V5.25C2.50424 5.66422 2.16845 6 1.75424 6C1.34002 6 1.00424 5.66422 1.00424 5.25V3.75253C1.00424 2.23556 2.23262 1.0051 3.74959 1.00254L5.24873 1Z" />
    </svg>
  );
}
function PulseIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M11.3354 1.08228C11.7059 1.27438 11.8561 1.74156 11.6708 2.12576L9.5593 6.50508C9.39923 6.83707 9.64113 7.22224 10.0097 7.22224H14C14 7.22224 14.2275 7.22219 14.25 7.22219C14.6642 7.22219 15 7.57041 15 7.99996C15 8.4295 14.6642 8.77772 14.25 8.77772C10.4465 8.77772 7.32888 11.131 5.67083 14.5699C5.48559 14.9541 5.03507 15.1098 4.66459 14.9177C4.29411 14.7256 4.14394 14.2584 4.32918 13.8742L6.44081 9.49467C6.60085 9.16275 6.35907 8.77764 5.99059 8.77759C4.57706 8.7774 3.16352 8.77781 1.75 8.77781C1.33579 8.77781 1 8.42959 1 8.00004C1 7.5705 1.33579 7.22228 1.75 7.22228C5.55362 7.22228 8.67116 4.86885 10.3292 1.43003C10.5145 1.04588 10.965 0.890196 11.3354 1.08228Z"
      />
    </svg>
  );
}
function AgentIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4.07132 3.8283C4.04394 3.81721 4.01406 3.81379 3.98488 3.8184C3.95566 3.82301 3.92826 3.83551 3.90561 3.85453C3.88297 3.87356 3.86594 3.8984 3.85636 3.92639C3.84678 3.95437 3.84501 3.98443 3.85124 4.01335L5.80802 13.1405C5.81898 13.1915 5.83884 13.2155 5.85542 13.2298C5.87605 13.2476 5.9078 13.2631 5.94754 13.268C5.98728 13.2729 6.0217 13.2654 6.04578 13.2532C6.06507 13.2434 6.08993 13.2252 6.11273 13.1784L7.83779 9.64746C8.05513 9.20258 8.45077 8.87059 8.92663 8.73378L12.7035 7.64791C12.7535 7.63353 12.776 7.61215 12.789 7.59475C12.8052 7.57307 12.8186 7.54044 12.8207 7.50049C12.8228 7.46054 12.813 7.42669 12.7992 7.40342C12.788 7.38476 12.7681 7.36116 12.7199 7.34158L4.07132 3.8283C4.07129 3.82829 4.07135 3.82832 4.07132 3.8283ZM3.75083 2.33677C4.04945 2.2896 4.35527 2.32474 4.63541 2.43841L13.2843 5.95183C13.2843 5.95184 13.2843 5.95183 13.2843 5.95183C14.747 6.54596 14.6351 8.65343 13.1179 9.08953L9.34109 10.1754C9.27311 10.1949 9.21659 10.2424 9.18554 10.3059L7.46077 13.8363C7.46072 13.8364 7.46082 13.8362 7.46077 13.8363C6.76755 15.2562 4.67275 14.9979 4.34147 13.4555L2.38492 4.3294C2.38489 4.32925 2.38495 4.32956 2.38492 4.3294C2.32134 4.03401 2.33935 3.72642 2.43722 3.44054C2.53514 3.15452 2.70919 2.90061 2.94065 2.70612C3.17211 2.51164 3.45221 2.38394 3.75083 2.33677Z" />
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
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M7.4145 8.3381C7.68162 7.8873 8.31838 7.8873 8.5855 8.3381L11.896 13.925C12.2589 14.5374 11.6035 15.2506 10.9879 14.9132L8.10753 13.3343C8.04032 13.2975 7.95967 13.2975 7.89247 13.3343L5.0121 14.9132C4.39652 15.2506 3.74112 14.5374 4.10401 13.925L7.4145 8.3381Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M13.5 8C13.5 4.96243 11.0376 2.5 8 2.5C4.96243 2.5 2.5 4.96243 2.5 8C2.5 8.96927 2.75037 9.87822 3.18945 10.668L3.38867 10.999L3.42773 11.0654C3.60231 11.4033 3.4953 11.825 3.16992 12.0371C2.84468 12.249 2.41642 12.1766 2.17773 11.8809L2.13281 11.8184L2.00195 11.6104C1.36597 10.5558 1 9.31963 1 8C1 4.13401 4.13401 1 8 1C11.866 1 15 4.13401 15 8C15 9.40749 14.5834 10.7198 13.8672 11.8184L13.8223 11.8809C13.5836 12.1766 13.1553 12.249 12.8301 12.0371C12.4831 11.8109 12.3851 11.346 12.6113 10.999L12.8105 10.668C13.2496 9.87822 13.5 8.96927 13.5 8Z"
      />
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
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M10.9933 10.0137C12.0962 10.0789 12.8652 10.3673 13.4151 10.7998C13.8745 11.1612 14.1329 11.5958 14.2901 11.9014L14.4171 12.1572C14.4895 12.2985 14.5578 12.5105 14.6075 12.6748C14.6647 12.8638 14.725 13.0835 14.7794 13.2881C14.834 13.4937 14.8839 13.6898 14.92 13.834C14.938 13.9059 14.9528 13.9653 14.963 14.0068C14.9681 14.0276 14.972 14.0444 14.9747 14.0557L14.9786 14.0723C15.0761 14.4747 14.8292 14.8809 14.4269 14.9785C14.0243 15.0759 13.619 14.8283 13.5216 14.4258L13.506 14.3652C13.4962 14.3255 13.4824 14.268 13.4649 14.1982C13.4299 14.0584 13.3814 13.8694 13.3292 13.6729C13.2767 13.4754 13.221 13.2747 13.171 13.1094C13.1168 12.9304 13.0863 12.851 13.0831 12.8428L12.9532 12.5869C12.8299 12.3483 12.7123 12.1564 12.4874 11.9795C12.2386 11.7838 11.7871 11.5637 10.9054 11.5117L10.5001 11.5C9.35452 11.5 8.80921 11.7545 8.52451 11.9785C8.2188 12.2193 8.11174 12.4807 7.90342 12.8594C7.908 12.8543 7.87818 12.9233 7.82139 13.1104C7.77177 13.2738 7.71736 13.4731 7.66611 13.6699C7.61516 13.8656 7.56804 14.0541 7.53428 14.1934C7.51748 14.2626 7.50456 14.3199 7.49521 14.3594L7.48057 14.4189C7.38673 14.8224 6.98361 15.0743 6.58018 14.9805C6.17691 14.8866 5.92589 14.4834 6.01963 14.0801L6.03525 14.0137C6.04513 13.9719 6.05972 13.9122 6.07725 13.8398C6.11228 13.6953 6.16022 13.4984 6.21396 13.292C6.26738 13.0869 6.32781 12.8659 6.38584 12.6748C6.43545 12.5114 6.50614 12.2896 6.58896 12.1387C6.72536 11.8909 6.99052 11.2777 7.5958 10.8008C8.22303 10.3069 9.1325 10 10.5001 10L10.9933 10.0137Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M5.71592 6.0127C5.86516 6.02118 6.00774 6.03415 6.14365 6.05078C6.55479 6.10113 6.84732 6.47558 6.79697 6.88672C6.74639 7.29744 6.37281 7.58914 5.96201 7.53906C5.75847 7.51415 5.52088 7.50001 5.24619 7.5C4.09451 7.50011 3.64308 7.75722 3.43955 7.94336C3.32997 8.04366 3.24909 8.16093 3.17197 8.30859C3.13166 8.3858 3.09633 8.46418 3.05283 8.55859C3.01292 8.64521 2.96118 8.75425 2.90342 8.85938C2.908 8.85434 2.87818 8.92333 2.82139 9.11035C2.77177 9.27378 2.71736 9.47313 2.66611 9.66992C2.61516 9.86558 2.56804 10.0541 2.53428 10.1934C2.51748 10.2626 2.50456 10.3199 2.49521 10.3594L2.48057 10.4189C2.38673 10.8224 1.98361 11.0743 1.58018 10.9805C1.17691 10.8866 0.925892 10.4834 1.01963 10.0801L1.03525 10.0137C1.04514 9.97195 1.05972 9.91217 1.07725 9.83984C1.11228 9.69532 1.16022 9.49838 1.21396 9.29199C1.26738 9.08686 1.32781 8.86594 1.38584 8.6748C1.43545 8.5114 1.50614 8.28958 1.58896 8.13867C1.61712 8.08751 1.64705 8.02502 1.69053 7.93066C1.73061 7.84367 1.78222 7.73041 1.84287 7.61426C1.96769 7.37529 2.14427 7.09444 2.42686 6.83594C3.00841 6.30406 3.88547 6.00003 5.24717 6L5.71592 6.0127Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M10.5001 5C11.6047 5 12.5001 5.89543 12.5001 7C12.5001 8.10457 11.6047 9 10.5001 9C9.39558 8.99994 8.5001 8.10453 8.5001 7C8.5001 5.89547 9.39558 5.00006 10.5001 5Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M5.5001 1C6.60467 1 7.5001 1.89543 7.5001 3C7.5001 4.10457 6.60467 5 5.5001 5C4.39558 4.99994 3.5001 4.10453 3.5001 3C3.5001 1.89547 4.39558 1.00006 5.5001 1Z"
      />
    </svg>
  );
}
function CustomersIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M11.0247 12.3333C13.6728 12.3334 14.6225 13.529 14.9606 14.319C15.112 14.6739 14.806 15 14.4046 15H7.59537C7.18784 14.9997 6.8816 14.6641 7.04464 14.3073C7.40663 13.5172 8.38955 12.3333 11.0247 12.3333Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M11 7C12.1045 7 12.9998 7.89543 12.9998 9C12.9998 10.1046 12.1045 11 11 11C9.89553 11 9.00018 10.1046 9.00018 9C9.00018 7.89543 9.89553 7 11 7Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M10 4.25V3.75C10 3.05964 9.44036 2.5 8.75 2.5H3.75C3.05964 2.5 2.5 3.05964 2.5 3.75V13.25C2.5 13.3881 2.61193 13.5 2.75 13.5H4.25C4.66421 13.5 5 13.8358 5 14.25C5 14.6642 4.66421 15 4.25 15H2.75C1.7835 15 1 14.2165 1 13.25V3.75C1 2.23122 2.23122 1 3.75 1H8.75C10.2688 1 11.5 2.23122 11.5 3.75V4.25C11.5 4.66421 11.1642 5 10.75 5C10.3358 5 10 4.66421 10 4.25Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M7.75 4.25C8.16421 4.25 8.5 4.58579 8.5 5C8.5 5.41421 8.16421 5.75 7.75 5.75H4.75C4.33579 5.75 4 5.41421 4 5C4 4.58579 4.33579 4.25 4.75 4.25H7.75Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M6.5 7.25C6.91421 7.25 7.25 7.58579 7.25 8C7.25 8.41421 6.91421 8.75 6.5 8.75H4.75C4.33579 8.75 4 8.41421 4 8C4 7.58579 4.33579 7.25 4.75 7.25H6.5Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M6.5 10.25C6.91421 10.25 7.25 10.5858 7.25 11C7.25 11.4142 6.91421 11.75 6.5 11.75H4.75C4.33579 11.75 4 11.4142 4 11C4 10.5858 4.33579 10.25 4.75 10.25H6.5Z"
      />
    </svg>
  );
}
function DraftIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M13.4326 7.26855C13.7913 6.91034 14.3729 6.91004 14.7314 7.26855C15.09 7.62707 15.0897 8.20868 14.7314 8.56738L9.2373 14.0615C8.68102 14.6176 7.94038 14.9516 7.15527 15C7.06762 15.0052 6.99481 14.9324 7 14.8447C7.04846 14.0597 7.38238 13.319 7.93848 12.7627L13.4326 7.26855Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M10.7539 1C12.5465 1 14 2.4535 14 4.24609C13.9998 4.66016 13.6641 4.99609 13.25 4.99609C12.8359 4.99609 12.5002 4.66016 12.5 4.24609C12.5 3.28193 11.7181 2.5 10.7539 2.5H4.25C3.2835 2.5 2.5 3.2835 2.5 4.25V11.7393C2.50006 12.133 2.7992 12.4569 3.18262 12.4961L4.33789 12.5039C4.71592 12.5425 5.01074 12.8618 5.01074 13.25C5.01074 13.6382 4.71592 13.9575 4.33789 13.9961L3.26074 14L3.0293 13.9883C1.88948 13.8723 1.00006 12.9097 1 11.7393V4.25C1 2.45507 2.45507 1 4.25 1H10.7539Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M6.75 8C7.16421 8 7.5 8.33579 7.5 8.75C7.5 9.16421 7.16421 9.5 6.75 9.5H5.25C4.83579 9.5 4.5 9.16421 4.5 8.75C4.5 8.33579 4.83579 8 5.25 8H6.75Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M9.75 5C10.1642 5 10.5 5.33579 10.5 5.75C10.5 6.16421 10.1642 6.5 9.75 6.5H5.25C4.83579 6.5 4.5 6.16421 4.5 5.75C4.5 5.33579 4.83579 5 5.25 5H9.75Z"
      />
    </svg>
  );
}
function ReleasesIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M8 5C8.17232 5.00007 8.33249 5.08922 8.42383 5.23535L10.9238 9.23535C11.0199 9.38934 11.0254 9.58336 10.9375 9.74219C10.8494 9.90111 10.6817 9.99993 10.5 10H5.5C5.31827 10 5.15063 9.90111 5.0625 9.74219C4.97455 9.58331 4.97999 9.38938 5.07617 9.23535L7.57617 5.23535L7.61328 5.18262C7.70752 5.06783 7.84926 5 8 5Z"
      />
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M11 1C13.2091 1 15 2.79086 15 5V11C15 13.14 13.3194 14.8879 11.2061 14.9951L11 15H5L4.79395 14.9951C2.7488 14.8913 1.10865 13.2512 1.00488 11.2061L1 11V5C1 2.79086 2.79086 1 5 1H11ZM5 2.5C3.61929 2.5 2.5 3.61929 2.5 5V11C2.5 12.3807 3.61929 13.5 5 13.5H11C12.3807 13.5 13.5 12.3807 13.5 11V5C13.5 3.61929 12.3807 2.5 11 2.5H5Z"
      />
    </svg>
  );
}
function LoopsIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M12.312 1.00057C13.0343 1.01281 13.7341 1.22383 14.2553 1.74475C14.2778 1.76732 14.2993 1.79087 14.3207 1.81409C14.7935 2.32713 14.9868 2.99616 14.9985 3.68725C15.0105 4.40468 14.8307 5.19416 14.517 5.99598C14.4588 6.14476 14.3938 6.29444 14.3256 6.4462C14.3154 6.46884 14.3058 6.49186 14.2953 6.51456C14.0735 6.99921 13.8016 7.49678 13.4848 7.99902C13.9162 8.68273 14.2655 9.35891 14.518 10.004C14.8315 10.8056 15.0115 11.5956 14.9994 12.3127C14.9871 13.0351 14.7763 13.7341 14.2553 14.2552C13.7342 14.7763 13.0353 14.9871 12.3129 14.9994C11.5957 15.0115 10.8059 14.8315 10.0044 14.518C9.74973 14.4183 9.49171 14.3001 9.22899 14.1713C9.68651 13.8557 10.1426 13.5097 10.5903 13.1351C11.2601 13.3929 11.8356 13.5079 12.2875 13.5003C12.7431 13.4925 13.0245 13.3648 13.1948 13.1946C13.365 13.0244 13.4926 12.7429 13.5004 12.2874C13.5081 11.8266 13.3891 11.2365 13.1205 10.5499C13.0073 10.2606 12.8688 9.9594 12.7075 9.65048L12.5385 9.33894C12.0888 9.91319 11.5859 10.4841 11.0346 11.0353C9.39854 12.6714 7.60184 13.8894 5.99563 14.518C5.19392 14.8316 4.40437 15.0115 3.68706 14.9994C2.96466 14.9871 2.26581 14.7764 1.7447 14.2552V14.2533L1.74372 14.2543C1.22372 13.7333 1.01288 13.0345 1.00057 12.3127C0.988551 11.5956 1.1685 10.8056 1.48201 10.004C1.73443 9.35898 2.08292 8.68266 2.51422 7.99902C2.51172 7.99505 2.50891 7.99127 2.50641 7.9873C2.29937 7.65838 2.11181 7.33137 1.94489 7.00873C1.93724 6.99397 1.92902 6.97953 1.92145 6.96478C1.84415 6.81383 1.77273 6.66327 1.70466 6.51456C1.69419 6.49172 1.68366 6.46898 1.67341 6.4462C1.60528 6.29455 1.54116 6.14465 1.48298 5.99598C1.45797 5.93204 1.43498 5.86733 1.41169 5.80358C1.36197 5.66737 1.31443 5.53226 1.27302 5.39731C1.15153 5.00159 1.06705 4.6127 1.02693 4.23806C1.0169 4.14436 1.00983 4.05151 1.00545 3.95973C1.00107 3.86786 1.00003 3.77699 1.00154 3.68725C1.00918 3.23548 1.09363 2.79263 1.28377 2.39714C1.35974 2.2391 1.45284 2.08856 1.56404 1.94789C1.61972 1.87747 1.6805 1.80994 1.74567 1.74475C2.26693 1.22347 2.96542 1.01278 3.68804 1.00057C4.40545 0.988507 5.1948 1.16831 5.9966 1.48204C7.60287 2.11071 9.39952 3.32846 11.0356 4.96467L11.4389 5.38071C11.8336 5.8001 12.2007 6.22808 12.5375 6.65812C12.5423 6.64961 12.5474 6.64124 12.5522 6.63273C12.7806 6.22396 12.9718 5.82759 13.1196 5.45005C13.1852 5.2824 13.2413 5.1203 13.2895 4.96467C13.3152 4.88144 13.3389 4.80008 13.3598 4.72051C13.3832 4.6313 13.4029 4.5442 13.4203 4.45975C13.4774 4.18353 13.5031 3.9335 13.4995 3.71264C13.4946 3.42811 13.4432 3.21144 13.3637 3.04756C13.3242 2.96627 13.2768 2.89883 13.226 2.8415C13.2152 2.82927 13.205 2.81562 13.1938 2.80439C13.0233 2.63478 12.7412 2.50739 12.2866 2.49968C11.8354 2.49223 11.2605 2.60606 10.5923 2.86298C10.1523 2.48776 9.70465 2.13695 9.25536 1.81409C9.5087 1.69092 9.7577 1.57822 10.0034 1.48204C10.8049 1.16843 11.5947 0.988618 12.312 1.00057ZM3.71343 2.49968C3.33057 2.50615 3.07121 2.59738 2.89703 2.72723C2.86397 2.75228 2.83345 2.77813 2.80621 2.80536C2.63602 2.97565 2.50834 3.25707 2.50055 3.71264C2.4978 3.87812 2.51157 4.06032 2.54254 4.25759C2.57304 4.45187 2.62175 4.66035 2.68707 4.88263C2.74007 5.06313 2.80328 5.25284 2.88043 5.45005C3.03129 5.83544 3.22669 6.24131 3.46148 6.6591C3.91084 6.08535 4.41366 5.51548 4.96439 4.96467C5.20313 4.72591 5.44625 4.49649 5.69094 4.27615C6.10736 4.50777 6.54636 4.79175 6.99855 5.12093C6.67156 5.4019 6.346 5.70419 6.02492 6.02527C5.37663 6.67367 4.80827 7.34118 4.32768 7.99902C4.80843 8.65718 5.37728 9.326 6.0259 9.97472C6.34092 10.2897 6.66128 10.5862 6.98194 10.8625C6.51397 11.1866 6.05963 11.4559 5.63333 11.6692C5.40899 11.4654 5.18468 11.2546 4.96537 11.0353L4.66361 10.7267C4.6405 10.7026 4.61818 10.6777 4.59525 10.6535C4.56276 10.6191 4.5307 10.5844 4.49857 10.5499C4.39974 10.444 4.30283 10.3375 4.20756 10.2306C4.19177 10.2128 4.1754 10.1956 4.15971 10.1779C3.91436 9.90052 3.68168 9.62015 3.46148 9.33894C3.22626 9.75736 3.03051 10.164 2.87945 10.5499C2.6109 11.2365 2.4919 11.8266 2.49957 12.2874C2.50735 12.7412 2.63403 13.0232 2.80328 13.1937L2.80523 13.1946C2.9755 13.3649 3.25683 13.4925 3.71245 13.5003C4.17331 13.5081 4.76292 13.3891 5.44974 13.1204C6.73591 12.617 8.24738 11.6321 9.68797 10.254L9.9741 9.97374C10.6227 9.32508 11.1916 8.65713 11.6723 7.99902C11.3119 7.50567 10.9016 7.00665 10.4458 6.51456L9.97508 6.02527C8.45007 4.50016 6.82246 3.41657 5.45071 2.87959C4.7638 2.61078 4.17437 2.49196 3.71343 2.49968Z"
      />
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
