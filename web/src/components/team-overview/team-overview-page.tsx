import * as Dialog from "@radix-ui/react-dialog";
import * as Popover from "@radix-ui/react-popover";
import { toggleFavoriteFor } from '@/lib/favorites';
import { teamHierarchy } from '@/lib/team-hierarchy';
import { TeamIcon } from '@/components/issue/issue-icons';
import { newTeamPath } from '@/lib/app-routes';
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  ArrowDownWideNarrow,
  ArrowUpNarrowWide,
  CalendarDays,
  Check,
  ChevronRight,
  FileText,
  FolderKanban,
  Link2,
  Menu,
  Plus,
  Pin,
  Pencil,
  Search,
  Trash2,
  UserRound,
  X,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode,
} from "react";
import { toast } from "sonner";
import { confirmAction } from '@/components/ui/action-dialog-service';
import { TeamSettingsIcon, TeamArchiveIcon as ArchiveIcon } from '@/components/layout/team-menu-icons';
import { isToday, isYesterday, isThisWeek, isSameWeek, subWeeks, isThisMonth, isSameMonth, subMonths, format } from 'date-fns';

import {
  createDocument,
  createTeamResourceSection,
  deleteTeamResource,
  deleteTeamResourceSection,
  fetchTeamResources,
  pinTeamResource,
  setTeamMembership,
  updateTeamResource,
  updateTeamResourceSection,
  updateStructuredTeamSettings,
  updateTeam,
} from "@/lib/api";
import {
  documentPath,
  settingsPath,
  teamDocumentsPath,
  teamHomePath,
  teamInitiativesPath,
  teamIssuesPath,
  teamProjectsPath,
  teamCyclesPath,
  teamLoopsPath,
  teamMembersPath,
  teamViewsPath,
  projectPath,
} from "@/lib/app-routes";
import { DisplayIcon } from "@/components/ui/view-action-icons";
import { VirtualColumnList } from "@/components/ui/virtual-column-list";
import {
  DirectoryFilterMenu,
  type DirectoryFilterGroup,
} from "@/components/workspace-directory/directory-menus";
import { UserAvatar } from "@/components/ui/user-avatar";
import { ProjectIcon, SlackIcon } from "@/components/issue/issue-icons";
import { ViewGlyph, ViewIconPicker } from "@/components/views/view-icon-picker";
import { DocumentGlyph } from "@/components/documents/document-icon";
import { DocumentsEmptyIllustration } from '@/components/documents/documents-empty-illustration';
import { PersonHover } from '@/components/property/person-info';
import { LoopsDirectory } from "@/components/loops/loops-page";
import { workspaceFeatureEnabled } from "@/components/layout/sidebar-customization-state";
import { canManageTeamSettings, viewerOwnsTeam } from "@/lib/settings-permissions";
import { useI18n } from "@/i18n/i18n";
import type {
  BootstrapData,
  FlowDocument,
  Team,
  TeamPinnedResource,
  TeamResourceSection,
} from "@/types/flow";

import "./team-overview-page.css";
import './team-documents.css';

type View = "overview" | "documents" | "loops" | "members";

export function TeamOverviewPage({
  data,
  team,
  view,
  onNavigate,
  onOpenSidebar,
  onReload,
}: {
  data: BootstrapData;
  team: Team;
  view: View;
  onNavigate: (path: string) => void;
  onOpenSidebar: () => void;
  onReload: () => Promise<void>;
}) {
  const {t}=useI18n();
  const loopsEnabled = workspaceFeatureEnabled(
    data.workspaceSettings?.featureFlags,
    "loops",
  );
  const canManageMembers = canManageTeamSettings(data, team.id, "members");
  const hierarchy = useMemo(() => teamHierarchy(data.teams, data.teamSettings), [data.teams, data.teamSettings]);
  const parentTeam = hierarchy.byId.get(data.teamSettings[team.id]?.parentTeamId ?? '');
  const childTeams = (hierarchy.children.get(team.id) ?? []).filter(item => !item.retiredAt);
  const [sections, setSections] = useState<TeamResourceSection[]>([]);
  const [resources, setResources] = useState<TeamPinnedResource[]>([]);
  const [loading, setLoading] = useState(true);
  const creatingDocument = useRef(false);
  const [documentCreating, setDocumentCreating] = useState(false);
  const [resourceOpen, setResourceOpen] = useState(false);
  const [resourceSectionId, setResourceSectionId] = useState('');
  const [collapsedSections, setCollapsedSections] = useState<Set<string>>(() => {
    try { return new Set(JSON.parse(localStorage.getItem(`flow:team:${team.id}:collapsed-sections`) ?? '[]')); } catch { return new Set(); }
  });
  const toggleSection = (id: string, all = false) => setCollapsedSections(current => {
    const next = all ? new Set(current.size ? [] : sections.map(section => section.id)) : new Set(current);
    if (!all) { if (next.has(id)) next.delete(id); else next.add(id); }
    try { localStorage.setItem(`flow:team:${team.id}:collapsed-sections`, JSON.stringify([...next])); } catch { /* Storage is optional. */ }
    return next;
  });
  const [sectionOpen, setSectionOpen] = useState(false);
  const [membersOpen, setMembersOpen] = useState(false);
  const lastSectionHash = useRef('');
  useEffect(() => {
    const reveal = () => {
      const target = sections.find(section => location.hash === `#section-${section.id}`);
      if (!target || lastSectionHash.current === location.hash) return;
      lastSectionHash.current = location.hash;
      setCollapsedSections(current => { const next = new Set(current); next.delete(target.id); return next; });
      requestAnimationFrame(() => document.getElementById(`section-${target.id}`)?.scrollIntoView({ block: 'start' }));
    };
    reveal(); window.addEventListener('hashchange', reveal);
    return () => window.removeEventListener('hashchange', reveal);
  }, [sections]);
  const favorite = data.favorites.some(
    (item) =>
      item.resourceType === "team" &&
      item.resourceId === team.id &&
      item.userId === data.viewer.id,
  );
  const load = useCallback(async () => {
    try {
      const result = await fetchTeamResources(team.id);
      setSections(result.sections);
      setResources(result.resources);
    } finally {
      setLoading(false);
    }
  }, [team.id]);
  useEffect(() => {
    setLoading(true);
    try { setCollapsedSections(new Set(JSON.parse(localStorage.getItem(`flow:team:${team.id}:collapsed-sections`) ?? '[]'))); } catch { setCollapsedSections(new Set()); }
    void load();
  }, [load, team.id]);
  const teamMembers = data.teamMembers
    .filter((item) => item.teamId === team.id)
    .map((item) => data.users.find((user) => user.id === item.userId))
    .filter(Boolean);
  const documents = data.documents.filter(
    (document) =>
      document.teamIds.includes(team.id) ||
      document.projectIds.some((id) =>
        data.projects
          .find((project) => project.id === id)
          ?.teamIds.includes(team.id),
      ),
  );
  const grouped = useMemo(
    () =>
      [
        {
          id: "",
          name: "Team resources",
          position: -1,
          createdAt: "",
          updatedAt: "",
          teamId: team.id,
        },
        ...[...sections].sort((a, b) => a.position - b.position),
      ].map((section) => ({
        section,
        items: resources.filter(
          (item) => (item.sectionId ?? "") === section.id,
        ).sort((a, b) => a.position - b.position),
      })),
    [resources, sections, team.id],
  );
  const reloadResources = async () => {
    await load();
    await onReload();
  };
  const newDocument = async () => {
    if (creatingDocument.current) return;
    creatingDocument.current = true;
    setDocumentCreating(true);
    try {
      const document = await createDocument({
        title: "New document",
        teamIds: [team.id],
      });
      await onReload();
      onNavigate(documentPath(data.workspace.urlKey, document));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not create document",
      );
    } finally {
      creatingDocument.current = false;
      setDocumentCreating(false);
    }
  };
  const newPinnedDocument = async (sectionId = '') => {
    try {
      const document = await createDocument({
        title: "New document",
        teamIds: [team.id],
      });
      await pinTeamResource(team.id, {
        sectionId,
        resourceType: "document",
        resourceId: document.id,
        title: document.title,
      });
      await reloadResources();
      onNavigate(documentPath(data.workspace.urlKey, document));
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not create document",
      );
    }
  };
  const saveTeamVisual = async (visual: { icon: string; color: string }) => {
    await updateTeam(data.workspace.urlKey, team.id, visual);
    await onReload();
  };
  const saveTeamName = async (name: string) => {
    const value = name.trim();
    if (value && value !== team.name) {
      await updateTeam(data.workspace.urlKey, team.id, { name: value });
      await onReload();
    }
  };
  const saveTeamDescription = async (description: string) => {
    await updateStructuredTeamSettings(team.id, {
      description: description.trim(),
    });
    await onReload();
  };
  const teamTabs = (
    <nav className="team-home-tabs" aria-label={t("Team views")}>
      <a
        aria-current={view === "overview" ? "page" : undefined}
        href={teamHomePath(data.workspace.urlKey, team.key)}
        onClick={(event) => {
          event.preventDefault();
          onNavigate(teamHomePath(data.workspace.urlKey, team.key));
        }}
      >
        {t("Overview")}
      </a>
      <a
        aria-current={view === "documents" ? "page" : undefined}
        href={teamDocumentsPath(data.workspace.urlKey, team.key)}
        onClick={(event) => {
          event.preventDefault();
          onNavigate(teamDocumentsPath(data.workspace.urlKey, team.key));
        }}
      >
        {t("Documents")}
      </a>
      {loopsEnabled && (
        <a
          aria-current={view === "loops" ? "page" : undefined}
          href={teamLoopsPath(data.workspace.urlKey, team.key)}
          onClick={(event) => {
            event.preventDefault();
            onNavigate(teamLoopsPath(data.workspace.urlKey, team.key));
          }}
        >
          {t("Loops")}
        </a>
      )}
      <a
        aria-current={view === "members" ? "page" : undefined}
        href={teamMembersPath(data.workspace.urlKey, team.key)}
        onClick={(event) => {
          event.preventDefault();
          onNavigate(teamMembersPath(data.workspace.urlKey, team.key));
        }}
      >
        {t("Members")}
      </a>
    </nav>
  );
  return (
    <main className="main-panel team-home-page">
      <header className="team-home-topbar">
        <button
          className="team-home-mobile"
          aria-label="Open sidebar"
          data-sidebar-trigger onClick={onOpenSidebar}
        >
          <Menu />
        </button>
        <ViewGlyph
          className="team-home-top-icon"
          color={team.color}
          icon={team.icon || "Team"}
        />
        <h2 data-i18n-ignore>{team.name}</h2>
        <div className="team-home-top-actions">
          <button
            aria-label={favorite ? "Remove from favorites" : "Add to favorites"}
            aria-checked={favorite}
            role="switch"
            onClick={() => void toggleFavoriteFor(data, "team", team.id, undefined, favorite)}
          >
            <FavoriteIcon filled={favorite} />
          </button>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button aria-label="Team actions">
                <TeamMoreIcon />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content data-flow-motion="floating"
                align="start"
                className="team-home-menu"
                sideOffset={4}
              >
                <DropdownMenu.Item asChild>
                  <a
                    href={settingsPath(data.workspace.urlKey, "team", team.key)}
                    onClick={(event) => {
                      event.preventDefault();
                      onNavigate(
                        settingsPath(data.workspace.urlKey, "team", team.key),
                      );
                    }}
                  >
                    <TeamSettingsIcon />
                    {t("Team settings")}
                  </a>
                </DropdownMenu.Item>
                <DropdownMenu.Item
                  onSelect={() =>
                    onNavigate(
                      `/${encodeURIComponent(data.workspace.urlKey)}/team/${encodeURIComponent(team.key)}/archive/issues`,
                    )
                  }
                >
                  <ArchiveIcon />
                  {t("Open archive")}
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        </div>
        <button
          className="team-home-copy-url"
          aria-label="Copy team URL"
          onClick={() =>
            void navigator.clipboard.writeText(
              `${location.origin}${teamHomePath(data.workspace.urlKey, team.key)}`,
            )
          }
        >
          <CopyLinkIcon />
        </button>
      </header>
      {teamTabs}
      {view === "overview" ? (
        <div className="team-home-scroll">
          <div className="team-home-overview-main">
            <section className="team-home-intro">
              <ViewIconPicker
                ariaLabel={t("Choose team icon")}
                color={team.color}
                icon={team.icon || "Team"}
                onChange={(visual) => void saveTeamVisual(visual)}
                prependTeam
                triggerClassName="team-home-large-glyph"
              />
              <h2
                data-i18n-ignore
                contentEditable
                suppressContentEditableWarning
                role="textbox"
                aria-label="Team name"
                onBlur={(event) =>
                  void saveTeamName(event.currentTarget.textContent ?? "")
                }
              >
                {team.name}
              </h2>
              <p
                data-placeholder={t("Add a description…")}
                contentEditable
                suppressContentEditableWarning
                role="textbox"
                aria-label="Team description"
                onBlur={(event) =>
                  void saveTeamDescription(
                    event.currentTarget.textContent ?? "",
                  )
                }
              >
                {data.teamSettings[team.id]?.description ?? ""}
              </p>
            </section>
            <section className="team-resources">
              <header>
                <h2>{t("Team resources")}</h2>
                <span />
                <ResourceCommandMenu
                  documents={documents}
                  onLink={() => { setResourceSectionId(''); setResourceOpen(true); }}
                  onNew={() => void newPinnedDocument()}
                  onSaved={reloadResources}
                  resources={resources}
                  team={team}
                />
                <button
                  aria-label={t("Add section")}
                  className="team-resource-command"
                  title={t("Add section")}
                  onClick={() => setSectionOpen(true)}
                >
                  <AddSectionIcon />
                </button>
              </header>
              {loading ? (
                <p className="team-resource-empty">{t("Loading…")}</p>
              ) : resources.length === 0 && sections.length === 0 ? (
                <p className="team-resource-empty">
                  {t("Add documents and links. Organize by creating sections.")}
                </p>
              ) : (
                grouped.map(
                  ({ section, items }) =>
                    (section.id || items.length > 0) && (
                      <ResourceSection
                        documents={documents.filter(document => !document.archivedAt)}
                        resources={resources}
                        collapsed={collapsedSections.has(section.id)}
                        onToggle={all => toggleSection(section.id, all)}
                        onNew={() => void newPinnedDocument(section.id)}
                        onLink={() => { setResourceSectionId(section.id); setResourceOpen(true); }}
                        data={data}
                        items={items}
                        key={section.id || "unsectioned"}
                        onNavigate={onNavigate}
                        onReload={reloadResources}
                        sections={sections}
                        section={section.id ? section : undefined}
                        team={team}
                      />
                    ),
                )
              )}
              {sectionOpen && (
                <NewSectionRow
                  onCancel={() => setSectionOpen(false)}
                  onSave={async (name) => {
                    await createTeamResourceSection(team.id, name);
                    setSectionOpen(false);
                    await reloadResources();
                  }}
                />
              )}
            </section>
          </div>
          <aside className="team-home-rail">
            {parentTeam && <section className="team-home-hierarchy"><span>{t('Parent team')}</span><a title={hierarchy.path(parentTeam.id)} href={teamHomePath(data.workspace.urlKey, parentTeam.key)} onClick={event => {event.preventDefault(); onNavigate(teamHomePath(data.workspace.urlKey, parentTeam.key));}}><TeamIcon team={parentTeam} size={18}/><span>{parentTeam.name}</span></a></section>}
            <section className="team-home-hierarchy"><span>{t('Sub-teams')}</span>
              {childTeams.map(child => <a key={child.id} href={teamHomePath(data.workspace.urlKey, child.key)} onClick={event => {event.preventDefault(); onNavigate(teamHomePath(data.workspace.urlKey, child.key));}}><TeamIcon team={child} size={18}/><span>{child.name}</span><small>{child.key}</small></a>)}
              {(data.viewerRole === 'admin' || data.viewerRole === 'owner') && !hierarchy.parentError('', team.id) && <button type="button" onClick={() => onNavigate(`${newTeamPath(data.workspace.urlKey)}?parentTeamId=${encodeURIComponent(team.id)}`)}><Plus size={16}/>{t('Create sub-team')}</button>}
            </section>
            <section className="team-home-members">
              <span>{t("Members")}</span>
              <div className="team-home-member-row">
                <a
                  aria-label={teamMembers
                    .map((user) => user!.displayName)
                    .join(" ")}
                  href={teamMembersPath(data.workspace.urlKey, team.key)}
                  onClick={(event) => {
                    event.preventDefault();
                    onNavigate(teamMembersPath(data.workspace.urlKey, team.key));
                  }}
                >
                  <span className="team-home-avatar-stack">
                    {teamMembers.slice(0, 8).map((user, index) => (
                      <UserAvatar
                        avatarUrl={user!.avatarUrl}
                        color={memberColor(index)}
                        key={user!.id}
                        name={user!.displayName}
                      />
                    ))}
                  </span>
                  <ChevronHomeIcon />
                </a>
                {canManageMembers && <button
                  aria-label={t("Add members")}
                  className="team-home-add-members"
                  title={t("Add members")}
                  onClick={() => setMembersOpen(true)}
                >
                  <PlusIcon />
                </button>}
              </div>
            </section>
            <section className="team-home-shortcuts">
              <span>{t("Go to")}</span>
              <div>
                <a
                  href={settingsPath(
                    data.workspace.urlKey,
                    "team",
                    team.key,
                    "notifications",
                  )}
                  onClick={(event) => {
                    event.preventDefault();
                    onNavigate(
                      settingsPath(
                        data.workspace.urlKey,
                        "team",
                        team.key,
                        "notifications",
                      ),
                    );
                  }}
                >
                  <SlackIcon />
                  {t("Connect channel")}
                  <ChevronHomeIcon />
                </a>
                <TeamShortcut
                  href={settingsPath(data.workspace.urlKey, "team", team.key)}
                  icon={<TeamSettingsIcon />}
                  label={t("Team settings")}
                  onNavigate={onNavigate}
                />
                <TeamShortcut
                  href={teamIssuesPath(data.workspace.urlKey, team.key)}
                  icon={<IssuesHomeIcon />}
                  label={t("Issues")}
                  onNavigate={onNavigate}
                />
                <TeamShortcut
                  href={teamCyclesPath(data.workspace.urlKey, team.key)}
                  icon={<TeamCycleIcon />}
                  label={t("Cycles")}
                  onNavigate={onNavigate}
                />
                <TeamShortcut
                  href={teamInitiativesPath(data.workspace.urlKey, team.key)}
                  icon={<InitiativeHomeIcon />}
                  label={t("Initiatives")}
                  onNavigate={onNavigate}
                />
                <TeamShortcut
                  href={teamProjectsPath(data.workspace.urlKey, team.key)}
                  icon={<ProjectIcon />}
                  label={t("Projects")}
                  onNavigate={onNavigate}
                />
                <TeamShortcut
                  href={teamViewsPath(data.workspace.urlKey, team.key)}
                  icon={<ViewGlyph color="currentColor" icon="CustomView" />}
                  label={t("Views")}
                  onNavigate={onNavigate}
                />
              </div>
            </section>
          </aside>
        </div>
      ) : view === "documents" ? (
        <TeamDocuments
          key={team.id}
          creating={documentCreating}
          data={data}
          documents={documents}
          onNavigate={onNavigate}
          onNew={() => void newDocument()}
          onReloadResources={load}
          resources={resources}
          team={team}
        />
      ) : view === "loops" ? (
        loopsEnabled ? (
          <LoopsDirectory
            data={data}
            embedded
            onNavigate={onNavigate}
            onOpenSidebar={onOpenSidebar}
            onReload={onReload}
            teamId={team.id}
          />
        ) : (
          <div className="state-fill">
            <strong>{t("Loops is unavailable")}</strong>
            <span>{t("This feature is turned off for the workspace.")}</span>
          </div>
        )
      ) : (
        <TeamMembersDirectory
          data={data}
          onAdd={() => setMembersOpen(true)}
          onNavigate={onNavigate}
          onReload={onReload}
          team={team}
        />
      )}
      {resourceOpen && (
        <AddLinkDialog
          sectionId={resourceSectionId}
          team={team}
          onClose={() => setResourceOpen(false)}
          onSaved={async () => {
            setResourceOpen(false);
            await reloadResources();
          }}
        />
      )}
      {membersOpen && (
        <AddMembersDialog
          data={data}
          team={team}
          onClose={() => setMembersOpen(false)}
          onSaved={async () => {
            setMembersOpen(false);
            await onReload();
          }}
        />
      )}
    </main>
  );
}

