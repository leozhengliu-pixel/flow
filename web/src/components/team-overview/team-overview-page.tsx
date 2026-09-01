import * as Dialog from "@radix-ui/react-dialog";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  Check,
  ChevronRight,
  FileText,
  Link2,
  Menu,
  Plus,
  Search,
  Trash2,
  X,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { toast } from "sonner";

import {
  addFavorite,
  createDocument,
  createTeamResourceSection,
  deleteTeamResource,
  deleteTeamResourceSection,
  fetchTeamResources,
  pinTeamResource,
  removeFavorite,
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
import { DisplayIcon, FilterIcon } from "@/components/ui/view-action-icons";
import { UserAvatar } from "@/components/ui/user-avatar";
import { ProjectIcon, SlackIcon } from "@/components/issue/issue-icons";
import { ViewGlyph, ViewIconPicker } from "@/components/views/view-icon-picker";
import { LoopsDirectory } from "@/components/loops/loops-page";
import { useI18n } from "@/i18n/i18n";
import type {
  BootstrapData,
  FlowDocument,
  Team,
  TeamPinnedResource,
  TeamResourceSection,
} from "@/types/flow";

import "./team-overview-page.css";

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
  const [sections, setSections] = useState<TeamResourceSection[]>([]);
  const [resources, setResources] = useState<TeamPinnedResource[]>([]);
  const [loading, setLoading] = useState(true);
  const [resourceOpen, setResourceOpen] = useState(false);
  const [sectionOpen, setSectionOpen] = useState(false);
  const [membersOpen, setMembersOpen] = useState(false);
  const favorite = data.favorites.some(
    (item) =>
      item.resourceType === "team" &&
      item.resourceId === team.id &&
      item.userId === data.viewer.id,
  );
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await fetchTeamResources(team.id);
      setSections(result.sections);
      setResources(result.resources);
    } finally {
      setLoading(false);
    }
  }, [team.id]);
  useEffect(() => {
    void load();
  }, [load]);
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
        ...sections,
      ].map((section) => ({
        section,
        items: resources.filter(
          (item) => (item.sectionId ?? "") === section.id,
        ),
      })),
    [resources, sections, team.id],
  );
  const reloadResources = async () => {
    await load();
    await onReload();
  };
  const newDocument = async () => {
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
    }
  };
  const newPinnedDocument = async () => {
    try {
      const document = await createDocument({
        title: "New document",
        teamIds: [team.id],
      });
      await pinTeamResource(team.id, {
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
      <a
        aria-current={view === "loops" ? "page" : undefined}
        href={teamLoopsPath(data.workspace.urlKey, team.key)}
        onClick={(event) => {
          event.preventDefault();
          onNavigate(teamLoopsPath(data.workspace.urlKey, team.key));
        }}
      >
        Loops
      </a>
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
          onClick={onOpenSidebar}
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
            onClick={() =>
              void (
                favorite
                  ? removeFavorite("team", team.id)
                  : addFavorite("team", team.id)
              ).then(onReload)
            }
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
              <DropdownMenu.Content
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
                  onLink={() => setResourceOpen(true)}
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
                <button
                  aria-label={t("Add members")}
                  className="team-home-add-members"
                  title={t("Add members")}
                  onClick={() => setMembersOpen(true)}
                >
                  <PlusIcon />
                </button>
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
          data={data}
          documents={documents}
          onNavigate={onNavigate}
          onNew={() => void newDocument()}
        />
      ) : view === "loops" ? (
        <LoopsDirectory
          data={data}
          embedded
          onNavigate={onNavigate}
          onOpenSidebar={onOpenSidebar}
          onReload={onReload}
          teamId={team.id}
        />
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

function TeamSettingsIcon() {
  return (
    <SvgIcon>
      <path d="M8.58 1a.5.5 0 0 1 .49.42l.25 1.48a.49.49 0 0 0 .33.38c.18.059.353.132.52.22a.49.49 0 0 0 .51 0l1.22-.87a.5.5 0 0 1 .64 0l.82.82a.5.5 0 0 1 .05.64l-.87 1.22a.49.49 0 0 0 0 .51c.088.167.161.34.22.52a.49.49 0 0 0 .38.33l1.48.25a.5.5 0 0 1 .42.49v1.17a.5.5 0 0 1-.42.49l-1.48.25a.49.49 0 0 0-.38.33 3.38 3.38 0 0 1-.22.52.49.49 0 0 0 0 .51l.87 1.22a.5.5 0 0 1-.05.64l-.82.82a.5.5 0 0 1-.64.05l-1.22-.87a.49.49 0 0 0-.51 0 3.38 3.38 0 0 1-.52.22.49.49 0 0 0-.33.38l-.25 1.48a.5.5 0 0 1-.49.42H7.42a.5.5 0 0 1-.49-.42l-.25-1.52a.49.49 0 0 0-.33-.38 3.38 3.38 0 0 1-.52-.22.49.49 0 0 0-.51 0l-1.22.87a.5.5 0 0 1-.64-.05l-.82-.82a.5.5 0 0 1 0-.64l.87-1.22a.49.49 0 0 0 0-.51 3.38 3.38 0 0 1-.22-.52.49.49 0 0 0-.38-.33l-1.49-.21A.5.5 0 0 1 1 8.58V7.42a.5.5 0 0 1 .42-.49l1.48-.25a.49.49 0 0 0 .38-.33 3.38 3.38 0 0 1 .22-.52.49.49 0 0 0 0-.51L2.59 4.1a.5.5 0 0 1 0-.64l.82-.82a.5.5 0 0 1 .64 0l1.22.87a.49.49 0 0 0 .51 0 3.38 3.38 0 0 1 .52-.22.49.49 0 0 0 .33-.38l.3-1.49A.5.5 0 0 1 7.42 1h1.16ZM8 6a2 2 0 1 0 0 4 2 2 0 0 0 0-4Z" />
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

function ArchiveIcon() {
  return (
    <SvgIcon>
      <path fillRule="evenodd" clipRule="evenodd" d="M9.25 8C9.66421 8 10 8.33579 10 8.75C10 9.16421 9.66421 9.5 9.25 9.5H6.75C6.33579 9.5 6 9.16421 6 8.75C6 8.33579 6.33579 8 6.75 8H9.25Z" />
      <path fillRule="evenodd" clipRule="evenodd" d="M12.75 2C13.9926 2 15 3.00736 15 4.25V5.75C15 6.35471 14.5705 6.85876 14 6.97461V11.75C14 12.9926 12.9926 14 11.75 14H4.2002C2.95755 14 1.9502 12.9926 1.9502 11.75V6.96191C1.40486 6.82746 1 6.33685 1 5.75V4.25C1 3.00736 2.00736 2 3.25 2H12.75ZM3.4502 11.75C3.4502 12.1642 3.78598 12.5 4.2002 12.5H11.75C12.1642 12.5 12.5 12.1642 12.5 11.75V7H3.4502V11.75ZM3.25 3.5C2.83579 3.5 2.5 3.83579 2.5 4.25V5.5H13.5V4.25C13.5 3.83579 13.1642 3.5 12.75 3.5H3.25Z" />
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
  data,
  items,
  onNavigate,
  onReload,
  sections,
  section,
  team,
}: {
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
  const save = async () => {
    if (!section || !name.trim()) return;
    await updateTeamResourceSection(team.id, section.id, { name: name.trim() });
    setEditing(false);
    await onReload();
  };
  return (
    <div className="team-resource-section">
      {section && (
        <header>
          {editing ? (
            <input
              autoFocus
              aria-label="Section name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void save();
                if (event.key === "Escape") setEditing(false);
              }}
              onBlur={() => void save()}
            />
          ) : (
            <button onClick={() => setEditing(true)}>{section.name}</button>
          )}
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild>
              <button aria-label={`Open menu ${section.name}`}>
                <TeamMoreIcon />
              </button>
            </DropdownMenu.Trigger>
            <DropdownMenu.Portal>
              <DropdownMenu.Content className="team-home-menu" align="end">
                <DropdownMenu.Item onSelect={() => setEditing(true)}>
                  Rename
                </DropdownMenu.Item>
                <DropdownMenu.Separator />
                <DropdownMenu.Item
                  className="danger"
                  onSelect={() =>
                    void deleteTeamResourceSection(team.id, section.id).then(
                      onReload,
                    )
                  }
                >
                  <Trash2 />
                  Delete section
                </DropdownMenu.Item>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
        </header>
      )}
      {items.map((item) => (
        <div className="team-resource-row" key={item.id}>
          {item.resourceType === "document" ? <FileText /> : <Link2 />}
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
              <DropdownMenu.Content className="team-home-menu" align="end">
                <DropdownMenu.Sub>
                  <DropdownMenu.SubTrigger>
                    Move to
                    <ChevronRight />
                  </DropdownMenu.SubTrigger>
                  <DropdownMenu.Portal>
                    <DropdownMenu.SubContent
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
      ))}
    </div>
  );
}

function ResourceCommandMenu({
  documents,
  onLink,
  onNew,
  onSaved,
  resources,
  team,
}: {
  documents: FlowDocument[];
  onLink: () => void;
  onNew: () => void;
  onSaved: () => Promise<void>;
  resources: TeamPinnedResource[];
  team: Team;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  const pinnedIds = new Set(
    resources
      .filter((resource) => resource.resourceType === "document")
      .map((resource) => resource.resourceId),
  );
  const visible = documents
    .filter((document) =>
      document.title.toLowerCase().includes(query.trim().toLowerCase()),
    )
    .slice(0, 25);
  const pin = async (document: FlowDocument) => {
    if (saving || pinnedIds.has(document.id)) return;
    setSaving(true);
    try {
      await pinTeamResource(team.id, {
        resourceType: "document",
        resourceId: document.id,
        title: document.title,
      });
      await onSaved();
    } catch (error) {
      toast.error(
        error instanceof Error ? error.message : "Could not add resource",
      );
    } finally {
      setSaving(false);
    }
  };
  return (
    <DropdownMenu.Root onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenu.Trigger asChild>
        <button
          aria-label={t("Add resources")}
          className="team-resource-command"
          title={t("Add resources")}
        >
          <PlusIcon />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          className="team-resource-command-menu"
          sideOffset={4}
        >
          <DropdownMenu.Item onSelect={onNew}>
            <NewDocumentIcon />
            {t("New document")}
          </DropdownMenu.Item>
          <DropdownMenu.Sub>
            <DropdownMenu.SubTrigger>
              <PageIcon />
              {t("Existing documents")}
              <ChevronHomeIcon />
            </DropdownMenu.SubTrigger>
            <DropdownMenu.Portal>
              <DropdownMenu.SubContent
                alignOffset={-6}
                className="team-resource-command-menu team-resource-existing-menu"
                sideOffset={4}
              >
                <label className="team-resource-existing-search">
                  <input
                    aria-label={t("Search documents…")}
                    autoFocus
                    placeholder={t("Search documents…")}
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    onKeyDown={(event) => event.stopPropagation()}
                  />
                  <Search />
                </label>
                <div className="team-resource-existing-list">
                  {visible.map((document) => (
                    <DropdownMenu.CheckboxItem
                      checked={pinnedIds.has(document.id)}
                      disabled={saving || pinnedIds.has(document.id)}
                      key={document.id}
                      onSelect={(event) => {
                        event.preventDefault();
                        void pin(document);
                      }}
                    >
                      <span className="team-resource-checkbox">
                        {pinnedIds.has(document.id) && <Check />}
                      </span>
                      <span data-i18n-ignore>{document.title}</span>
                    </DropdownMenu.CheckboxItem>
                  ))}
                  {!visible.length && (
                    <p>{t("No documents found")}</p>
                  )}
                </div>
              </DropdownMenu.SubContent>
            </DropdownMenu.Portal>
          </DropdownMenu.Sub>
          <DropdownMenu.Separator />
          <DropdownMenu.Item onSelect={onLink}>
            <NewLinkIcon />
            {t("New link…")}
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function AddLinkDialog({
  team,
  onClose,
  onSaved,
}: {
  team: Team;
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
        <Dialog.Overlay className="team-home-overlay" />
        <Dialog.Content
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
  const save = async () => {
    if (!name.trim() || saving) return;
    setSaving(true);
    try {
      await onSave(name.trim());
    } catch (error) {
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
        onBlur={() => !name.trim() && onCancel()}
        onChange={(event) => setName(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") void save();
          if (event.key === "Escape") onCancel();
        }}
      />
      <span aria-hidden="true">▾</span>
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
  const [saving, setSaving] = useState(false);
  const memberIds = new Set(
    data.teamMembers
      .filter((membership) => membership.teamId === team.id)
      .map((membership) => membership.userId),
  );
  const candidates = data.users.filter(
    (user) =>
      !memberIds.has(user.id) &&
      user.displayName.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const save = async () => {
    if (!selected.length || saving) return;
    setSaving(true);
    try {
      await Promise.all(
        selected.map((userId) =>
          setTeamMembership(data.workspace.urlKey, team.id, userId, true),
        ),
      );
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
        <Dialog.Overlay className="team-home-overlay" />
        <Dialog.Content
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
              <DropdownMenu.Content
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
                        <span data-i18n-ignore>{user.displayName}</span>
                      </DropdownMenu.CheckboxItem>
                    );
                  })}
                  {!candidates.length && <p>{t("No members available")}</p>}
                </div>
              </DropdownMenu.Content>
            </DropdownMenu.Portal>
          </DropdownMenu.Root>
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
  const members = data.teamMembers
    .filter((membership) => membership.teamId === team.id)
    .map((membership) => {
      const user = data.users.find((item) => item.id === membership.userId);
      const workspaceMember = data.members.find((item) => item.user.id === membership.userId);
      return user ? { membership, user, workspaceMember } : undefined;
    })
    .filter(Boolean);
  return <section className="team-members-directory">
    <div className="team-members-toolbar">
      <button className="team-members-add" onClick={onAdd}><PlusIcon/>{t("Add a member")}</button>
      <button aria-label={t("Display options")} className="team-members-display"><DisplayIcon/></button>
    </div>
    <header>
      <button>{t("Name")} <span>↓</span></button>
      <button>{t("Email")}</button>
      <button>{t("Role")}</button>
      <span/>
    </header>
    <div className="team-members-list">
      {members.map((entry, index) => {
        const value = entry!;
        const profile = `/${data.workspace.urlKey}/profiles/${encodeURIComponent(value.user.name)}`;
        return <a href={profile} key={value.user.id} onClick={event => { event.preventDefault(); onNavigate(profile) }}>
          <span className="team-members-person">
            <UserAvatar avatarUrl={value.user.avatarUrl} color={memberColor(index)} name={value.user.displayName}/>
            <span><strong data-i18n-ignore>{value.user.displayName}</strong><small data-i18n-ignore>{value.user.name}</small></span>
          </span>
          <span data-i18n-ignore>{value.user.email}</span>
          <span className="team-members-role">{t(value.workspaceMember?.role === "admin" ? "Workspace admin" : "Member")}</span>
          <DropdownMenu.Root>
            <DropdownMenu.Trigger asChild><button aria-label={t("Open menu")} onClick={event => { event.preventDefault(); event.stopPropagation() }}><TeamMoreIcon/></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><DropdownMenu.Content align="end" className="team-home-menu" sideOffset={4}>
              <DropdownMenu.Item onSelect={() => onNavigate(profile)}>{t("View profile")}</DropdownMenu.Item>
              <DropdownMenu.Separator/>
              <DropdownMenu.Item className="danger" onSelect={() => void setTeamMembership(data.workspace.urlKey, team.id, value.user.id, false).then(onReload)}>{t("Remove from team")}</DropdownMenu.Item>
            </DropdownMenu.Content></DropdownMenu.Portal>
          </DropdownMenu.Root>
        </a>;
      })}
    </div>
  </section>;
}

function TeamDocuments({
  data,
  documents,
  onNavigate,
  onNew,
}: {
  data: BootstrapData;
  documents: FlowDocument[];
  onNavigate: (path: string) => void;
  onNew: () => void;
}) {
  const [query, setQuery] = useState("");
  const [filterOpen, setFilterOpen] = useState(false);
  const [displayOpen, setDisplayOpen] = useState(false);
  const [ordering, setOrdering] = useState<"name" | "created" | "updated" | "owner">("name");
  const [selected, setSelected] = useState<string[]>([]);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const visible = documents
    .filter((item) => item.title.toLowerCase().includes(query.toLowerCase()))
    .sort((a, b) => ordering === "name" ? a.title.localeCompare(b.title) : ordering === "owner" ? a.creator.displayName.localeCompare(b.creator.displayName) : +new Date(ordering === "created" ? b.createdAt : b.updatedAt) - +new Date(ordering === "created" ? a.createdAt : a.updatedAt));
  const grouped = [...data.projects.filter(project => visible.some(document => document.projectIds.includes(project.id))).map(project => ({ id: project.id, name: project.name, project, items: visible.filter(document => document.projectIds.includes(project.id)) })), { id: "", name: "No project", project: undefined, items: visible.filter(document => !document.projectIds.length) }].filter(group => group.items.length);
  return (
    <div className="team-documents">
      <div className="team-documents-toolbar">
        <button aria-label="New document" className="team-documents-new" onClick={onNew} type="button"><Plus size={14}/>New document</button>
        <button aria-expanded={filterOpen} aria-label="Add filter" onClick={() => { setFilterOpen(value => !value); setDisplayOpen(false) }} type="button"><FilterIcon/></button>
        <button aria-expanded={displayOpen} aria-label="Display options" onClick={() => { setDisplayOpen(value => !value); setFilterOpen(false) }} type="button"><DisplayIcon/></button>
        {filterOpen && <div className="team-documents-menu is-filter"><label><Search/><input autoFocus aria-label="Find documents" placeholder="Find documents…" value={query} onChange={event => setQuery(event.target.value)}/></label><button onClick={() => { setQuery(""); setFilterOpen(false) }} type="button"><X size={13}/>Clear filter</button></div>}
        {displayOpen && <div className="team-documents-menu is-display"><strong>Ordering</strong>{(["name","created","updated","owner"] as const).map(value => <button aria-checked={ordering === value} key={value} onClick={() => { setOrdering(value); setDisplayOpen(false) }} role="menuitemradio" type="button">{value === "updated" ? "Last edited" : value[0].toUpperCase()+value.slice(1)}{ordering === value && <Check size={12}/>}</button>)}</div>}
      </div>
      <header>
        <button onClick={() => setOrdering("name")} type="button">Name</button>
        <button onClick={() => setOrdering("created")} type="button">Created</button>
        <button onClick={() => setOrdering("updated")} type="button">Last edited</button>
        <button onClick={() => setOrdering("owner")} type="button">Owner</button>
      </header>
      {grouped.map(group => <section className="team-documents-group" key={group.id || "none"}>
        <div className="team-documents-group-header"><button aria-expanded={!collapsed.has(group.id)} aria-label={collapsed.has(group.id)?"Expand group":"Collapse group"} onClick={()=>setCollapsed(current=>{const next=new Set(current);if(next.has(group.id))next.delete(group.id);else next.add(group.id);return next})} type="button"><ChevronRight/></button>{"project" in group && group.project ? <a data-i18n-ignore href={projectPath(data.workspace.urlKey, group.project)} onClick={event => { event.preventDefault(); onNavigate(projectPath(data.workspace.urlKey, group.project)) }}>{group.name}</a> : <span>{group.name}</span>}<small>{group.items.length}</small></div>
        {!collapsed.has(group.id)&&group.items.map(document => <a className="team-document-row" href={documentPath(data.workspace.urlKey, document)} key={document.id} onClick={event => { if ((event.target as HTMLElement).closest("button,input")) { event.preventDefault(); return } event.preventDefault(); onNavigate(documentPath(data.workspace.urlKey, document)) }}>
          <label aria-label="Select document"><input checked={selected.includes(document.id)} onChange={() => setSelected(current => current.includes(document.id) ? current.filter(id => id !== document.id) : [...current, document.id])} type="checkbox"/><span><Check size={10}/></span></label>
          <FileText/><strong data-i18n-ignore>{document.title}</strong><time>{relativeDocumentDate(document.createdAt)}</time><time>{relativeDocumentDate(document.updatedAt)}</time><button className="team-document-owner" type="button"><span>{document.creator.displayName.slice(0,2).toUpperCase()}</span><i data-i18n-ignore>{document.creator.displayName}</i></button>
          <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className="team-document-more" type="button"><TeamMoreIcon/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="team-home-menu"><DropdownMenu.Item onSelect={() => onNavigate(documentPath(data.workspace.urlKey, document))}>Open document</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(`${location.origin}${documentPath(data.workspace.urlKey, document)}`)}>Copy link</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
        </a>)}
      </section>)}
      {!visible.length && (
        <div className="team-documents-empty">
          <FileText />
          <strong>No documents</strong>
          <p>Create a document for this team to share plans and decisions.</p>
          <button onClick={onNew}>
            <Plus />
            New document
          </button>
        </div>
      )}
    </div>
  );
}

function relativeDocumentDate(value: string) { const days = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 86400000)); return days < 1 ? "today" : `${days}d ago` }
