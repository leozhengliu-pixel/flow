import * as Dialog from "@radix-ui/react-dialog";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  Activity,
  Bell,
  BookOpen,
  Check,
  ChevronRight,
  FileText,
  Link2,
  Menu,
  MoreHorizontal,
  Plus,
  Search,
  Settings,
  Star,
  Trash2,
  X,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

import {
  addFavorite,
  addSubscription,
  createDocument,
  createTeamResourceSection,
  deleteTeamResource,
  deleteTeamResourceSection,
  fetchTeamResources,
  pinTeamResource,
  removeFavorite,
  removeSubscription,
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
  teamViewsPath,
  loopsPath,
  projectPath,
} from "@/lib/app-routes";
import { DisplayIcon, FilterIcon } from "@/components/ui/view-action-icons";
import { ViewIconPicker } from "@/components/views/view-icon-picker";
import { useI18n } from "@/i18n/i18n";
import type {
  BootstrapData,
  FlowDocument,
  Team,
  TeamPinnedResource,
  TeamResourceSection,
} from "@/types/flow";

import "./team-overview-page.css";

type View = "overview" | "documents";

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
  const favorite = data.favorites.some(
    (item) =>
      item.resourceType === "team" &&
      item.resourceId === team.id &&
      item.userId === data.viewer.id,
  );
  const subscribed = data.subscriptions.some(
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
  const saveTeamVisual = async (visual:{icon:string;color:string}) => { await updateTeam(data.workspace.urlKey,team.id,visual); await onReload() }
  const saveTeamName = async (name:string) => { const value=name.trim(); if(value&&value!==team.name){await updateTeam(data.workspace.urlKey,team.id,{name:value});await onReload()} }
  const saveTeamDescription = async (description:string) => { await updateStructuredTeamSettings(team.id,{description:description.trim()});await onReload() }
  const teamTabs=<nav className="team-home-tabs" aria-label={t('Team views')}><a aria-current={view==="overview"?"page":undefined} href={teamHomePath(data.workspace.urlKey,team.key)} onClick={event=>{event.preventDefault();onNavigate(teamHomePath(data.workspace.urlKey,team.key))}}>{t('Overview')}</a><a aria-current={view==="documents"?"page":undefined} href={teamDocumentsPath(data.workspace.urlKey,team.key)} onClick={event=>{event.preventDefault();onNavigate(teamDocumentsPath(data.workspace.urlKey,team.key))}}>{t('Documents')}</a><a href={loopsPath(data.workspace.urlKey)} onClick={event=>{event.preventDefault();onNavigate(loopsPath(data.workspace.urlKey))}}>Loops</a><a href={settingsPath(data.workspace.urlKey,"team",team.key,"members")} onClick={event=>{event.preventDefault();onNavigate(settingsPath(data.workspace.urlKey,"team",team.key,"members"))}}>{t('Members')}</a></nav>
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
        <span className="team-home-glyph" style={{ color: team.color }}>
          {team.key.slice(0, 1)}
        </span>
        <h1 data-i18n-ignore>{team.name}</h1>
        <button
          aria-label={favorite ? "Remove from favorites" : "Add to favorites"}
          onClick={() =>
            void (
              favorite
                ? removeFavorite("team", team.id)
                : addFavorite("team", team.id)
            ).then(onReload)
          }
        >
          <Star fill={favorite ? "currentColor" : "none"} />
        </button>
        <DropdownMenu.Root>
          <DropdownMenu.Trigger asChild>
            <button aria-label="Team actions">
              <MoreHorizontal />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <DropdownMenu.Content align="start" className="team-home-menu">
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
                  <Settings />
                  Team settings
                </a>
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={()=>void navigator.clipboard.writeText(`${location.origin}${teamHomePath(data.workspace.urlKey,team.key)}`)}><Link2/>Copy URL</DropdownMenu.Item>
              <DropdownMenu.Item onSelect={()=>onNavigate(`/${encodeURIComponent(data.workspace.urlKey)}/team/${encodeURIComponent(team.key)}/archive/issues`)}><FileText/>Open archive</DropdownMenu.Item>
              <DropdownMenu.Item
                onSelect={() =>
                  void (
                    subscribed
                      ? removeSubscription("team", team.id)
                      : addSubscription("team", team.id, ["updates"])
                  ).then(onReload)
                }
              >
                <Bell />
                {subscribed ? "Unsubscribe" : "Subscribe"}
              </DropdownMenu.Item>
              <DropdownMenu.Item onSelect={()=>onNavigate(settingsPath(data.workspace.urlKey,"team",team.key,"notifications"))}><Bell/>Configure Slack notifications…</DropdownMenu.Item>
            </DropdownMenu.Content>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
        <button aria-label="Copy team URL" onClick={()=>void navigator.clipboard.writeText(`${location.origin}${teamHomePath(data.workspace.urlKey,team.key)}`)}><Link2/></button>
        <span />
      </header>
      {teamTabs}
      {view === "overview" ? (
        <div className="team-home-scroll">
          <section className="team-home-intro">
            <ViewIconPicker color={team.color} icon={team.icon||"Team"} onChange={visual=>void saveTeamVisual(visual)} prependTeam triggerClassName="team-home-large-glyph"/>
            <h2 data-i18n-ignore contentEditable suppressContentEditableWarning role="textbox" aria-label="Team name" onBlur={event=>void saveTeamName(event.currentTarget.textContent??'')}>{team.name}</h2>
            <p data-placeholder="Add a description…" contentEditable suppressContentEditableWarning role="textbox" aria-label="Team description" onBlur={event=>void saveTeamDescription(event.currentTarget.textContent??'')}>{data.teamSettings[team.id]?.description??''}</p>
          </section>
          <section className="team-resources">
            <header>
              <h2>Team resources</h2>
              <span />
              <button onClick={() => setResourceOpen(true)}>
                <Plus />
                Add resources
              </button>
              <button onClick={() => setSectionOpen(true)}>
                <Plus />
                Add section
              </button>
            </header>
            {loading ? (
              <p className="team-resource-empty">Loading…</p>
            ) : resources.length === 0 && sections.length === 0 ? (
              <p className="team-resource-empty">
                Add documents and links. Organize by creating sections.
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
          </section>
          <section className="team-home-members">
            <header>
              <h2>Members</h2>
              <button
                onClick={() =>
                  onNavigate(
                    settingsPath(
                      data.workspace.urlKey,
                      "team",
                      team.key,
                      "members",
                    ),
                  )
                }
              >
                <Plus />
                Add members
              </button>
            </header>
            <div>
              {teamMembers.slice(0, 8).map((user) => (
                <span key={user!.id} title={user!.displayName}>
                  {user!.displayName.slice(0, 2).toUpperCase()}
                </span>
              ))}
            </div>
          </section>
          <section className="team-home-shortcuts">
            <h2>Go to</h2>
            <a href={settingsPath(data.workspace.urlKey,"team",team.key,"notifications")} onClick={event=>{event.preventDefault();onNavigate(settingsPath(data.workspace.urlKey,"team",team.key,"notifications"))}}><Bell/>Connect channel<ChevronRight/></a>
            <a
              href={settingsPath(data.workspace.urlKey, "team", team.key)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(
                  settingsPath(data.workspace.urlKey, "team", team.key),
                );
              }}
            >
              <Settings />
              Team settings
              <ChevronRight />
            </a>
            <a
              href={teamIssuesPath(data.workspace.urlKey, team.key)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(teamIssuesPath(data.workspace.urlKey, team.key));
              }}
            >
              <FileText />
              Issues
              <ChevronRight />
            </a>
            <a
              href={teamInitiativesPath(data.workspace.urlKey, team.key)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(
                  teamInitiativesPath(data.workspace.urlKey, team.key),
                );
              }}
            >
              <Star />
              Initiatives
              <ChevronRight />
            </a>
            <a href={teamCyclesPath(data.workspace.urlKey,team.key)} onClick={event=>{event.preventDefault();onNavigate(teamCyclesPath(data.workspace.urlKey,team.key))}}><Activity/>Cycles<ChevronRight/></a>
            <a
              href={teamProjectsPath(data.workspace.urlKey, team.key)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(teamProjectsPath(data.workspace.urlKey, team.key));
              }}
            >
              <BookOpen />
              Projects
              <ChevronRight />
            </a>
            <a
              href={teamViewsPath(data.workspace.urlKey, team.key)}
              onClick={(event) => {
                event.preventDefault();
                onNavigate(teamViewsPath(data.workspace.urlKey, team.key));
              }}
            >
              <Search />
              Views
              <ChevronRight />
            </a>
          </section>
        </div>
      ) : (
        <TeamDocuments
          data={data}
          documents={documents}
          onNavigate={onNavigate}
          onNew={() => void newDocument()}
        />
      )}
      {resourceOpen && (
        <ResourcePicker
          data={data}
          sections={sections}
          team={team}
          onClose={() => setResourceOpen(false)}
          onSaved={async () => {
            setResourceOpen(false);
            await reloadResources();
          }}
        />
      )}
      {sectionOpen && (
        <SectionEditor
          onClose={() => setSectionOpen(false)}
          onSave={async (name) => {
            await createTeamResourceSection(team.id, name);
            setSectionOpen(false);
            await reloadResources();
          }}
        />
      )}
    </main>
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
                <MoreHorizontal />
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
                <MoreHorizontal />
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