function TeamShortcut({
  href,
  icon,
  label,
  onNavigate,
}: {
  href: string;
  icon: ReactNode;
  label: string;
  onNavigate: (path: string) => void;
}) {
  return (
    <a
      href={href}
      onClick={(event) => {
        event.preventDefault();
        onNavigate(href);
      }}
    >
      {icon}
      <span>{label}</span>
      <ChevronHomeIcon />
    </a>
  );
}

function memberColor(index: number) {
  return ["#a84665", "#d68a3a", "#21aab8", "#5e6ad2"][index % 4];
}

function SvgIcon({
  children,
  className,
  size = 16,
}: {
  children: ReactNode;
  className?: string;
  size?: number;
}) {
  return (
    <svg
      aria-hidden="true"
      className={className}
      fill="currentColor"
      focusable="false"
      height={size}
      viewBox="0 0 16 16"
      width={size}
    >
      {children}
    </svg>
  );
}

function FavoriteIcon({ filled = false }: { filled?: boolean }) {
  return (
    <SvgIcon size={14}>
      <path
        fill={filled ? "currentColor" : undefined}
        d="M10.5193 4.98997L9.46118 2.01693C9.34483 1.70806 9.1452 1.45362 8.88451 1.27433C8.62466 1.09562 8.31641 1 8.00081 1C7.68521 1 7.37696 1.09562 7.11712 1.27433C6.85642 1.45362 6.65679 1.70806 6.54528 2.00374L5.48248 4.98997L2.55536 4.98997C2.23765 4.98973 1.92683 5.08675 1.66556 5.26809C1.40342 5.45004 1.20379 5.70812 1.09414 6.00737C0.984248 6.30728 0.970192 6.63372 1.05394 6.94194C1.13753 7.2496 1.31442 7.52386 1.56019 7.7275L4.08545 9.80411L3.02371 12.9604C2.91854 13.2733 2.91647 13.6112 3.01776 13.9252C3.11884 14.2385 3.3175 14.5113 3.58464 14.7044C3.85102 14.8969 4.17178 15.0003 4.50071 14.9996C4.82872 14.9993 5.14907 14.8951 5.41483 14.702L8.00053 12.8223L10.5851 14.7014C10.8496 14.8944 11.17 14.9991 11.4991 15C11.8281 15.0009 12.1491 14.8978 12.4157 14.7054C12.6831 14.5124 12.882 14.2394 12.9833 13.926C13.0848 13.6113 13.0827 13.2731 12.9773 12.9602L11.9156 9.80207L14.444 7.72408C14.695 7.51166 14.8686 7.23684 14.9493 6.92968C15.0168 6.67352 15.0167 6.40505 14.9504 6.15011L14.9022 5.99753C14.791 5.70157 14.5918 5.44667 14.3314 5.26673C14.0718 5.08736 13.7637 4.9909 13.4479 4.98998L10.5193 4.98997ZM13.4986 6.54821C13.4962 6.55733 13.491 6.56562 13.4832 6.57224L10.7049 8.85551C10.546 8.98629 10.4307 9.16168 10.3739 9.35896C10.3168 9.55714 10.3214 9.76807 10.3875 9.96371L11.5556 13.4385C11.5586 13.4474 11.5587 13.4565 11.5559 13.4652C11.553 13.4741 11.5467 13.4827 11.5378 13.4891C11.5281 13.4961 11.5159 13.5 11.503 13.5C11.4902 13.5 11.4779 13.496 11.4683 13.4889L8.60012 11.4036C8.42554 11.2769 8.21577 11.2088 8.00055 11.2088C7.78531 11.2088 7.5755 11.2769 7.40134 11.4034L4.53289 13.4886C4.52321 13.4957 4.511 13.4996 4.49835 13.4996C4.48523 13.4997 4.47312 13.4958 4.46329 13.4887C4.45442 13.4822 4.44826 13.4738 4.4453 13.4646C4.44255 13.4561 4.4426 13.4471 4.44547 13.4386L5.61393 9.96499C5.67961 9.76981 5.68428 9.5592 5.62728 9.3612C5.57043 9.16375 5.45499 8.98835 5.29643 8.85789L2.51507 6.57069C2.50925 6.56586 2.50387 6.55753 2.50146 6.54865C2.49919 6.54032 2.49957 6.53163 2.50257 6.52343C2.50583 6.51453 2.5121 6.50643 2.52085 6.50035C2.53046 6.49368 2.54238 6.48996 2.55479 6.48997H5.8221C6.03248 6.4897 6.23685 6.42501 6.40824 6.30453C6.58053 6.18341 6.71109 6.01179 6.78158 5.81318L7.9609 2.49821C7.97668 2.50367 7.98851 2.5 8.00081 2.5C8.01311 2.5 8.02494 2.50367 8.03451 2.51025C8.04324 2.51625 8.04952 2.52427 8.05284 2.53307L9.22029 5.81379C9.29053 6.01192 9.42137 6.18383 9.59407 6.30503C9.76589 6.4256 9.97082 6.49011 10.1806 6.48997H13.4457C13.4563 6.49001 13.4686 6.49385 13.4786 6.50077C13.4962 6.5114 13.5007 6.54022 13.4986 6.54821Z"
      />
    </SvgIcon>
  );
}

function TeamMoreIcon() {
  return (
    <SvgIcon size={14}>
      <path d="M3 6.5a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Zm5 0a1.5 1.5 0 1 1 0 3 1.5 1.5 0 0 1 0-3Z" />
    </SvgIcon>
  );
}

function ResourceChevronIcon() {
  return <SvgIcon size={16}><path d="M7.00194 10.6239C6.66861 10.8183 6.25 10.5779 6.25 10.192V5.80802C6.25 5.42212 6.66861 5.18169 7.00194 5.37613L10.7596 7.56811C11.0904 7.76105 11.0904 8.23895 10.7596 8.43189L7.00194 10.6239Z"/></SvgIcon>;
}

function CopyLinkIcon() {
  return (
    <SvgIcon size={14}>
      <path d="M9.30558 10.206C9.57224 10.4726 9.59447 10.8912 9.37225 11.1831L6.84751 13.7175C5.58692 14.9781 3.54311 14.9781 2.28252 13.7175C1.02193 12.4569 1.02193 10.4131 2.28252 9.15251L4.74059 6.69443C5.0315 6.40353 5.50315 6.40353 5.79405 6.69443C6.08496 6.98534 6.08496 7.45699 5.79405 7.74789L3.33598 10.206C2.6572 10.8847 2.6572 11.9853 3.33598 12.664C4.01476 13.3428 5.11527 13.3428 5.79405 12.664L8.25212 10.206C8.54303 9.91506 9.01468 9.91506 9.30558 10.206ZM9.82982 6.17019C10.1207 6.46109 10.1207 6.93274 9.82982 7.22365L7.34921 9.70427C7.0583 9.99518 6.58665 9.99518 6.29575 9.70427C6.00484 9.41337 6.00484 8.94172 6.29575 8.65081L8.77637 6.17019C9.06727 5.87928 9.53892 5.87928 9.82982 6.17019ZM13.7175 2.2825C14.9781 3.54309 14.9781 5.5869 13.7175 6.84749L11.2594 9.30557C10.9685 9.59647 10.4969 9.59647 10.206 9.30557C9.91506 9.01466 9.91506 8.54301 10.206 8.25211L12.664 5.79403C13.3428 5.11525 13.3428 4.01474 12.664 3.33596C11.9853 2.65718 10.8847 2.65718 10.206 3.33596L7.74791 5.79403C7.457 6.08494 6.98535 6.08494 6.69445 5.79403C6.40354 5.50313 6.40354 5.03148 6.69445 4.74057L9.15252 2.2825C10.4131 1.02191 12.4569 1.02191 13.7175 2.2825Z" />
    </SvgIcon>
  );
}

function PlusIcon() {
  return (
    <SvgIcon>
      <path d="M8.75 4C8.75 3.58579 8.41421 3.25 8 3.25C7.58579 3.25 7.25 3.58579 7.25 4V7.25H4C3.58579 7.25 3.25 7.58579 3.25 8C3.25 8.41421 3.58579 8.75 4 8.75H7.25V12C7.25 12.4142 7.58579 12.75 8 12.75C8.41421 12.75 8.75 12.4142 8.75 12V8.75H12C12.4142 8.75 12.75 8.41421 12.75 8C12.75 7.58579 12.4142 7.25 12 7.25H8.75V4Z" />
    </SvgIcon>
  );
}

function AddSectionIcon() {
  return (
    <SvgIcon>
      <path fillRule="evenodd" clipRule="evenodd" d="M8 6.75C8.41421 6.75 8.75 7.08579 8.75 7.5V8.74512L9.99902 8.74414C10.4132 8.7436 10.7494 9.07899 10.75 9.49316C10.7505 9.90732 10.4151 10.2435 10.001 10.2441L8.75 10.2451V11.5C8.75 11.9142 8.41421 12.25 8 12.25C7.58579 12.25 7.25 11.9142 7.25 11.5V10.248L6.00098 10.25C5.58682 10.2505 5.25061 9.91515 5.25 9.50098C5.24946 9.08682 5.58485 8.75061 5.99902 8.75L7.25 8.74805V7.5C7.25 7.08579 7.58579 6.75 8 6.75Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M12.75 4C13.9926 4 15 5.00736 15 6.25V12.75C15 13.9926 13.9926 15 12.75 15H3.25C2.00736 15 1 13.9926 1 12.75V6.25C1 5.00736 2.00736 4 3.25 4H12.75ZM3.25 5.5C2.83579 5.5 2.5 5.83579 2.5 6.25V12.75C2.5 13.1642 2.83579 13.5 3.25 13.5H12.75C13.1642 13.5 13.5 13.1642 13.5 12.75V6.25C13.5 5.83579 13.1642 5.5 12.75 5.5H3.25Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M12.25 1C12.6642 1 13 1.33579 13 1.75C13 2.16421 12.6642 2.5 12.25 2.5H3.75C3.33579 2.5 3 2.16421 3 1.75C3 1.33579 3.33579 1 3.75 1H12.25Z" />
    </SvgIcon>
  );
}

function IssuesHomeIcon() {
  return (
    <SvgIcon>
      <path fillRule="evenodd" clipRule="evenodd" d="M13.25 5.25C14.2165 5.25 15 6.0335 15 7V11.75C15 13.5449 13.5449 15 11.75 15H6.75C5.7835 15 5 14.2165 5 13.25C5 12.8358 5.33579 12.5 5.75 12.5C6.16421 12.5 6.5 12.8358 6.5 13.25C6.5 13.3881 6.61193 13.5 6.75 13.5H11.75C12.7165 13.5 13.5 12.7165 13.5 11.75V7C13.5 6.86193 13.3881 6.75 13.25 6.75C12.8358 6.75 12.5 6.41421 12.5 6C12.5 5.58579 12.8358 5.25 13.25 5.25Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M8.1543 1.00391C9.73945 1.08421 11 2.39489 11 4V8L10.9961 8.1543C10.9184 9.68834 9.68834 10.9184 8.1543 10.9961L8 11H4L3.8457 10.9961C2.31166 10.9184 1.08163 9.68834 1.00391 8.1543L1 8V4C1 2.39489 2.26055 1.08421 3.8457 1.00391L4 1H8L8.1543 1.00391ZM4 2.5C3.17157 2.5 2.5 3.17157 2.5 4V8C2.5 8.82843 3.17157 9.5 4 9.5H8C8.82843 9.5 9.5 8.82843 9.5 8V4C9.5 3.17157 8.82843 2.5 8 2.5H4Z" />
    </SvgIcon>
  );
}

function TeamCycleIcon() {
  return (
    <SvgIcon>
      <path d="M8 1C8.41421 1 8.75 1.33579 8.75 1.75C8.75 2.16421 8.41421 2.5 8 2.5C4.96243 2.5 2.5 4.96243 2.5 8C2.5 11.0376 4.96243 13.5 8 13.5C11.0376 13.5 13.5 11.0376 13.5 8C13.5 6.23619 12.6701 4.66687 11.377 3.65918C11.0503 3.40457 10.9925 2.93313 11.2471 2.60645C11.5017 2.27972 11.9731 2.22098 12.2998 2.47559C13.9418 3.75527 15 5.75433 15 8C15 11.866 11.866 15 8 15C4.13401 15 1 11.866 1 8C1 4.13401 4.13401 1 8 1Z" />
      <path d="M6.85547 6.1123C7.07646 5.97571 7.35255 5.96305 7.58496 6.0791L10.085 7.3291C10.3391 7.45614 10.5 7.71579 10.5 8C10.5 8.28421 10.3391 8.54386 10.085 8.6709L7.58496 9.9209C7.35255 10.0369 7.07646 10.0243 6.85547 9.8877C6.63458 9.75099 6.5 9.50981 6.5 9.25V6.75C6.5 6.49019 6.63458 6.24901 6.85547 6.1123Z" />
    </SvgIcon>
  );
}

function InitiativeHomeIcon() {
  return (
    <SvgIcon>
      <path fillRule="evenodd" clipRule="evenodd" d="M7.4145 8.3381C7.68162 7.8873 8.31838 7.8873 8.5855 8.3381L11.896 13.925C12.2589 14.5374 11.6035 15.2506 10.9879 14.9132L8.10753 13.3343C8.04032 13.2975 7.95967 13.2975 7.89247 13.3343L5.0121 14.9132C4.39652 15.2506 3.74112 14.5374 4.10401 13.925L7.4145 8.3381Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M13.5 8C13.5 4.96243 11.0376 2.5 8 2.5C4.96243 2.5 2.5 4.96243 2.5 8C2.5 8.96927 2.75037 9.87822 3.18945 10.668L3.38867 10.999C3.60231 11.4033 3.4953 11.825 3.16992 12.0371C2.84468 12.249 2.41642 12.1766 2.17773 11.8809C1.43197 10.782 1 9.44952 1 8C1 4.13401 4.13401 1 8 1C11.866 1 15 4.13401 15 8C15 9.40749 14.5834 10.7198 13.8672 11.8184C13.5836 12.1766 13.1553 12.249 12.8301 12.0371C12.4831 11.8109 12.3851 11.346 12.6113 10.999L12.8105 10.668C13.2496 9.87822 13.5 8.96927 13.5 8Z" />
    </SvgIcon>
  );
}

function ChevronHomeIcon() {
  return (
    <SvgIcon size={14}>
      <path d="M5.46967 11.4697C5.17678 11.7626 5.17678 12.2374 5.46967 12.5303C5.76256 12.8232 6.23744 12.8232 6.53033 12.5303L10.5303 8.53033C10.8207 8.23999 10.8236 7.77014 10.5368 7.47624L6.63419 3.47624C6.34492 3.17976 5.87009 3.17391 5.57361 3.46318C5.27713 3.75244 5.27128 4.22728 5.56054 4.52376L8.94583 7.99351L5.46967 11.4697Z" />
    </SvgIcon>
  );
}

function NewDocumentIcon() {
  return (
    <SvgIcon>
      <path d="M8.21191 0.782654C8.78407 0.839556 9.32228 1.09224 9.73242 1.50238L13.2676 5.03754C13.728 5.49799 13.9888 6.11977 13.998 6.76996H8.75C7.7835 6.76996 7 5.98646 7 5.01996V2.26996H4.5C3.94771 2.26996 3.5 2.71767 3.5 3.26996V12.27C3.5 12.8222 3.94772 13.27 4.5 13.27H6.25C6.66421 13.27 7 13.6057 7 14.02C7 14.4342 6.66421 14.77 6.25 14.77H4.5C3.11929 14.77 2 13.6507 2 12.27V3.26996C2 1.88925 3.11929 0.769958 4.5 0.769958H7.96484L8.21191 0.782654ZM8.5 5.01996C8.5 5.15803 8.61193 5.26996 8.75 5.26996H11.3789L8.67188 2.56293C8.61953 2.51058 8.56157 2.46525 8.5 2.42621V5.01996Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M11.5 7.76996C11.9142 7.76996 12.25 8.10575 12.25 8.51996V10.52H14.25C14.6642 10.52 15 10.8558 15 11.27C15 11.6842 14.6642 12.02 14.25 12.02H12.25V14.02C12.25 14.4342 11.9142 14.77 11.5 14.77C11.0858 14.77 10.75 14.4342 10.75 14.02V12.02H8.75C8.33579 12.02 8 11.6842 8 11.27C8 10.8558 8.33579 10.52 8.75 10.52H10.75V8.51996C10.75 8.10575 11.0858 7.76996 11.5 7.76996Z" />
    </SvgIcon>
  );
}

function PageIcon() {
  return (
    <SvgIcon>
      <use href="#Page" />
    </SvgIcon>
  );
}

function NewLinkIcon() {
  return (
    <SvgIcon>
      <path fillRule="evenodd" clipRule="evenodd" d="M11.5001 7.99998C11.9143 7.99998 12.2501 8.33577 12.2501 8.74998V10.75H14.2501C14.6643 10.75 15.0001 11.0858 15.0001 11.5C15.0001 11.9142 14.6643 12.25 14.2501 12.25H12.2501V14.25C12.2501 14.6642 11.9143 15 11.5001 15C11.0859 15 10.7501 14.6642 10.7501 14.25V12.25H8.7501C8.33589 12.25 8.0001 11.9142 8.0001 11.5C8.0001 11.0858 8.33589 10.75 8.7501 10.75H10.7501V8.74998C10.7501 8.33577 11.0859 7.99998 11.5001 7.99998Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M4.44444 6.44432C4.73727 6.15149 5.21208 6.15162 5.50499 6.44432C5.79788 6.73721 5.79788 7.21197 5.50499 7.50486L3.00499 10.0049C2.33146 10.6784 2.33158 11.7707 3.00499 12.4443C3.67859 13.1179 4.77083 13.1179 5.44444 12.4443L5.69444 12.1943C5.98727 11.9015 6.46208 11.9016 6.75499 12.1943C7.04788 12.4872 7.04788 12.962 6.75499 13.2549L6.50499 13.5049C5.24559 14.7642 3.20383 14.7643 1.94444 13.5049C0.685249 12.2455 0.685125 10.2036 1.94444 8.94432L4.44444 6.44432Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M8.94444 1.94432C10.2038 0.685084 12.2456 0.685045 13.505 1.94432C14.7643 3.20367 14.7642 5.24546 13.505 6.50486L13.255 6.75486C12.9621 7.04776 12.4873 7.04776 12.1944 6.75486C11.9016 6.46197 11.9016 5.98719 12.1944 5.69432L12.4444 5.44432C13.1179 4.7707 13.118 3.67843 12.4444 3.00486C11.7709 2.33138 10.6786 2.33142 10.005 3.00486L7.50499 5.50486C7.21209 5.79776 6.73733 5.79776 6.44444 5.50486C6.15162 5.21197 6.15157 4.73719 6.44444 4.44432L8.94444 1.94432Z" />
    </SvgIcon>
  );
}