function ResourcePicker({
  data,
  sections,
  team,
  onClose,
  onSaved,
}: {
  data: BootstrapData;
  sections: TeamResourceSection[];
  team: Team;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const [query, setQuery] = useState(""),
    [sectionId, setSectionId] = useState(""),
    [url, setUrl] = useState(""),
    [title, setTitle] = useState(""),
    [linkMode, setLinkMode] = useState(false),
    [saving, setSaving] = useState(false);
  const documents = data.documents
    .filter((item) => item.title.toLowerCase().includes(query.toLowerCase()))
    .slice(0, 25);
  const pin = async (resource: Partial<TeamPinnedResource>) => {
    setSaving(true);
    try {
      await pinTeamResource(team.id, { ...resource, sectionId });
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
          className="team-resource-dialog"
        >
          <Dialog.Title>Add resources</Dialog.Title>
          <Dialog.Close aria-label="Close">
            <X />
          </Dialog.Close>
          <div className="team-resource-dialog-tabs">
            <button aria-pressed={!linkMode} onClick={() => setLinkMode(false)}>
              Documents
            </button>
            <button aria-pressed={linkMode} onClick={() => setLinkMode(true)}>
              Link
            </button>
          </div>
          <label>
            Section
            <select
              value={sectionId}
              onChange={(event) => setSectionId(event.target.value)}
            >
              <option value="">Team resources</option>
              {sections.map((section) => (
                <option key={section.id} value={section.id}>
                  {section.name}
                </option>
              ))}
            </select>
          </label>
          {linkMode ? (
            <>
              <label>
                Title
                <input
                  autoFocus
                  value={title}
                  onChange={(event) => setTitle(event.target.value)}
                />
              </label>
              <label>
                URL
                <input
                  type="url"
                  value={url}
                  onChange={(event) => setUrl(event.target.value)}
                />
              </label>
              <footer>
                <button onClick={onClose}>Cancel</button>
                <button
                  className="primary"
                  disabled={
                    saving || !title.trim() || !/^https?:\/\//i.test(url)
                  }
                  onClick={() =>
                    void pin({
                      resourceType: "link",
                      title: title.trim(),
                      url: url.trim(),
                    })
                  }
                >
                  Add link
                </button>
              </footer>
            </>
          ) : (
            <>
              <label className="team-resource-search">
                <Search />
                <input
                  autoFocus
                  aria-label="Search documents"
                  placeholder="Search documents…"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                />
              </label>
              <div className="team-resource-results">
                {documents.map((document) => (
                  <button
                    disabled={saving}
                    key={document.id}
                    onClick={() =>
                      void pin({
                        resourceType: "document",
                        resourceId: document.id,
                        title: document.title,
                      })
                    }
                  >
                    <FileText />
                    <span data-i18n-ignore>{document.title}</span>
                    <Plus />
                  </button>
                ))}
                {!documents.length && <p>No documents found</p>}
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function SectionEditor({
  onClose,
  onSave,
}: {
  onClose: () => void;
  onSave: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState(""),
    [saving, setSaving] = useState(false);
  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="team-home-overlay" />
        <Dialog.Content
          aria-describedby={undefined}
          className="team-section-dialog"
        >
          <Dialog.Title>Add section</Dialog.Title>
          <Dialog.Close aria-label="Close">
            <X />
          </Dialog.Close>
          <label>
            Section name
            <input
              autoFocus
              value={name}
              onChange={(event) => setName(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && name.trim()) {
                  setSaving(true);
                  void onSave(name.trim()).catch(() => setSaving(false));
                }
              }}
            />
          </label>
          <footer>
            <button onClick={onClose}>Cancel</button>
            <button
              className="primary"
              disabled={saving || !name.trim()}
              onClick={() => {
                setSaving(true);
                void onSave(name.trim()).catch(() => setSaving(false));
              }}
            >
              Create
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
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
          <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className="team-document-more" type="button"><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="team-home-menu"><DropdownMenu.Item onSelect={() => onNavigate(documentPath(data.workspace.urlKey, document))}>Open document</DropdownMenu.Item><DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(`${location.origin}${documentPath(data.workspace.urlKey, document)}`)}>Copy link</DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
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