function CloseIcon() {
  return (
    <SvgIcon size={14}>
      <path d="M3.46967 3.46967C3.76256 3.17678 4.23744 3.17678 4.53033 3.46967L8 6.93934L11.4697 3.46967C11.7626 3.17678 12.2374 3.17678 12.5303 3.46967C12.8232 3.76256 12.8232 4.23744 12.5303 4.53033L9.06066 8L12.5303 11.4697C12.8232 11.7626 12.8232 12.2374 12.5303 12.5303C12.2374 12.8232 11.7626 12.8232 11.4697 12.5303L8 9.06066L4.53033 12.5303C4.23744 12.8232 3.76256 12.8232 3.46967 12.5303C3.17678 12.2374 3.17678 11.7626 3.46967 11.4697L6.93934 8L3.46967 4.53033C3.17678 4.23744 3.17678 3.76256 3.46967 3.46967Z" />
    </SvgIcon>
  );
}

function SelectChevronIcon() {
  return (
    <SvgIcon size={10}>
      <path d="M3.46967 5.46967C3.76256 5.17678 4.23744 5.17678 4.53033 5.46967L8 8.93934L11.4697 5.46967C11.7626 5.17678 12.2374 5.17678 12.5303 5.46967C12.8232 5.76256 12.8232 6.23744 12.5303 6.53033L8.53033 10.5303C8.23744 10.8232 7.76256 10.8232 7.46967 10.5303L3.46967 6.53033C3.17678 6.23744 3.17678 5.76256 3.46967 5.46967Z" />
    </SvgIcon>
  );
}

function ResourceSection({
  documents, resources, collapsed, onToggle, onNew, onLink,
  data,
  items,
  onNavigate,
  onReload,
  sections,
  section,
  team,
}: {
  documents: FlowDocument[];
  resources: TeamPinnedResource[];
  collapsed: boolean;
  onToggle: (all: boolean) => void;
  onNew: () => void;
  onLink: () => void;
  data: BootstrapData;
  items: TeamPinnedResource[];
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  sections: TeamResourceSection[];
  section?: TeamResourceSection;
  team: Team;
}) {
  const [editing, setEditing] = useState(false),
    [name, setName] = useState(section?.name ?? "");
  const [menuOpen, setMenuOpen] = useState(false);
  const { t } = useI18n();
  const saving = useRef(false), cancelled = useRef(false);
  const beginRename = () => { cancelled.current = false; setName(section?.name ?? ''); setEditing(true); };
  const save = async () => {
    if (!section || saving.current || cancelled.current) return;
    if (!name.trim() || name.trim() === section.name) { setEditing(false); return; }
    saving.current = true;
    try { await updateTeamResourceSection(team.id, section.id, { name: name.trim() }); setEditing(false); await onReload(); }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not rename section')); }
    finally { saving.current = false; }
  };
  const remove = async () => {
    if (!section) return;
    if (items.length && !await confirmAction(`Delete "${section.name}"?`, { description: t('Resources in this section will stay pinned to the page.'), confirmLabel: t('Delete section'), danger: true })) return;
    try { await deleteTeamResourceSection(team.id, section.id); await onReload(); }
    catch (error) { toast.error(error instanceof Error ? error.message : t('Could not delete section')); }
  };
  return (
    <div className={`team-resource-section${section ? ' is-named' : ''}`} id={section ? `section-${section.id}` : undefined}
      onDragOver={event => { if (event.dataTransfer.types.some(type => type === 'application/x-flow-team-resource' || type === 'application/x-flow-team-section')) { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; } }}
      onDrop={event => {
        const resourceId = event.dataTransfer.getData('application/x-flow-team-resource');
        const sectionId = event.dataTransfer.getData('application/x-flow-team-section');
        if (resourceId && resources.some(resource => resource.id === resourceId)) {
          event.preventDefault(); event.stopPropagation();
          void updateTeamResource(team.id, resourceId, { sectionId: section?.id ?? '', position: Math.max(-1, ...items.map(item => item.position)) + 1 }).then(onReload).catch(() => toast.error(t('Could not move resource')));
        } else if (section && sectionId !== section.id && sections.some(item => item.id === sectionId)) {
          event.preventDefault(); event.stopPropagation();
          const ordered = sections.filter(item => item.id !== sectionId).sort((a, b) => a.position - b.position);
          const after = event.clientY > event.currentTarget.getBoundingClientRect().top + event.currentTarget.clientHeight / 2;
          const index = ordered.findIndex(item => item.id === section.id) + (after ? 1 : 0);
          const before = ordered[index - 1]?.position, next = ordered[index]?.position;
          const position = before === undefined ? (next ?? 0) - 1 : next === undefined ? before + 1 : (before + next) / 2;
          void updateTeamResourceSection(team.id, sectionId, { position }).then(onReload).catch(() => toast.error(t('Could not move section')));
        }
      }}>
      {section && (
        <header draggable={!editing} onDragStart={event => { event.dataTransfer.setData('application/x-flow-team-section', section.id); event.dataTransfer.effectAllowed = 'move'; }} onContextMenu={event => { event.preventDefault(); setMenuOpen(true); }}>
          {editing ? (
            <input
              autoFocus
              aria-label={t('Rename section')}
              value={name}
              onChange={(event) => setName(event.target.value)}
              onKeyDown={(event) => {
                if (event.nativeEvent.isComposing) return;
                if (event.key === "Enter") { event.preventDefault(); void save(); }
                if (event.key === "Escape") { cancelled.current = true; setEditing(false); }
              }}
              onBlur={() => void save()}
            />
          ) : (
            <span className="team-resource-section-name" data-i18n-ignore onDoubleClick={beginRename}>{section.name}</span>
          )}
          <button className="team-resource-collapse" aria-label={t(collapsed ? 'Expand section' : 'Collapse section')} aria-expanded={!collapsed} onClick={event => onToggle(event.altKey)}><ResourceChevronIcon/></button>
          <span className="team-resource-section-rule"/>
          <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}>
            <DropdownMenu.Trigger asChild>
              <button aria-label={`Open menu ${section.name}`}>
                <TeamMoreIcon />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content data-flow-motion="floating" className="team-home-menu team-section-menu" align="end" sideOffset={4} onCloseAutoFocus={event => event.preventDefault()}>
                <ResourceCommandMenu nested documents={documents} resources={resources} sectionId={section.id} team={team} onNew={onNew} onLink={onLink} onSaved={onReload}/>
                <DropdownMenu.Item onSelect={() => { void navigator.clipboard.writeText(`${location.origin}/${data.workspace.urlKey}/team/${team.key}/overview#section-${section.id}`).then(() => toast.success(t('Link copied'))).catch(() => toast.error(t('Could not copy link'))); }}><CopyLinkIcon/><span>{t('Copy link')}</span></DropdownMenu.Item>
                <DropdownMenu.Item onSelect={() => { setMenuOpen(false); setTimeout(beginRename, 100); }}>
                  <Pencil/><span>{t('Rename…')}</span>
                </DropdownMenu.Item>
                <DropdownMenu.Separator />
                <DropdownMenu.Item
                  className="danger"
                  onSelect={() => void remove()}
                >
                  <Trash2 />
                  <span>{t(items.length ? 'Delete…' : 'Delete')}</span>
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        </header>
      )}
      <AnimatedCollapse open={!collapsed}>
      {section && !items.length && <ResourceCommandMenu documents={documents} resources={resources} sectionId={section.id} team={team} onNew={onNew} onLink={onLink} onSaved={onReload} empty/>}
      {items.map((item) => {
        const resourceDocument = item.resourceType === "document"
          ? data.documents.find((value) => value.id === item.resourceId)
          : undefined;
        return (
        <div className="team-resource-row" key={item.id} draggable onDragStart={event => { event.stopPropagation(); event.dataTransfer.setData('application/x-flow-team-resource', item.id); event.dataTransfer.effectAllowed = 'move'; }}>
          {resourceDocument ? <DocumentGlyph document={resourceDocument} /> : item.resourceType === "document" ? <FileText /> : <Link2 />}
          <button
            data-i18n-ignore
            onClick={() => {
              const document = data.documents.find(
                (value) => value.id === item.resourceId,
              );
              if (document)
                onNavigate(documentPath(data.workspace.urlKey, document));
              else if (item.url) window.open(item.url, "_blank", "noopener");
            }}
          >
            {item.title}
          </button>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button aria-label={`Open menu ${item.title}`}>
                <TeamMoreIcon />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content data-flow-motion="floating" className="team-home-menu" align="end">
                <DropdownMenu.Sub>
                  <DropdownMenu.SubTrigger>
                    Move to
                    <ChevronRight />
                  </DropdownMenu.SubTrigger>
                  <DropdownMenu.Portal>
                    <DropdownMenu.SubContent data-flow-motion="floating"
                      className="team-home-menu"
                      sideOffset={4}
                    >
                      <DropdownMenu.Item
                        onSelect={() =>
                          void updateTeamResource(team.id, item.id, {
                            sectionId: "",
                          }).then(onReload)
                        }
                      >
                        Team resources{!item.sectionId && <Check />}
                      </DropdownMenu.Item>
                      {sections.map((value) => (
                        <DropdownMenu.Item
                          key={value.id}
                          onSelect={() =>
                            void updateTeamResource(team.id, item.id, {
                              sectionId: value.id,
                            }).then(onReload)
                          }
                        >
                          {value.name}
                          {item.sectionId === value.id && <Check />}
                        </DropdownMenu.Item>
                      ))}
                    </DropdownMenu.SubContent>
                  </DropdownMenu.Portal>
                </DropdownMenu.Sub>
                <DropdownMenu.Separator />
                <DropdownMenu.Item
                  className="danger"
                  onSelect={() =>
                    void deleteTeamResource(team.id, item.id).then(onReload)
                  }
                >
                  <Trash2 />
                  Remove
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        </div>
      )})}
      </AnimatedCollapse>
    </div>
  );
}

function ResourceCommandMenu({
  documents, onLink, onNew, onSaved, resources, team, sectionId, nested = false, empty = false,
}: {
  documents: FlowDocument[];
  onLink: () => void;
  onNew: () => void;
  onSaved: () => Promise<void>;
  resources: TeamPinnedResource[];
  team: Team;
  sectionId?: string;
  nested?: boolean;
  empty?: boolean;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState('');
  const [saving, setSaving] = useState(false);
  const pending = useRef(false);
  const visible = [...new Map(documents.filter(document => !document.archivedAt && document.title.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())).map(document => [document.id, document])).values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  const groups = new Map<string, FlowDocument[]>();
  for (const document of visible) {
    const date = new Date(document.createdAt);
    const label = !Number.isFinite(date.getTime()) ? 'Earlier' : isToday(date) ? 'Today' : isYesterday(date) ? 'Yesterday' : isThisWeek(date) ? 'This week' : isSameWeek(date, subWeeks(new Date(), 1)) ? 'Last week' : isThisMonth(date) ? 'This month' : isSameMonth(date, subMonths(new Date(), 1)) ? 'Last month' : format(date, 'MMMM yyyy');
    const group = groups.get(label) ?? []; group.push(document); groups.set(label, group);
  }
  const pinned = (document: FlowDocument) => resources.find(item => item.resourceType === 'document' && item.resourceId === document.id);
  const selected = (document: FlowDocument) => { const item = pinned(document); return Boolean(item && (!sectionId || item.sectionId === sectionId)); };
  const toggle = async (document: FlowDocument) => {
    if (pending.current) return;
    pending.current = true; setSaving(true);
    try {
      const item = pinned(document);
      if (item && selected(document)) await deleteTeamResource(team.id, item.id);
      else if (item) await updateTeamResource(team.id, item.id, { sectionId: sectionId ?? '' });
      else await pinTeamResource(team.id, { resourceType: 'document', resourceId: document.id, title: document.title, sectionId: sectionId ?? '' });
      await onSaved();
    } catch (error) { toast.error(error instanceof Error ? error.message : t('Could not update resource')); }
    finally { pending.current = false; setSaving(false); }
  };
  const content = <>
    <DropdownMenu.Item onSelect={onNew}><NewDocumentIcon/><span>{t('New document')}</span></DropdownMenu.Item>
    <DropdownMenu.Sub onOpenChange={open => { if (!open) setQuery(''); }}>
      <DropdownMenu.SubTrigger><PageIcon/><span>{t('Existing documents')}</span><ResourceChevronIcon/></DropdownMenu.SubTrigger>
      <DropdownMenu.Portal>
        <DropdownMenu.SubContent data-flow-motion="floating" collisionPadding={8} alignOffset={-6} className="team-resource-command-menu team-resource-existing-menu" sideOffset={-2}>
          <label className="team-resource-existing-search"><input aria-label={t('Search documents…')} autoFocus placeholder={t('Search documents…')} value={query} onChange={event => setQuery(event.target.value)} onKeyDown={event => { if (event.key.length === 1) event.stopPropagation(); }}/><Search/></label>
          <div className="team-resource-existing-list">
            {[...groups].map(([label, items]) => <DropdownMenu.Group key={label}>
              <DropdownMenu.Label className="team-resource-date-group">{t(label)}</DropdownMenu.Label>
              {items.map(document => <DropdownMenu.CheckboxItem checked={selected(document)} disabled={saving} key={document.id} onSelect={event => { event.preventDefault(); void toggle(document); }}>
                <span className="team-resource-checkbox">{selected(document) && <Check/>}</span><DocumentGlyph document={document}/><span data-i18n-ignore>{document.title || t('Untitled document')}</span>
              </DropdownMenu.CheckboxItem>)}
            </DropdownMenu.Group>)}
            {!visible.length && <p>{t('No matching documents')}</p>}
          </div>
        </DropdownMenu.SubContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Sub>
    <DropdownMenu.Separator/>
    <DropdownMenu.Item onSelect={onLink}><NewLinkIcon/><span>{t('New link…')}</span></DropdownMenu.Item>
  </>;
  if (nested) return <DropdownMenu.Sub>
    <DropdownMenu.SubTrigger><Pin/><span>{t('Add resources')}</span><ResourceChevronIcon/></DropdownMenu.SubTrigger>
    <DropdownMenu.Portal><DropdownMenu.SubContent data-flow-motion="floating" collisionPadding={8} className="team-resource-command-menu" sideOffset={-2} alignOffset={-6}>{content}</DropdownMenu.SubContent></DropdownMenu.Portal>
  </DropdownMenu.Sub>;
  return <DropdownMenu.Root onOpenChange={open => { if (!open) setQuery(''); }}>
    <DropdownMenu.Trigger asChild><button aria-label={t('Add resources')} className={empty ? 'team-resource-empty-add' : 'team-resource-command'} title={t('Add resources')}><PlusIcon/>{empty && <span>{t('Add resources')}</span>}</button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align={empty ? 'start' : 'end'} className="team-resource-command-menu" sideOffset={4}>{content}</DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>;
}

function AddLinkDialog({
  team,
  sectionId = '',
  onClose,
  onSaved,
}: {
  team: Team;
  sectionId?: string;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [url, setUrl] = useState("");
  const [title, setTitle] = useState("");
  const [saving, setSaving] = useState(false);
  const save = async () => {
    const normalized = url.trim();
    if (!/^https?:\/\//i.test(normalized)) {
      toast.error(t("Enter a valid URL"));
      return;
    }
    setSaving(true);
    try {
      await pinTeamResource(team.id, {
        sectionId,
        resourceType: "link",
        title: title.trim() || normalized,
        url: normalized,
      });
      await onSaved();
    } catch (error) {
      setSaving(false);
      toast.error(
        error instanceof Error ? error.message : "Could not add resource",
      );
    }
  };
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay data-flow-motion="backdrop" className="team-home-overlay" />
        <Dialog.Content data-flow-motion="dialog"
          aria-describedby={undefined}
          className="team-link-dialog"
        >
          <Dialog.Title>
            <CopyLinkIcon />
            {t("Add link to team")}
          </Dialog.Title>
          <label>
            <span>URL</span>
            <input
              autoFocus
              aria-label="URL"
              placeholder="https://…"
              type="url"
              value={url}
              onChange={(event) => setUrl(event.target.value)}
            />
          </label>
          <label>
            <span>
              {t("Title")} <small>({t("optional")})</small>
            </span>
            <input
              aria-label={`${t("Title")} (${t("optional")})`}
              value={title}
              onChange={(event) => setTitle(event.target.value)}
            />
          </label>
          <footer>
            <button onClick={onClose}>{t("Cancel")}</button>
            <button
              className="primary"
              disabled={saving}
              onClick={() => void save()}
            >
              {t("Add link")}
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function NewSectionRow({
  onCancel,
  onSave,
}: {
  onCancel: () => void;
  onSave: (name: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);
  const cancelled = useRef(false), pending = useRef(false);
  const save = async () => {
    if (pending.current || cancelled.current) return;
    if (!name.trim()) { onCancel(); return; }
    pending.current = true;
    setSaving(true);
    try {
      await onSave(name.trim());
    } catch (error) {
      pending.current = false;
      setSaving(false);
      toast.error(
        error instanceof Error ? error.message : t("Could not add section"),
      );
    }
  };
  return (
    <div className="team-new-section-row">
      <input
        autoFocus
        aria-label={t("Section name")}
        disabled={saving}
        maxLength={80}
        placeholder={t("Section name")}
        value={name}
        onBlur={() => void save()}
        onChange={(event) => setName(event.target.value)}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return;
          if (event.key === "Enter") { event.preventDefault(); void save(); }
          if (event.key === "Escape") { cancelled.current = true; onCancel(); }
        }}
      />
      <span aria-hidden="true"><ResourceChevronIcon/></span>
      <i />
    </div>
  );
}

function AddMembersDialog({
  data,
  team,
  onClose,
  onSaved,
}: {
  data: BootstrapData;
  team: Team;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [role, setRole] = useState<"owner" | "member">("member");
  const [saving, setSaving] = useState(false);
  const canAssignOwner = viewerOwnsTeam(data, team.id);
  const workspaceMemberById = useMemo(() => new Map((data.members ?? []).map(member => [member.user.id, member])), [data.members]);
  const memberIds = new Set(
    data.teamMembers
      .filter((membership) => membership.teamId === team.id)
      .map((membership) => membership.userId),
  );
  const candidates = data.users.filter(
    (user) => {
      if (memberIds.has(user.id)) return false;
      const value = query.trim().toLocaleLowerCase();
      const workspaceMember = workspaceMemberById.get(user.id);
      if (!workspaceMember || workspaceMember.status !== "active") return false;
      return !value || [user.displayName, user.name, user.userId, user.email, user.id].some(field => field?.toLocaleLowerCase().includes(value));
    },
  );
  const save = async () => {
    if (!selected.length || saving) return;
    setSaving(true);
    try {
      for (let offset = 0; offset < selected.length; offset += 5) {
        await Promise.all(selected.slice(offset, offset + 5).map(userId =>
          setTeamMembership(data.workspace.urlKey, team.id, userId, true, role),
        ));
      }
      await onSaved();
    } catch (error) {
      setSaving(false);
      toast.error(
        error instanceof Error ? error.message : t("Could not add members"),
      );
    }
  };
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay data-flow-motion="backdrop" className="team-home-overlay" />
        <Dialog.Content data-flow-motion="dialog"
          aria-describedby={undefined}
          className="team-members-dialog"
        >
          <Dialog.Title>
            {t("Add members to")} <span data-i18n-ignore>{team.name}</span>
          </Dialog.Title>
          <Dialog.Close
            aria-label={t("Close modal dialog")}
            className="team-members-close"
          >
            <CloseIcon />
          </Dialog.Close>
          <DropdownMenu.Root onOpenChange={(open) => !open && setQuery("")}>
            <DropdownMenu.Trigger asChild>
              <button
                aria-label={t("Select members")}
                className="team-members-select"
              >
                <span>
                  {selected.length
                    ? t(`${selected.length} selected members`)
                    : t("Select members…")}
                </span>
                <SelectChevronIcon />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content data-flow-motion="floating"
                align="start"
                className="team-members-picker"
                sideOffset={4}
              >
                <label>
                  <input
                    aria-label={t("Search members…")}
                    autoFocus
                    placeholder={t("Search members…")}
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    onKeyDown={(event) => event.stopPropagation()}
                  />
                  <Search />
                </label>
                <div>
                  {candidates.map((user, index) => {
                    const checked = selected.includes(user.id);
                    return (
                      <DropdownMenu.CheckboxItem
                        aria-label={user.displayName}
                        checked={checked}
                        key={user.id}
                        onSelect={(event) => {
                          event.preventDefault();
                          setSelected((current) =>
                            checked
                              ? current.filter((id) => id !== user.id)
                              : [...current, user.id],
                          );
                        }}
                      >
                        <span className="team-resource-checkbox">
                          {checked && <Check />}
                        </span>
                        <UserAvatar
                          avatarUrl={user.avatarUrl}
                          color={memberColor(index)}
                          name={user.displayName}
                        />
                        <span className="team-members-candidate-copy"><strong data-i18n-ignore>{user.displayName}</strong><small data-i18n-ignore>{user.name}{user.email ? ` · ${user.email}` : ""}</small></span>
                        <small className="team-members-candidate-role">{t(workspaceMemberById.get(user.id)?.role === "admin" ? "Workspace admin" : "Member")}</small>
                      </DropdownMenu.CheckboxItem>
                    );
                  })}
                  {!candidates.length && <p>{t("No members available")}</p>}
                </div>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
          {canAssignOwner && <div aria-label={t("Team role")} className="team-members-role-choice" role="group">
            <span>{t("Team role")}</span>
            <div><button aria-pressed={role === "member"} onClick={() => setRole("member")} type="button">{t("Team member")}</button><button aria-pressed={role === "owner"} onClick={() => setRole("owner")} type="button">{t("Team owner")}</button></div>
          </div>}
          <footer>
            <button onClick={onClose}>{t("Cancel")}</button>
            <button
              className="primary"
              disabled={!selected.length || saving}
              onClick={() => void save()}
            >
              {t("Add members")}
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

type TeamMemberVisibility = { owners: boolean; members: boolean; guests: boolean; invited: boolean };
type TeamMemberColumn = "email" | "role" | "subteams";
type TeamMemberOrdering = "name" | "email" | "role";
type TeamMemberDirectoryEntry = {
  membership: BootstrapData["teamMembers"][number];
  user: BootstrapData["users"][number];
  workspaceMember?: BootstrapData["members"][number];
  invitation?: BootstrapData["invitations"][number];
};

function TeamMembersDirectory({
  data,
  onAdd,
  onNavigate,
  onReload,
  team,
}: {
  data: BootstrapData;
  onAdd: () => void;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  team: Team;
}) {
  const { t } = useI18n();
  const initialDisplay = useMemo(() => readTeamMemberDirectoryState(team.id), [team.id]);
  const [ordering, setOrdering] = useState<TeamMemberOrdering>(initialDisplay.ordering);
  const [descending, setDescending] = useState(initialDisplay.descending);
  const [columns, setColumns] = useState<Set<TeamMemberColumn>>(() => initialDisplay.columns);
  const [visibility, setVisibility] = useState<TeamMemberVisibility>(initialDisplay.visibility);
  const [busyUserId, setBusyUserId] = useState("");
  const directoryRef = useRef<HTMLElement>(null);
  const canManage = canManageTeamSettings(data, team.id, "members");
  const canChangeOwners = viewerOwnsTeam(data, team.id);
  const hierarchy = useMemo(() => teamHierarchy(data.teams, data.teamSettings, data.teamParents), [data.teamParents, data.teamSettings, data.teams]);
  const descendantIds = useMemo(() => new Set([...hierarchy.subtree(team.id)].filter(id => id !== team.id)), [hierarchy, team.id]);
  const hasSubteams = descendantIds.size > 0;
  const userById = useMemo(() => new Map(data.users.map(user => [user.id, user])), [data.users]);
  const workspaceMemberById = useMemo(() => new Map((data.members ?? []).map(member => [member.user.id, member])), [data.members]);
  const subteamCountByUser = useMemo(() => {
    const teamsByUser = new Map<string, Set<string>>();
    data.teamMembers.forEach(member => { if (descendantIds.has(member.teamId)) { const teams = teamsByUser.get(member.userId) ?? new Set<string>(); teams.add(member.teamId); teamsByUser.set(member.userId, teams); } });
    return new Map([...teamsByUser].map(([userId, teams]) => [userId, teams.size]));
  }, [data.teamMembers, descendantIds]);
  const allMembers = data.teamMembers
    .filter((membership) => membership.teamId === team.id)
    .flatMap<TeamMemberDirectoryEntry>((membership) => {
      const user = userById.get(membership.userId);
      const workspaceMember = workspaceMemberById.get(membership.userId);
      return user ? [{ membership, user, workspaceMember }] : [];
    });
  const invitedMembers: TeamMemberDirectoryEntry[] = (data.invitations ?? []).filter(invitation => visibility.invited && invitation.status === "pending" && invitation.teamIds.includes(team.id)).map(invitation => {
    const user = { id: `invited:${invitation.id}`, name: invitation.email, displayName: invitation.email, email: invitation.email, active: false, emailVerified: false };
    return { invitation, membership: { teamId: team.id, userId: user.id, role: "member", joinedAt: invitation.createdAt }, user, workspaceMember: { user, role: invitation.role, status: "suspended", joinedAt: invitation.createdAt } };
  });
  const members = [...allMembers.filter(entry => {
      const workspaceRole = entry.workspaceMember?.role;
      if (entry.membership.role === "owner" || workspaceRole === "owner" || workspaceRole === "admin") return visibility.owners;
      return workspaceRole === "guest" ? visibility.guests : visibility.members;
    }), ...invitedMembers]
    .sort((left, right) => {
      const leftValue = ordering === "name" ? left.user.displayName : ordering === "email" ? left.user.email : `${left.membership.role}-${left.workspaceMember?.role}`;
      const rightValue = ordering === "name" ? right.user.displayName : ordering === "email" ? right.user.email : `${right.membership.role}-${right.workspaceMember?.role}`;
      const result = leftValue.localeCompare(rightValue, undefined, { numeric: true, sensitivity: "base" });
      return descending ? -result : result;
    });
  const ownerCount = allMembers.filter((entry) => entry.membership.role === "owner").length;
  const gridStyle = { gridTemplateColumns: `minmax(260px,1fr) ${columns.has("email") ? "minmax(190px,220px) " : ""}${columns.has("role") ? "140px " : ""}${hasSubteams && columns.has("subteams") ? "82px " : ""}28px` } as CSSProperties;
  useEffect(() => persistTeamMemberDirectoryState(team.id, { ordering, descending, columns, visibility }), [columns, descending, ordering, team.id, visibility]);
  const changeOrder = (next: typeof ordering) => {
    if (next === ordering) setDescending((value) => !value);
    else { setOrdering(next); setDescending(false); }
  };
  const changeMembership = async (userId: string, member: boolean, role: "owner" | "member" = "member") => {
    setBusyUserId(userId);
    try {
      await setTeamMembership(data.workspace.urlKey, team.id, userId, member, role);
      await onReload();
      toast.success(member ? t("Team role updated") : t(userId === data.viewer.id ? "You left the team" : "Member removed"));
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not update team membership"));
    } finally {
      setBusyUserId("");
    }
  };
  const removeMember = async (entry: (typeof members)[number]) => {
    const self = entry.user.id === data.viewer.id;
    const confirmed = await confirmAction(self ? t("Leave this team?") : t(`Remove ${entry.user.displayName} from this team?`), {
      confirmLabel: self ? t("Leave team") : t("Remove member"),
      danger: true,
    });
    if (confirmed) await changeMembership(entry.user.id, false, entry.membership.role);
  };
  useEffect(() => {
    const scroller = directoryRef.current?.querySelector<HTMLElement>(".team-members-scroll");
    if (scroller) scroller.tabIndex = 0;
  }, [members.length]);
  const keyboardScroll = (event: ReactKeyboardEvent<HTMLElement>) => {
    const scroller = directoryRef.current?.querySelector<HTMLElement>(".team-members-scroll");
    if (!scroller || !["PageDown", "PageUp", "Home", "End"].includes(event.key)) return;
    if ((event.target as HTMLElement).closest("[role=menu],button,input,a,select")) return;
    event.preventDefault();
    if (event.key === "Home") scroller.scrollTop = 0;
    else if (event.key === "End") scroller.scrollTop = scroller.scrollHeight;
    else scroller.scrollBy({ top: (event.key === "PageDown" ? 1 : -1) * scroller.clientHeight * .9, behavior: "smooth" });
  };
  const header = <header className="team-members-header" style={gridStyle}>
    <button onClick={() => changeOrder("name")} type="button">{ordering === "name" ? (descending ? "Z-A" : "A-Z") : t("Name")}</button>
    {columns.has("email") && <button onClick={() => changeOrder("email")} type="button">{t("Email")}{ordering === "email" && <span>{descending ? "↓" : "↑"}</span>}</button>}
    {columns.has("role") && <button onClick={() => changeOrder("role")} type="button">{t("Role")}{ordering === "role" && <span>{descending ? "↓" : "↑"}</span>}</button>}
    {hasSubteams && columns.has("subteams") && <span>{t("Sub-teams")}</span>}
    <span/>
  </header>;
  return <section className="team-members-directory" onKeyDownCapture={keyboardScroll} ref={directoryRef}>
    <div className="team-members-toolbar">
      {canManage && <button className="team-members-add" onClick={onAdd}><PlusIcon/>{t("Add a member")}</button>}
      <TeamMembersDisplayMenu columns={columns} descending={descending} hasSubteams={hasSubteams} onColumn={(column) => setColumns((current) => { const next = new Set(current); if (next.has(column)) next.delete(column); else next.add(column); return next; })} onDirection={() => setDescending((value) => !value)} onOrdering={setOrdering} onVisibility={(key) => setVisibility(current => ({ ...current, [key]: !current[key] }))} ordering={ordering} visibility={visibility}/>
    </div>
    {members.length ? <VirtualColumnList ariaLabel={t("Team members")} className="team-members-columns" computeItemKey={(_index, entry) => entry.user.id} data={members} header={header} increaseViewportBy={{ top: 96, bottom: 288 }} scrollerClassName="team-members-list team-members-scroll" virtualize={members.length > 60} itemContent={(index, value) => {
        const profile = `/${data.workspace.urlKey}/profiles/${encodeURIComponent(value.user.name)}`;
        const invited = Boolean(value.invitation);
        const managed = value.membership.managed === true;
        const lastOwner = value.membership.role === "owner" && ownerCount === 1;
        const menuVisible = !invited && (canManage || value.user.id === data.viewer.id);
        return <div aria-busy={busyUserId === value.user.id || undefined} className={`team-member-row${invited ? " is-invited" : ""}`} role="listitem" style={gridStyle}>
          {invited ? <span className="team-members-person">
            <UserAvatar avatarUrl={value.user.avatarUrl} color={memberColor(index)} name={value.user.displayName}/>
            <span><strong data-i18n-ignore>{value.user.displayName}</strong><small>{t("Invited")}</small></span>
          </span> : <a className="team-members-person" href={profile} onClick={event => { event.preventDefault(); onNavigate(profile) }}>
            <UserAvatar avatarUrl={value.user.avatarUrl} color={memberColor(index)} name={value.user.displayName}/>
            <span><strong data-i18n-ignore>{value.user.displayName}</strong><small data-i18n-ignore>{value.user.userId || value.user.name}</small></span>
          </a>}
          {columns.has("email") && <span className="team-members-email" data-i18n-ignore>{value.user.email}</span>}
          {columns.has("role") && <span className="team-members-role"><span>{t(invited ? "Invited" : value.membership.role === "owner" ? "Team owner" : "Team member")}</span><small>{t(workspaceRoleLabel(value.workspaceMember?.role))}{managed ? ` · ${t(value.membership.managedSource === "scim" ? "SCIM managed" : "Managed")}` : ""}</small></span>}
          {hasSubteams && columns.has("subteams") && <span className="team-member-subteams">{subteamCountByUser.get(value.user.id) || "—"}</span>}
          {menuVisible ? <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild><button aria-label={t("Open menu")} onClick={event => { event.preventDefault(); event.stopPropagation() }}><TeamMoreIcon/></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="end" className="team-home-menu" sideOffset={4}>
              <DropdownMenu.Item onSelect={() => onNavigate(profile)}>{t("View profile")}</DropdownMenu.Item>
              {canChangeOwners && <><DropdownMenu.Separator/><DropdownMenu.Item disabled={managed || busyUserId === value.user.id || lastOwner} title={managed ? t("Managed by SCIM") : lastOwner ? t("A team needs at least one owner") : undefined} onSelect={() => void changeMembership(value.user.id, true, value.membership.role === "owner" ? "member" : "owner")}>{t(value.membership.role === "owner" ? "Make team member" : "Make team owner")}</DropdownMenu.Item></>}
              <DropdownMenu.Separator/>
              <DropdownMenu.Item className="danger" disabled={managed || busyUserId === value.user.id || lastOwner} title={managed ? t("Managed by SCIM") : lastOwner ? t("A team needs at least one owner") : undefined} onSelect={() => void removeMember(value)}>{t(value.user.id === data.viewer.id ? "Leave team" : "Remove from team")}</DropdownMenu.Item>
            </DropdownMenu.Content></DropdownMenu.Portal>
          </DropdownMenu.Root>
          : <span/>}
        </div>;
      }}/> : <>{header}<div className="team-members-empty">{t("No team members")}</div></>}
  </section>;
}

function TeamMembersDisplayMenu({ columns, descending, hasSubteams, onColumn, onDirection, onOrdering, onVisibility, ordering, visibility }: { columns: Set<TeamMemberColumn>; descending: boolean; hasSubteams: boolean; onColumn: (column: TeamMemberColumn) => void; onDirection: () => void; onOrdering: (ordering: TeamMemberOrdering) => void; onVisibility: (key: keyof TeamMemberVisibility) => void; ordering: TeamMemberOrdering; visibility: TeamMemberVisibility }) {
  const { t } = useI18n();
  const labels = { name: t("Name"), email: t("Email"), role: t("Role") };
  const visibilityLabels: Record<keyof TeamMemberVisibility, string> = { owners: t("Show owners and admins"), members: t("Show members"), guests: t("Show guests"), invited: t("Show invited") };
  return <Popover.Root><Popover.Trigger asChild><button aria-label={t("Display options")} className="team-members-display"><DisplayIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className="team-directory-display-menu team-members-display-menu" sideOffset={4}>
    <div className="team-directory-order-row"><span>{t("Ordering")}</span><div><select aria-label={t("Ordering")} onChange={event => onOrdering(event.target.value as TeamMemberOrdering)} value={ordering}>{(Object.keys(labels) as TeamMemberOrdering[]).map(value => <option key={value} value={value}>{labels[value]}</option>)}</select><button aria-label={t("Direction")} onClick={onDirection} title={t(descending ? "Descending" : "Ascending")} type="button">{descending ? <ArrowDownWideNarrow/> : <ArrowUpNarrowWide/>}</button></div></div>
    <div className="team-directory-visibility">{(Object.keys(visibilityLabels) as (keyof TeamMemberVisibility)[]).map(value => <label key={value}><span>{visibilityLabels[value]}</span><button aria-checked={visibility[value]} onClick={() => onVisibility(value)} role="switch" type="button"><i/></button></label>)}</div>
    <div className="team-directory-properties"><span>{t("Display properties")}</span><div>{(["email", "role", ...(hasSubteams ? ["subteams" as const] : [])] as TeamMemberColumn[]).map(value => <button aria-pressed={columns.has(value)} key={value} onClick={() => onColumn(value)} type="button">{value === "subteams" ? t("Sub-teams") : labels[value]}</button>)}</div></div>
  </Popover.Content></Popover.Portal></Popover.Root>;
}

function readTeamMemberDirectoryState(teamId: string) {
  const fallback = { ordering: "name" as TeamMemberOrdering, descending: false, columns: new Set<TeamMemberColumn>(["email", "role", "subteams"]), visibility: { owners: true, members: true, guests: true, invited: true } };
  try {
    const saved = JSON.parse(localStorage.getItem(`flow:team:${teamId}:member-directory`) ?? "null") as { ordering?: TeamMemberOrdering; descending?: boolean; columns?: TeamMemberColumn[]; visibility?: Partial<TeamMemberVisibility> } | null;
    if (!saved) return fallback;
    return { ordering: ["name", "email", "role"].includes(saved.ordering ?? "") ? saved.ordering! : fallback.ordering, descending: saved.descending === true, columns: new Set((saved.columns ?? [...fallback.columns]).filter(value => ["email", "role", "subteams"].includes(value))), visibility: { ...fallback.visibility, ...saved.visibility } };
  } catch { return fallback; }
}
function persistTeamMemberDirectoryState(teamId: string, value: ReturnType<typeof readTeamMemberDirectoryState>) {
  try { localStorage.setItem(`flow:team:${teamId}:member-directory`, JSON.stringify({ ...value, columns: [...value.columns] })); } catch { /* Storage is optional. */ }
}

function workspaceRoleLabel(role: BootstrapData["viewerRole"] | undefined) {
  if (role === "owner") return "Workspace owner";
  if (role === "admin") return "Workspace admin";
  if (role === "guest") return "Guest";
  return "Workspace member";
}

function TeamDocuments({
  creating,
  data,
  documents,
  onNavigate,
  onNew,
  onReloadResources,
  resources,
  team,
}: {
  creating: boolean;
  data: BootstrapData;
  documents: FlowDocument[];
  onNavigate: (path: string) => void;
  onNew: () => void;
  onReloadResources: () => Promise<void>;
  resources: TeamPinnedResource[];
  team: Team;
}) {
  const { t } = useI18n();
  const initial = useMemo(() => readDocumentDirectoryState(), []);
  const [search, setSearch] = useState(initial.search);
  const [searchOpen, setSearchOpen] = useState(Boolean(initial.search));
  const searchRef = useRef<HTMLInputElement>(null);
  const [filters, setFilters] = useState<Record<DocumentFilterField, Set<string>>>(initial.filters);
  const [match, setMatch] = useState<"all" | "any">(initial.match);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [grouping, setGrouping] = useState<DocumentGrouping>(initial.grouping);
  const [ordering, setOrdering] = useState<DocumentOrdering>(initial.ordering);
  const [descending, setDescending] = useState(initial.descending);
  const [showInactive, setShowInactive] = useState(initial.showInactive);
  const [onlyMyProjects, setOnlyMyProjects] = useState(initial.onlyMyProjects);
  const [properties, setProperties] = useState<Set<DocumentProperty>>(initial.properties);
  const [selected, setSelected] = useState<string[]>([]);
  const collapseKey = `flow:${data.workspace.id}:${data.viewer.id}:team-documents:${team.id}:collapsed`;
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(() => {
    try { const value: unknown = JSON.parse(localStorage.getItem(collapseKey) ?? '{}'); return value && typeof value === 'object' && !Array.isArray(value) ? Object.fromEntries(Object.entries(value).filter(([, value]) => typeof value === 'boolean')) : {}; } catch { return {}; }
  });
  const [pinBusy, setPinBusy] = useState("");
  const userById = useMemo(() => new Map(data.users.map(user => [user.id, user])), [data.users]);
  const projectById = useMemo(() => new Map(data.projects.map(project => [project.id, project])), [data.projects]);
  const ownerFor = useCallback((document: FlowDocument) => {
    const ownerId = document.permissions?.find(permission => permission.role === "owner" && permission.subjectType === "user")?.subjectId;
    return (ownerId ? userById.get(ownerId) : undefined) ?? document.creator;
  }, [userById]);
  const activeGroups = (Object.keys(filters) as DocumentFilterField[]).filter(field => filters[field].size);
  const matches = useCallback((document: FlowDocument) => {
    const checks = activeGroups.map(field => {
      const values = filters[field];
      if (field === "creator") return values.has(document.creator.id);
      if (field === "owner") return values.has(ownerFor(document).id);
      if (field === "project") return document.projectIds.some(id => values.has(id));
      if (field === "cycle") return data.cycles.some(cycle => values.has(cycle.id) && cycle.resources.some(resource => resource.documentId === document.id));
      return [...values].some(value => documentMatchesDate(document, value));
    });
    return !checks.length || (match === "all" ? checks.every(Boolean) : checks.some(Boolean));
  }, [activeGroups, data.cycles, filters, match, ownerFor]);
  const visible = useMemo(() => documents.filter(document => {
    if (document.archivedAt || search.trim() && !document.title.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase())) return false;
    if (!matches(document)) return false;
    const projects = document.projectIds.map(id => projectById.get(id)).filter(Boolean);
    if (!showInactive && projects.length && !projects.some(project => !project!.archivedAt && !["completed", "canceled", "cancelled"].includes(project!.status.type))) return false;
    if (onlyMyProjects && projects.length && !projects.some(project => project!.lead?.id === data.viewer.id || project!.memberIds.includes(data.viewer.id))) return false;
    return true;
  }).sort((a, b) => {
    const left = documentOrderValue(a, ordering, ownerFor, projectById);
    const right = documentOrderValue(b, ordering, ownerFor, projectById);
    const result = typeof left === "number" && typeof right === "number" ? left - right : String(left).localeCompare(String(right), undefined, { numeric: true, sensitivity: "base" });
    const stable = result || a.title.localeCompare(b.title) || a.id.localeCompare(b.id);
    return descending ? -stable : stable;
  }), [data.viewer.id, descending, documents, matches, onlyMyProjects, ordering, ownerFor, projectById, showInactive, search]);
  const grouped = useMemo(() => groupDocuments(visible, grouping, data, ownerFor), [data, grouping, ownerFor, visible]);
  const filterGroups = useMemo<DirectoryFilterGroup[]>(() => [
    { id: "creator", label: t("Creator"), icon: <UserRound/>, choices: uniqueUsers(documents.map(document => document.creator)).map(user => ({ id: user.id, label: user.displayName, meta: user.email, person: user })) },
    { id: "owner", label: t("Owner"), icon: <UserRound/>, choices: uniqueUsers(documents.map(ownerFor)).map(user => ({ id: user.id, label: user.displayName, meta: user.email, person: user })) },
    { id: "project", label: t("Project"), icon: <FolderKanban/>, choices: data.projects.filter(project => documents.some(document => document.projectIds.includes(project.id))).map(project => ({ id: project.id, label: project.name, icon: <ProjectIcon style={{ color: project.color }}/> })) },
    { id: "cycle", label: t("Cycle"), icon: <CalendarDays/>, choices: data.cycles.filter(cycle => cycle.resources.some(resource => resource.documentId && documents.some(document => document.id === resource.documentId))).map(cycle => ({ id: cycle.id, label: cycle.name })) },
    { id: "dates", label: t("Dates"), icon: <CalendarDays/>, selectionMode: "single", choices: [{ id: "today", label: t("Edited today") }, { id: "week", label: t("Edited this week") }, { id: "month", label: t("Edited this month") }, { id: "older", label: t("Older") }] },
  ], [data.cycles, data.projects, documents, ownerFor, t]);
  useEffect(() => persistDocumentDirectoryState({ filters, match, grouping, ordering, descending, showInactive, onlyMyProjects, properties, search }), [descending, filters, grouping, match, onlyMyProjects, ordering, properties, showInactive, search]);
  useEffect(() => {
    const find = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f' && !(event.target instanceof Element && event.target.closest('[role="dialog"],input,textarea,[contenteditable="true"]'))) { event.preventDefault(); setSearchOpen(true); requestAnimationFrame(() => searchRef.current?.focus()); }
    };
    window.addEventListener('keydown', find); return () => window.removeEventListener('keydown', find);
  }, []);
  useEffect(() => {
    const restore = () => { const value = readDocumentDirectoryState(); setFilters(value.filters); setSearch(value.search); setSearchOpen(Boolean(value.search)); setGrouping(value.grouping); setOrdering(value.ordering); setDescending(value.descending); setProperties(value.properties); setMatch(value.match); setOnlyMyProjects(value.onlyMyProjects); setShowInactive(value.showInactive); };
    window.addEventListener('popstate', restore); return () => window.removeEventListener('popstate', restore);
  }, []);
  const visibleIDs = useMemo(() => new Set(visible.map(document => document.id)), [visible]);
  const selectedVisible = selected.filter(id => visibleIDs.has(id));
  const isCollapsed = (id: string, project: boolean) => collapsed[`${grouping}:${id}`] ?? (grouping === 'project' && project);
  const toggleGroup = (id: string, project: boolean) => {
    const next = { ...collapsed, [`${grouping}:${id}`]: !isCollapsed(id, project) };
    setCollapsed(next);
    try { localStorage.setItem(collapseKey, JSON.stringify(next)); } catch { /* Optional local preference. */ }
  };
  const changeFilter = (field: string, value: string, checked: boolean) => setFilters(current => ({ ...current, [field]: new Set(checked ? [...current[field as DocumentFilterField], value] : [...current[field as DocumentFilterField]].filter(item => item !== value)) }));
  const changeOrder = (next: DocumentOrdering) => { if (next === ordering) setDescending(value => !value); else { setOrdering(next); setDescending(next === "created" || next === "updated"); } };
  const togglePin = async (document: FlowDocument) => {
    const existing = resources.find(resource => resource.resourceType === "document" && resource.resourceId === document.id);
    setPinBusy(document.id);
    try {
      if (existing) await deleteTeamResource(team.id, existing.id);
      else await pinTeamResource(team.id, { resourceType: "document", resourceId: document.id, title: document.title });
      await onReloadResources();
      toast.success(t(existing ? "Removed from team overview" : "Pinned to team overview"));
    } catch (error) { toast.error(error instanceof Error ? error.message : t("Could not update team overview")); }
    finally { setPinBusy(""); }
  };
  const visibleProperties = new Set(properties);
  if (grouping === "project") visibleProperties.delete("project");
  if (grouping === 'owner') visibleProperties.delete('owner');
  const gridStyle = documentGridStyle(visibleProperties);
  type DocumentEntry = { kind: 'group'; group: (typeof grouped)[number] } | { kind: 'document'; document: FlowDocument; groupId: string };
  const entries: DocumentEntry[] = grouped.flatMap(group => [
    ...(grouping === 'none' ? [] : [{ kind: 'group' as const, group }]),
    ...(isCollapsed(group.id, Boolean(group.project)) && grouping !== 'none' ? [] : group.items.map(document => ({ kind: 'document' as const, document, groupId: group.id }))),
  ]);
  const copySelected = async () => { try { await navigator.clipboard.writeText(documents.filter(document => selectedVisible.includes(document.id)).map(document => `${location.origin}${documentPath(data.workspace.urlKey, document)}`).join('\n')); toast.success(t('Copied document links')); } catch { toast.error(t('Could not copy document link')); } };
  const header = <header className="team-documents-column-header" style={gridStyle}>
    <span/><button className="team-documents-name-heading" aria-label={ordering === 'name' ? descending ? 'Z-A' : 'A-Z' : t('Order by Name')} onClick={() => changeOrder('name')} type="button">{t('Name')}{ordering === 'name' && (descending ? <ArrowDownWideNarrow size={12}/> : <ArrowUpNarrowWide size={12}/>)}</button>
    {visibleProperties.has('project') && <button className="team-documents-project-column" onClick={() => changeOrder('project')} type="button">{t('Projects')}</button>}
    {visibleProperties.has('created') && <button className="team-documents-date-column" onClick={() => changeOrder('created')} type="button">{t('Created')}</button>}
    {visibleProperties.has('updated') && <button className="team-documents-date-column" onClick={() => changeOrder('updated')} type="button">{t('Last edited')}</button>}
    {visibleProperties.has('owner') && <button className="team-documents-owner-column" onClick={() => changeOrder('owner')} type="button">{t('Owner')}</button>}<span/>
  </header>;
  const renderEntry = (_: number, entry: DocumentEntry) => {
    if (entry.kind === 'group') { const { group } = entry; return <div className="team-documents-group-header"><button aria-expanded={!isCollapsed(group.id, Boolean(group.project))} aria-label={t(isCollapsed(group.id, Boolean(group.project)) ? 'Expand group' : 'Collapse group')} onClick={() => toggleGroup(group.id, Boolean(group.project))} type="button"><ChevronRight/></button>{group.project ? <a data-i18n-ignore href={projectPath(data.workspace.urlKey,group.project)} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); onNavigate(projectPath(data.workspace.urlKey,group.project!)); }}>{group.name}</a> : <span data-i18n-ignore={grouping === 'owner' || grouping === 'cycle' || undefined}>{t(group.name)}</span>}<small>{group.items.length}</small></div>; }
    const document = entry.document, owner = ownerFor(document), project = document.projectIds.map(id => projectById.get(id)).find(Boolean);
    const pinned = resources.some(resource => resource.resourceType === 'document' && resource.resourceId === document.id);
    return <a className="team-document-row" style={gridStyle} data-selected={selected.includes(document.id)} href={documentPath(data.workspace.urlKey, document)} onClick={event => {
      if ((event.target as HTMLElement).closest('button,input,label')) { event.preventDefault(); return; }
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      event.preventDefault(); onNavigate(documentPath(data.workspace.urlKey, document));
    }}>
      <label onClick={event => event.stopPropagation()}><input aria-label={t('Select document')} checked={selected.includes(document.id)} onChange={event => setSelected(current => event.target.checked ? [...new Set([...current, document.id])] : current.filter(id => id !== document.id))} type="checkbox"/><span aria-hidden="true"><Check size={10}/></span></label>
      <span className="team-document-title"><DocumentGlyph document={document}/><strong data-i18n-ignore>{document.title || t('Untitled')}</strong>{pinned && <Pin aria-label={t('Pinned to team overview')}/>}</span>
      {visibleProperties.has('project') && <button className="team-document-project team-documents-project-column" disabled={!project} onClick={() => { if(project)onNavigate(projectPath(data.workspace.urlKey,project)); }} type="button"><span data-i18n-ignore>{project?.name ?? '—'}</span></button>}
      {visibleProperties.has('created') && <time className="team-documents-date-column" dateTime={document.createdAt} title={new Date(document.createdAt).toLocaleString()}>{t(relativeDocumentDate(document.createdAt))}</time>}
      {visibleProperties.has('updated') && <time className="team-documents-date-column" dateTime={document.updatedAt} title={new Date(document.updatedAt).toLocaleString()}>{t(relativeDocumentDate(document.updatedAt))}</time>}
      {visibleProperties.has('owner') && <span className="team-documents-owner-column"><PersonHover person={owner}><button className="team-document-owner" onClick={() => onNavigate(`/${data.workspace.urlKey}/member/${encodeURIComponent(owner.name)}/assigned`)} type="button"><UserAvatar avatarUrl={owner.avatarUrl} name={owner.displayName}/><i data-i18n-ignore>{owner.displayName}</i></button></PersonHover></span>}
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={t('Open menu')} className="team-document-more" type="button"><TeamMoreIcon/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" className="team-home-menu"><DropdownMenu.Item onSelect={() => onNavigate(documentPath(data.workspace.urlKey, document))}>{t('Open document')}</DropdownMenu.Item><DropdownMenu.Item onSelect={() => { void navigator.clipboard.writeText(`${location.origin}${documentPath(data.workspace.urlKey, document)}`).catch(() => toast.error(t('Could not copy document link'))); }}>{t('Copy link')}</DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item disabled={pinBusy === document.id} onSelect={() => void togglePin(document)}>{t(pinned ? 'Remove from team overview' : 'Pin to team overview')}</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    </a>;
  };
  return (
    <div className="team-documents" data-created={visibleProperties.has('created')} data-updated={visibleProperties.has('updated')} data-owner={visibleProperties.has('owner')} data-project={visibleProperties.has('project')}>
      <div className="team-documents-toolbar">
        <button aria-label={t("New document")} disabled={creating} aria-busy={creating || undefined} className="team-documents-new" onClick={onNew} type="button"><PlusIcon/>{t("New document")}</button>
        <DirectoryFilterMenu groups={filterGroups} menuClassName="team-documents-filter-menu" onAdvanced={() => setAdvancedOpen(true)} onChoice={changeFilter} selected={filters} triggerClassName="team-documents-icon-button"/>
        <TeamDocumentsDisplayMenu descending={descending} grouping={grouping} onDirection={() => setDescending(value => !value)} onGrouping={setGrouping} onOnlyMyProjects={setOnlyMyProjects} onOrdering={next => { setOrdering(next); setDescending(next === 'created' || next === 'updated'); }} onProperty={(property) => setProperties(current => { const next = new Set(current); if (next.has(property)) next.delete(property); else next.add(property); return next; })} onShowInactive={setShowInactive} onlyMyProjects={onlyMyProjects} ordering={ordering} properties={properties} showInactive={showInactive}/>
      </div>
      {searchOpen && <div className="team-documents-search"><Search size={14}/><input ref={searchRef} autoFocus aria-label={t('Find documents')} placeholder={t('Find documents…')} value={search} onChange={event => setSearch(event.target.value)} onKeyDown={event => { if(event.key === 'Escape'){setSearch('');setSearchOpen(false);} }}/><button type="button" aria-label={t('Close search')} onClick={() => {setSearch('');setSearchOpen(false);}}><X size={14}/></button></div>}
      {activeGroups.length > 0 && <div className="team-documents-active-filters">{activeGroups.map(field => <button key={field} onClick={() => setFilters(current => ({ ...current, [field]: new Set() }))} type="button">{t(documentFilterLabel(field))} <span>{filters[field].size}</span><X/></button>)}<button className="is-clear" onClick={() => setFilters(emptyDocumentFilters())}>{t("Clear")}</button></div>}
      {visible.length > 0 ? <VirtualColumnList header={header} increaseViewportBy={200} ariaLabel={t('Documents')} className="team-documents-list" data={entries} computeItemKey={(_, entry) => entry.kind === 'group' ? `group:${entry.group.id}` : `${entry.groupId}:${entry.document.id}`} itemContent={renderEntry} virtualize={entries.length > 80}/> : <>
        <div className="team-documents-empty" data-filtered={documents.some(document => !document.archivedAt)} role="status">
          <div><DocumentsEmptyIllustration/><section><h3>{t(documents.some(document => !document.archivedAt) ? activeGroups.length ? 'No documents matching your filters' : search.trim() ? 'No documents matching your search' : 'No documents to show' : 'Team documents')}</h3>
          {!documents.some(document => !document.archivedAt) && <><p>{t('Create documents to share notes, decisions, and plans with your team.')}</p><button type="button" disabled={creating} aria-busy={creating || undefined} onClick={onNew}>{t('Create document')}</button></>}
          </section></div>
        </div>
      </>}
      {selectedVisible.length > 0 && <div className="team-documents-selection" role="toolbar" aria-label={t('Selected documents')}><span>{selectedVisible.length} {t('selected')}</span><button type="button" onClick={() => void copySelected()}>{t('Copy links')}</button><button type="button" onClick={() => setSelected([])} aria-label={t('Clear selection')}><X size={14}/></button></div>}
      <Dialog.Root open={advancedOpen} onOpenChange={setAdvancedOpen}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="team-documents-advanced-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="team-documents-advanced"><Dialog.Title>{t("Advanced filter")}</Dialog.Title><Dialog.Close asChild><button aria-label={t("Close advanced filter")}><X/></button></Dialog.Close><span>{t("Match")}</span><div><button aria-pressed={match === "all"} onClick={() => setMatch("all")}>{t("All filters")}</button><button aria-pressed={match === "any"} onClick={() => setMatch("any")}>{t("Any filter")}</button></div><footer><button onClick={() => setFilters(emptyDocumentFilters())}>{t("Clear all")}</button><Dialog.Close asChild><button className="is-primary">{t("Done")}</button></Dialog.Close></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
    </div>
  );
}

function relativeDocumentDate(value: string) { const days = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 86400000)); return days < 1 ? "today" : `${days}d ago` }

type DocumentFilterField = "creator" | "owner" | "project" | "cycle" | "dates";
type DocumentGrouping = "project" | "owner" | "cycle" | "recency" | "none";
type DocumentOrdering = "name" | "created" | "updated" | "owner" | "project";
type DocumentProperty = "created" | "updated" | "owner" | "project";

function emptyDocumentFilters(): Record<DocumentFilterField, Set<string>> { return { creator: new Set(), owner: new Set(), project: new Set(), cycle: new Set(), dates: new Set() }; }
function documentFilterLabel(field: DocumentFilterField) { return ({ creator: "Creator", owner: "Owner", project: "Project", cycle: "Cycle", dates: "Dates" })[field]; }
function uniqueUsers(users: BootstrapData["users"]) { return [...new Map(users.map(user => [user.id, user])).values()]; }
function documentMatchesDate(document: FlowDocument, value: string) { const date = new Date(document.updatedAt); if (value === "today") return isToday(date); if (value === "week") return isThisWeek(date); if (value === "month") return isThisMonth(date); return !isThisMonth(date); }
function documentOrderValue(document: FlowDocument, ordering: DocumentOrdering, ownerFor: (document: FlowDocument) => BootstrapData["viewer"], projectById: Map<string, BootstrapData["projects"][number]>) { if (ordering === "name") return document.title; if (ordering === "owner") return ownerFor(document).displayName; if (ordering === "project") return document.projectIds.map(id => projectById.get(id)).find(Boolean)?.name ?? ""; return new Date(ordering === "created" ? document.createdAt : document.updatedAt).getTime(); }
function documentGridStyle(properties: Set<DocumentProperty>) { return { gridTemplateColumns: `22px minmax(0,1fr) ${properties.has("project") ? "150px " : ""}${properties.has("created") ? "120px " : ""}${properties.has("updated") ? "120px " : ""}${properties.has("owner") ? "140px " : ""}40px` } as CSSProperties; }
function groupDocuments(documents: FlowDocument[], grouping: DocumentGrouping, data: BootstrapData, ownerFor: (document: FlowDocument) => BootstrapData["viewer"]) {
  if (grouping === "none") return [{ id: "all", name: "Documents", project: undefined, items: documents }];
  if (grouping === "owner") {
    const groups = new Map<string, { id: string; name: string; project: undefined; items: FlowDocument[] }>();
    for (const document of documents) { const owner = ownerFor(document), group = groups.get(owner.id) ?? { id: `owner:${owner.id}`, name: owner.displayName, project: undefined, items: [] }; group.items.push(document); groups.set(owner.id, group); }
    return [...groups.values()].sort((a,b) => a.name.localeCompare(b.name));
  }
  if (grouping === "cycle") { const cycleGroups = data.cycles.filter(cycle => documents.some(document => cycle.resources.some(resource => resource.documentId === document.id))).map(cycle => ({ id: `cycle:${cycle.id}`, name: cycle.name, project: undefined, items: documents.filter(document => cycle.resources.some(resource => resource.documentId === document.id)) })); const assigned = new Set(cycleGroups.flatMap(group => group.items.map(item => item.id))); return [...cycleGroups, { id: "no-cycle", name: "No cycle", project: undefined, items: documents.filter(document => !assigned.has(document.id)) }].filter(group => group.items.length); }
  if (grouping === 'recency') {
    const buckets = [{ id:'today', name:'Today', items:[] as FlowDocument[] },{ id:'week', name:'This week',items:[] as FlowDocument[] },{id:'month',name:'This month',items:[] as FlowDocument[]},{id:'older',name:'Older',items:[] as FlowDocument[]}];
    for(const document of documents){const date=new Date(document.updatedAt);buckets[isToday(date)?0:isThisWeek(date)?1:isThisMonth(date)?2:3].items.push(document);}
    return buckets.filter(bucket=>bucket.items.length).map(bucket=>({...bucket,project:undefined}));
  }
  const knownProjects = new Set(data.projects.map(project => project.id));
  return [{ id: "team", name: "Team documents", project: undefined, items: documents.filter(document => !document.projectIds.some(id => knownProjects.has(id))) }, ...data.projects.filter(project => documents.some(document => document.projectIds.includes(project.id))).sort((a,b) => a.name.localeCompare(b.name)).map(project => ({ id: project.id, name: project.name, project, items: documents.filter(document => document.projectIds.includes(project.id)) }))].filter(group => group.items.length);
}

function TeamDocumentsDisplayMenu({ descending, grouping, onDirection, onGrouping, onOnlyMyProjects, onOrdering, onProperty, onShowInactive, onlyMyProjects, ordering, properties, showInactive }: { descending: boolean; grouping: DocumentGrouping; onDirection: () => void; onGrouping: (value: DocumentGrouping) => void; onOnlyMyProjects: (value: boolean) => void; onOrdering: (value: DocumentOrdering) => void; onProperty: (value: DocumentProperty) => void; onShowInactive: (value: boolean) => void; onlyMyProjects: boolean; ordering: DocumentOrdering; properties: Set<DocumentProperty>; showInactive: boolean }) {
  const { t } = useI18n();
  const groupLabels: Record<DocumentGrouping, string> = { none: t("None"), owner: t("Owner"), cycle: t("Cycle"), project: t("Project"), recency: t("Recency") };
  const orderLabels: Record<DocumentOrdering, string> = { name: t("Name"), created: t("Created"), updated: t("Last edited"), owner: t("Owner"), project: t("Project") };
  return <Popover.Root><Popover.Trigger asChild><button aria-label={t("Display options")} className="team-documents-icon-button" type="button"><DisplayIcon/></button></Popover.Trigger><Popover.Portal><Popover.Content data-flow-motion="floating" align="end" className="team-directory-display-menu team-documents-display-menu" sideOffset={4}>
    <div className="team-directory-order-row"><span>{t("Grouping")}</span><div><select aria-label={t("Grouping")} onChange={event => onGrouping(event.target.value as DocumentGrouping)} value={grouping}>{(Object.keys(groupLabels) as DocumentGrouping[]).map(value => <option key={value} value={value}>{groupLabels[value]}</option>)}</select></div></div>
    <div className="team-directory-order-row"><span>{t("Ordering")}</span><div><select aria-label={t("Ordering")} onChange={event => onOrdering(event.target.value as DocumentOrdering)} value={ordering}>{(Object.keys(orderLabels) as DocumentOrdering[]).map(value => <option key={value} value={value}>{orderLabels[value]}</option>)}</select><button aria-label={t("Direction")} onClick={onDirection} title={t(descending ? "Descending" : "Ascending")} type="button">{descending ? <ArrowDownWideNarrow/> : <ArrowUpNarrowWide/>}</button></div></div>
    <div className="team-directory-visibility">{[[t("Show inactive projects"), showInactive, onShowInactive], [t("Show only my projects"), onlyMyProjects, onOnlyMyProjects]].map(([label, checked, update]) => <label key={label as string}><span>{label as string}</span><button aria-checked={checked as boolean} onClick={() => (update as (value: boolean) => void)(!(checked as boolean))} role="switch" type="button"><i/></button></label>)}</div>
    <div className="team-directory-properties"><span>{t("Display properties")}</span><div>{(["project", "owner", "updated", "created"] as DocumentProperty[]).filter(value => value !== "project" || grouping !== "project").map(value => <button aria-pressed={properties.has(value)} key={value} onClick={() => onProperty(value)} type="button">{orderLabels[value]}</button>)}</div></div>
  </Popover.Content></Popover.Portal></Popover.Root>;
}

function readDocumentDirectoryState() {
  const params = new URLSearchParams(typeof location === "undefined" ? "" : location.search);
  const filters = emptyDocumentFilters();
  (Object.keys(filters) as DocumentFilterField[]).forEach(field => params.getAll(`doc-${field}`).forEach(value => filters[field].add(value)));
  const grouping = (params.get('doc-group') === 'created' ? 'recency' : ["project", "owner", "cycle", "recency", "none"].includes(params.get("doc-group") ?? "") ? params.get("doc-group") : "project") as DocumentGrouping;
  const ordering = (["name", "created", "updated", "owner", "project"].includes(params.get("doc-order") ?? "") ? params.get("doc-order") : "name") as DocumentOrdering;
  const properties = new Set<DocumentProperty>((params.get("doc-columns") ?? "created,updated,owner,project").split(",").filter(value => ["created", "updated", "owner", "project"].includes(value)) as DocumentProperty[]);
  return { filters, search: params.get('doc-search') ?? '', match: params.get("doc-match") === "any" ? "any" as const : "all" as const, grouping, ordering, descending: params.get("doc-direction") === "desc", showInactive: params.get("doc-inactive") === "1", onlyMyProjects: params.get("doc-mine") === "1", properties };
}
function persistDocumentDirectoryState(state: ReturnType<typeof readDocumentDirectoryState>) {
  if (typeof history === "undefined" || typeof location === "undefined") return;
  const params = new URLSearchParams(location.search);
  (Object.keys(state.filters) as DocumentFilterField[]).forEach(field => { params.delete(`doc-${field}`); state.filters[field].forEach(value => params.append(`doc-${field}`, value)); });
  const setOptional = (key: string, value: string, keep: boolean) => { if (keep) params.set(key, value); else params.delete(key); };
  setOptional('doc-search', state.search, Boolean(state.search));
  setOptional("doc-match", state.match, state.match !== "all"); setOptional("doc-group", state.grouping, state.grouping !== "project"); setOptional("doc-order", state.ordering, state.ordering !== "name"); setOptional("doc-direction", "desc", state.descending); setOptional("doc-inactive", "1", state.showInactive); setOptional("doc-mine", "1", state.onlyMyProjects);
  const columnValue = [...state.properties].sort().join(","); setOptional("doc-columns", columnValue, columnValue !== "created,owner,project,updated");
  const query = params.toString(); history.replaceState(history.state, "", `${location.pathname}${query ? `?${query}` : ""}${location.hash}`);
}
import { AnimatedCollapse } from '@/components/ui/motion';
