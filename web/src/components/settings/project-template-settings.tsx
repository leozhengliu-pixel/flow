import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { useEffect, useRef, useState, type CSSProperties, type KeyboardEvent } from "react";

import { toast } from "@/lib/toast";
import {
  createProjectTemplate,
  deleteProjectTemplate,
  updateProjectTemplate,
} from "@/lib/api";
import { labelTeamScopeIds, labelsForResource, toggleGroupedLabelIds } from "@/lib/labels";
import { useI18n } from "@/i18n/i18n";
import { MentionTextField } from "@/components/editor/mention-text-field";
import {
  ProjectStatusIcon,
  PriorityIcon,
  TeamIcon,
} from "@/components/issue/issue-icons";
import { PersonPicker } from "@/components/issue/core-property-pickers";
import { PropertyMenu, type PropertyOption } from "@/components/property/property-menu";
import { ProjectDependencyPicker } from "@/components/projects-page/project-dependency-picker";
import { confirmAction } from "@/components/ui/action-dialog-service";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import {
  FlowDropdownMenuContent,
  FlowMenuItem,
  FlowMenuOptions,
  FlowMenuSeparator,
  FlowSubmenu,
  type FlowMenuOption,
} from "@/components/ui/row-context-menu";
import { UserAvatar } from "@/components/ui/user-avatar";
import { ViewGlyph, ViewIconPicker } from "@/components/views/view-icon-picker";
import { normalizeProjectIcon } from "@/components/views/project-icon";
import type {
  BootstrapData,
  ProjectDependencyRelationInput,
  ProjectTemplate,
  ProjectTemplateIssue,
  TemplateMilestone,
} from "@/types/flow";

import { MilestoneDiamond, NoPriorityGlyph, TemplateGlyph } from "./project-template-glyphs";
import "./project-template-settings.css";

const PRIORITIES = ["No priority", "Urgent", "High", "Medium", "Low"] as const;
const INITIATIVE_GROUPS: Record<string, string> = { proposed: "Proposed", planned: "Planned", active: "Active", completed: "Completed", canceled: "Canceled" };
const DEFAULT_COLOR = "#bec2c8";

type Props = {
  data: BootstrapData;
  mode?: "new" | "edit";
  templateId?: string;
  onCreate: () => void;
  onOpen: (template: ProjectTemplate) => void;
  onDuplicate: (template: ProjectTemplate) => void;
  onNavigateList: () => void;
  onReload: () => Promise<void>;
};

/** Settings › Projects › Templates: the workspace project template list and its editor page. */
export function ProjectTemplateSettings({
  data,
  mode,
  templateId,
  onCreate,
  onOpen,
  onDuplicate,
  onNavigateList,
  onReload,
}: Props) {
  const { t } = useI18n();
  if (mode === "new")
    return (
      <ProjectTemplateEditor
        data={data}
        source={duplicateSource(data)}
        onClose={onNavigateList}
        onReload={onReload}
      />
    );
  if (mode === "edit") {
    const template = data.projectTemplates.find((item) => item.id === templateId);
    return template ? (
      <ProjectTemplateEditor
        key={template.id}
        data={data}
        template={template}
        onClose={onNavigateList}
        onDuplicate={() => onDuplicate(template)}
        onReload={onReload}
      />
    ) : (
      <div className="pt-not-found">{t("Project template not found")}</div>
    );
  }
  return (
    <ProjectTemplateList
      data={data}
      items={data.projectTemplates.filter(
        (item) => (item.visibility ?? "workspace") === "workspace" || !item.visibilityTeamId,
      )}
      onCreate={onCreate}
      onOpen={onOpen}
      onDuplicate={onDuplicate}
      onReload={onReload}
    />
  );
}

function duplicateSource(data: BootstrapData) {
  const id = new URLSearchParams(location.search).get("duplicate");
  return id ? data.projectTemplates.find((item) => item.id === id) : undefined;
}

function templateCreateUrl(data: BootstrapData, template: ProjectTemplate) {
  return `${location.origin}/${encodeURIComponent(data.workspace.urlKey)}/projects/all?create=1&template=${encodeURIComponent(template.id)}`;
}

async function copyTemplateUrl(data: BootstrapData, template: ProjectTemplate, t: (value: string) => string) {
  await navigator.clipboard.writeText(templateCreateUrl(data, template));
  toast.success(t("Prefilled create project URL copied to clipboard"), {
    description: t("The URL contains the selected template."),
  });
}

async function confirmTemplateDelete(template: ProjectTemplate, t: (value: string) => string, locale: string) {
  return confirmAction(
    locale === "zh-CN" ? `删除模板“${template.name}”？` : `Delete the template "${template.name}"?`,
    { description: t("You cannot undo this action."), confirmLabel: t("Delete"), danger: true },
  );
}

/* ----------------------------------------------------------------------------------------------
 * List
 * -------------------------------------------------------------------------------------------- */

function ProjectTemplateList({
  data,
  items,
  onCreate,
  onOpen,
  onDuplicate,
  onReload,
}: {
  data: BootstrapData;
  items: ProjectTemplate[];
  onCreate: () => void;
  onOpen: (template: ProjectTemplate) => void;
  onDuplicate: (template: ProjectTemplate) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  return (
    <div className="pt-list-page" data-i18n-ignore>
      <header className="pt-list-header">
        <h1>{t("Project templates")}</h1>
        <p>
          {t(
            "These templates are available when creating projects for any team in the workspace. To create templates that only apply to specific teams, add them as team templates.",
          )}
          <a href="https://flow.app/docs/project-templates" target="_blank" rel="noreferrer">
            {t("Docs")}
          </a>
        </p>
      </header>
      <ProjectTemplateListCard data={data} items={items} onCreate={onCreate} onOpen={onOpen} onDuplicate={onDuplicate} onReload={onReload} />
    </div>
  );
}

/**
 * The project template card shared by Settings › Projects › Templates and a team's Settings › Templates:
 * "No project templates" / "N project templates" with "New template", then one row per template.
 * Templates inherited from a parent team show where they come from and cannot be opened here.
 */
export function ProjectTemplateListCard({
  data,
  items,
  onCreate,
  onOpen,
  onDuplicate,
  onReload,
  inheritedFrom,
}: {
  data: BootstrapData;
  items: ProjectTemplate[];
  onCreate: () => void;
  onOpen: (template: ProjectTemplate) => void;
  onDuplicate: (template: ProjectTemplate) => void;
  onReload: () => Promise<void>;
  /** The parent team a template is inherited from, when it is not this team's own. */
  inheritedFrom?: (template: ProjectTemplate) => string | undefined;
}) {
  const { t, locale } = useI18n();
  const remove = async (item: ProjectTemplate) => {
    if (!(await confirmTemplateDelete(item, t, locale))) return;
    try {
      await deleteProjectTemplate(item.id);
      await onReload();
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  return (
    <section className="pt-list-card" data-i18n-ignore>
      <header>
        <h3>
          {items.length
            ? t(items.length === 1 ? "1 project template" : "project template count").replace("{count}", String(items.length))
            : t("No project templates")}
        </h3>
        <button type="button" aria-label={t("Create new project template")} onClick={onCreate}>
          <FlowGlyph name="plus" />
          {t("New template")}
        </button>
      </header>
      {items.map((item) => {
        const parent = inheritedFrom?.(item);
        return (
          <div className={`pt-list-row${parent ? " is-inherited" : ""}`} key={item.id}>
            {!parent && <button type="button" aria-label={item.name} className="pt-row-link" onClick={() => onOpen(item)} />}
            <span className="pt-row-icon" style={{ "--pt-icon-color": item.color || DEFAULT_COLOR } as CSSProperties}>
              <ViewGlyph color={item.color || DEFAULT_COLOR} icon={normalizeProjectIcon(item.icon)} />
            </span>
            <span className="pt-row-text">
              <strong data-i18n-ignore>{item.name}</strong>
              <small>
                {parent ? (
                  <>
                    {t("Inherited from")} <b data-i18n-ignore>{parent}</b>
                  </>
                ) : item.templateDescription ? (
                  <span data-i18n-ignore>{item.templateDescription}</span>
                ) : item.creator?.displayName ? (
                  <>
                    {t("Created by")} <b data-i18n-ignore>{item.creator.displayName}</b>
                  </>
                ) : (
                  relativeTemplateTime(item.createdAt, t)
                )}
              </small>
            </span>
            {!parent && (
              <DropdownMenu.Root modal={false}>
                <DropdownMenu.Trigger asChild>
                  <button type="button" aria-label={t("Open menu")} className="pt-row-menu-trigger">
                    <FlowGlyph name="ellipsis" />
                  </button>
                </DropdownMenu.Trigger>
                <DropdownMenu.Portal>
                  <FlowDropdownMenuContent label={t("Template actions")} align="end" className="pt-template-menu">
                    <FlowMenuItem icon={<FlowGlyph name="edit" />} label="Edit" onSelect={() => onOpen(item)} />
                    <FlowMenuItem icon={<TemplateGlyph name="duplicate" />} label="Duplicate…" onSelect={() => onDuplicate(item)} />
                    <FlowMenuItem
                      icon={<FlowGlyph name="copy" />}
                      label="Copy URL to create project from template"
                      onSelect={() => void copyTemplateUrl(data, item, t)}
                    />
                    <FlowMenuSeparator />
                    <FlowMenuItem icon={<FlowGlyph name="delete" />} label="Delete" onSelect={() => void remove(item)} />
                  </FlowDropdownMenuContent>
                </DropdownMenu.Portal>
              </DropdownMenu.Root>
            )}
          </div>
        );
      })}
    </section>
  );
}

/**
 * A team's full-page project template editor (team Settings › Templates › project/new, project/{id}/edit,
 * project/{id}/duplicate): the workspace editor with the team preset as the template's team and visibility.
 */
export function TeamProjectTemplateEditor({
  data,
  teamId,
  template,
  source,
  onClose,
  onDuplicate,
  onReload,
}: {
  data: BootstrapData;
  teamId: string;
  template?: ProjectTemplate;
  source?: ProjectTemplate;
  onClose: () => void;
  onDuplicate?: () => void;
  onReload: () => Promise<void>;
}) {
  return (
    <ProjectTemplateEditor
      key={template?.id ?? `new:${source?.id ?? ""}`}
      data={data}
      template={template}
      source={source}
      teamId={teamId}
      backLabel="Team templates"
      onClose={onClose}
      onDuplicate={onDuplicate}
      onReload={onReload}
    />
  );
}

function relativeTemplateTime(value: string, t: (value: string) => string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const minutes = Math.max(0, Math.round((Date.now() - date.getTime()) / 60000));
  if (minutes < 1) return t("just now");
  if (minutes < 60) return t(minutes === 1 ? "1 minute ago" : "{count} minutes ago").replace("{count}", String(minutes));
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

/* ----------------------------------------------------------------------------------------------
 * Editor
 * -------------------------------------------------------------------------------------------- */

type Draft = {
  name: string;
  projectName: string;
  summary: string;
  description: string;
  icon: string;
  color: string;
  statusId: string;
  priority: number;
  leadId: string;
  memberIds: string[];
  teamIds: string[];
  initiativeIds: string[];
  labelIds: string[];
  dependencyRelations: ProjectDependencyRelationInput[];
  milestones: TemplateMilestone[];
  issues: ProjectTemplateIssue[];
  visibilityTeamId: string;
};

function draftFrom(data: BootstrapData, base: ProjectTemplate | undefined, name?: string): Draft {
  const defaultStatusId =
    data.projectStatuses.find((item) => item.type === "backlog")?.id ?? data.projectStatuses[0]?.id ?? "";
  const relations: ProjectDependencyRelationInput[] = base?.dependencyRelations?.length
    ? base.dependencyRelations.map((item) => ({ projectId: item.projectId, type: item.type }))
    : (base?.dependencyIds ?? []).map((projectId) => ({ projectId, type: "blocked_by" as const }));
  return {
    name: name ?? base?.name ?? "",
    projectName: base?.projectName ?? "",
    summary: base?.summary ?? "",
    description: base?.description ?? "",
    icon: normalizeProjectIcon(base?.icon),
    color: base?.color ?? DEFAULT_COLOR,
    statusId: base?.statusId ?? defaultStatusId,
    priority: base?.priority ?? 0,
    leadId: base?.leadId ?? "",
    memberIds: base?.memberIds ?? [],
    teamIds: base?.teamIds ?? [],
    initiativeIds: base?.initiativeIds ?? [],
    labelIds: base?.labelIds ?? [],
    dependencyRelations: relations,
    milestones: base?.milestones ?? [],
    issues: base?.issues ?? [],
    visibilityTeamId: base?.visibilityTeamId ?? "",
  };
}

type MilestoneComposerState = { id?: string } | null;
type IssueComposerState = { id?: string; parentId?: string } | null;

function ProjectTemplateEditor({
  data,
  template,
  source,
  teamId,
  backLabel = "Project templates",
  onClose,
  onDuplicate,
  onReload,
}: {
  data: BootstrapData;
  template?: ProjectTemplate;
  source?: ProjectTemplate;
  /** A team's own template: new templates start with this team and team visibility. */
  teamId?: string;
  backLabel?: string;
  onClose: () => void;
  onDuplicate?: () => void;
  onReload: () => Promise<void>;
}) {
  const { t, locale } = useI18n();
  // The draft starts from the template once; saving reloads and closes the editor.
  const [initial] = useState(() => {
    const draft = draftFrom(data, template ?? source, source ? `Copy of ${source.name}` : undefined);
    return teamId && !template && !source ? { ...draft, teamIds: [teamId], visibilityTeamId: teamId } : draft;
  });
  const [draft, setDraft] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [milestoneComposer, setMilestoneComposer] = useState<MilestoneComposerState>(null);
  const [issueComposer, setIssueComposer] = useState<IssueComposerState>(null);
  const nameRef = useRef<HTMLInputElement>(null);
  const issuesRef = useRef<HTMLDivElement>(null);
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((current) => ({ ...current, [key]: value }));
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial);

  const close = async () => {
    if (
      dirty &&
      !(await confirmAction(t("Discard changes?"), {
        description: t("Confirm that you want to discard your changes."),
        confirmLabel: t("Discard"),
        danger: true,
      }))
    )
      return;
    onClose();
  };

  const save = async () => {
    if (saving) return;
    if (!draft.name.trim()) {
      nameRef.current?.focus();
      toast.error(t("Template name is required"));
      return;
    }
    setSaving(true);
    try {
      const input = {
        name: draft.name.trim(),
        projectName: draft.projectName.trim(),
        summary: draft.summary,
        description: draft.description,
        icon: draft.icon,
        color: draft.color,
        statusId: draft.statusId,
        priority: draft.priority,
        leadId: draft.leadId,
        memberIds: draft.memberIds,
        teamIds: draft.teamIds,
        initiativeIds: draft.initiativeIds,
        labelIds: draft.labelIds,
        dependencyRelations: draft.dependencyRelations,
        dependencyIds: draft.dependencyRelations.filter((item) => item.type === "blocked_by").map((item) => item.projectId),
        milestones: draft.milestones,
        issues: draft.issues,
        visibility: draft.visibilityTeamId ? ("teams" as const) : ("workspace" as const),
        visibilityTeamId: draft.visibilityTeamId,
      };
      if (template) await updateProjectTemplate(template.id, input);
      else await createProjectTemplate(input);
      await onReload();
      onClose();
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!template || !(await confirmTemplateDelete(template, t, locale))) return;
    try {
      await deleteProjectTemplate(template.id);
      await onReload();
      onClose();
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };

  const openIssueComposer = (state: NonNullable<IssueComposerState>) => {
    setIssueComposer(state);
    window.requestAnimationFrame(() => issuesRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" }));
  };

  return (
    <div className="pt-editor" data-i18n-ignore>
      <a className="pt-back" href={teamId ? location.pathname.replace(/\/templates\/project\/.*$/, "/templates") : `${location.pathname.split("/settings/")[0]}/settings/project-templates`} onClick={(event) => { event.preventDefault(); void close(); }}>
        <FlowGlyph name="chevronLeft" width={14} height={14} />
        <span>{t(backLabel)}</span>
      </a>
      <div className="pt-editor-inner">
        <header className="pt-title">
          <h1>{t(template ? "Edit project template" : "New project template")}</h1>
          {template && (
            <DropdownMenu.Root modal={false}>
              <DropdownMenu.Trigger asChild>
                <button type="button" className="pt-title-menu" aria-label={t("Open menu")}>
                  <FlowGlyph name="ellipsis" />
                </button>
              </DropdownMenu.Trigger>
              <DropdownMenu.Portal>
                <FlowDropdownMenuContent label={t("Template actions")} align="end" className="pt-template-menu">
                  <FlowMenuItem icon={<TemplateGlyph name="duplicate" />} label="Duplicate…" onSelect={onDuplicate} />
                  <FlowMenuItem
                    icon={<FlowGlyph name="copy" />}
                    label="Copy URL to create project from template"
                    onSelect={() => void copyTemplateUrl(data, template, t)}
                  />
                  <FlowMenuSeparator />
                  <FlowMenuItem icon={<FlowGlyph name="delete" />} label="Delete" onSelect={() => void remove()} />
                </FlowDropdownMenuContent>
              </DropdownMenu.Portal>
            </DropdownMenu.Root>
          )}
        </header>
        <div className="pt-fields">
          <div className="pt-name">
            <label htmlFor="pt-template-name">
              <span>{t("Name")}</span>
            </label>
            <input
              id="pt-template-name"
              ref={nameRef}
              autoFocus
              autoComplete="off"
              placeholder={t("Add a descriptive name…")}
              value={draft.name}
              onChange={(event) => set("name", event.target.value)}
            />
          </div>
          <div className="pt-card-group">
            <section className="pt-card">
              <div className="pt-card-body">
                <div className="pt-card-head">
                  <div className="pt-identity" style={{ "--pt-icon-tile": draft.color } as CSSProperties}>
                    <ViewIconPicker
                      ariaLabel={t("Choose icon")}
                      color={draft.color}
                      icon={draft.icon}
                      onChange={(visual) => setDraft((current) => ({ ...current, icon: visual.icon, color: visual.color }))}
                      triggerClassName="pt-icon-trigger"
                    />
                    <input
                      className="pt-project-name"
                      aria-label={t("Project name")}
                      placeholder={t("Project name")}
                      autoComplete="off"
                      value={draft.projectName}
                      onChange={(event) => set("projectName", event.target.value)}
                    />
                  </div>
                  <div className="pt-summary">
                    <input
                      aria-label={t("Project summary")}
                      placeholder={t("Add a short summary…")}
                      autoComplete="off"
                      value={draft.summary}
                      onChange={(event) => set("summary", event.target.value)}
                    />
                  </div>
                  <div className="pt-pills">
                    <TemplatePills
                      data={data}
                      draft={draft}
                      set={set}
                      onAddIssue={() => openIssueComposer({})}
                    />
                  </div>
                  <div className="pt-description">
                    <MentionTextField
                      ariaLabel={t("Project description")}
                      placeholder={t("Write a description, a project brief, or collect ideas…")}
                      value={draft.description}
                      users={data.users}
                      onChange={(value) => set("description", value)}
                      onSubmit={() => void save()}
                    />
                  </div>
                </div>
                <div className="pt-section-slot">
                  <MilestonesSection
                    milestones={draft.milestones}
                    composer={milestoneComposer}
                    onComposer={setMilestoneComposer}
                    onChange={(milestones) =>
                      setDraft((current) => ({
                        ...current,
                        milestones,
                        // Issues keep only milestones that still exist.
                        issues: current.issues.map((issue) =>
                          issue.milestoneId && !milestones.some((item) => item.id === issue.milestoneId)
                            ? { ...issue, milestoneId: undefined }
                            : issue,
                        ),
                      }))
                    }
                  />
                </div>
                <div className="pt-section-slot pt-issues-slot" ref={issuesRef}>
                  <IssuesSection
                    data={data}
                    issues={draft.issues}
                    milestones={draft.milestones}
                    defaultTeamId={draft.teamIds[0]}
                    composer={issueComposer}
                    onComposer={(state) => (state ? openIssueComposer(state) : setIssueComposer(null))}
                    onChange={(issues) => set("issues", issues)}
                  />
                </div>
              </div>
            </section>
            <footer className="pt-footer">
              <div className="pt-footer-visibility">
                <label>
                  <span>{t("Visibility")}</span>
                </label>
                <VisibilityMenu
                  teamId={draft.visibilityTeamId}
                  teams={data.teams}
                  onChange={(teamId) => set("visibilityTeamId", teamId)}
                />
              </div>
              <div className="pt-footer-actions">
                <button type="button" className="pt-button" onClick={() => void close()}>
                  {t("Cancel")}
                </button>
                <button
                  type="button"
                  className="pt-button pt-button--primary"
                  aria-label={t(template ? "Save" : "Create")}
                  aria-disabled={saving || undefined}
                  onClick={() => void save()}
                >
                  {t(template ? "Save" : "Create")}
                </button>
              </div>
            </footer>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------------------------------------
 * Property pills
 * -------------------------------------------------------------------------------------------- */

function priorityOptions(t: (value: string) => string): PropertyOption[] {
  return PRIORITIES.map((label, index) => ({
    id: String(index),
    label: t(label),
    icon: index === 0 ? <NoPriorityGlyph size={16} /> : <PriorityIcon priority={index} size={16} />,
    shortcut: String(index),
  }));
}

function userOptions(data: BootstrapData, emptyLabel?: string): PropertyOption[] {
  return [
    ...(emptyLabel ? [{ id: "", label: emptyLabel, icon: <TemplateGlyph name="lead" size={14} /> }] : []),
    ...data.users
      .filter((item) => item.active)
      .map((item) => ({
        id: item.id,
        label: item.displayName,
        keywords: `${item.name} ${item.email}`,
        icon: <UserAvatar className="avatar" name={item.displayName} avatarUrl={item.avatarUrl} />,
        i18nIgnore: true,
      })),
  ];
}

function teamOptions(data: BootstrapData, emptyLabel?: string): PropertyOption[] {
  return [
    ...(emptyLabel ? [{ id: "", label: emptyLabel, icon: <FlowGlyph name="leadTeam" width={14} height={14} /> }] : []),
    ...data.teams
      .filter((item) => !item.archivedAt)
      .map((item) => ({
        id: item.id,
        label: item.name,
        keywords: item.key,
        labelContent: (
          <>
            {item.name}
            <span className="pt-option-detail">{item.key}</span>
          </>
        ),
        icon: (
          <span className="pt-team-avatar">
            <TeamIcon team={item} size={14} />
          </span>
        ),
        i18nIgnore: true,
      })),
  ];
}

function labelOptions(data: BootstrapData, type: "issue" | "project", teamId?: string): PropertyOption[] {
  const groups = new Map(
    data.labelGroups
      .filter((group) => group.resourceType === type && !group.archivedAt)
      .map((group) => [group.id, group.name]),
  );
  const scopes = teamId ? new Set(labelTeamScopeIds(teamId, data.teams, data.teamSettings)) : undefined;
  return labelsForResource(data.labels, type, data.labelGroups)
    .filter((item) => !scopes || !item.scope || item.scope === "Workspace" || scopes.has(item.scope))
    .map((item) => ({
      id: item.id,
      label: item.name,
      color: item.color,
      description: item.description,
      issueCount: item.issueCount,
      scope: item.scope,
      groupId: item.groupId,
      groupLabel: item.groupId ? groups.get(item.groupId) : undefined,
      i18nIgnore: true,
    }));
}

function toggleId(values: string[], id: string) {
  return values.includes(id) ? values.filter((value) => value !== id) : [...values, id];
}

function countLabel(t: (value: string) => string, count: number, one: string, many: string) {
  return count === 1 ? t(one) : t(many).replace("{count}", String(count));
}

function TemplatePills({
  data,
  draft,
  set,
  onAddIssue,
}: {
  data: BootstrapData;
  draft: Draft;
  set: <K extends keyof Draft>(key: K, value: Draft[K]) => void;
  onAddIssue: () => void;
}) {
  const { t } = useI18n();
  const status = data.projectStatuses.find((item) => item.id === draft.statusId);
  const lead = data.users.find((item) => item.id === draft.leadId);
  const members = data.users.filter((item) => draft.memberIds.includes(item.id));
  const teams = data.teams.filter((item) => draft.teamIds.includes(item.id));
  const initiatives = data.initiatives.filter((item) => draft.initiativeIds.includes(item.id));
  const projectLabels = labelOptions(data, "project");
  const labels = projectLabels.filter((item) => draft.labelIds.includes(item.id));
  const pill = (set: boolean, extra = "") => `pt-pill${set ? " is-set" : ""}${extra ? ` ${extra}` : ""}`;
  const issueCount = draft.issues.length;
  const people = data.users
    .filter((item) => item.active || item.id === draft.leadId || draft.memberIds.includes(item.id))
    .map((item) => ({ id: item.id, label: item.displayName, name: item.name, email: item.email, avatarUrl: item.avatarUrl, active: item.active }));
  return (
    <>
      <PropertyMenu
        compact
        label={t("Status")}
        ariaLabel={t("Change project status")}
        searchPlaceholder={t("Change status…")}
        searchShortcut="P, then S"
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-standard"
        value={status?.name ?? t("Status")}
        valueIsEntityName={Boolean(status)}
        selectedId={draft.statusId}
        triggerClassName={pill(true)}
        icon={<ProjectStatusIcon color={status?.color} name={status?.name} size={14} type={status?.type} />}
        options={data.projectStatuses.map((item, index) => ({
          id: item.id,
          label: item.name,
          icon: <ProjectStatusIcon color={item.color} name={item.name} size={16} type={item.type} />,
          shortcut: String(index + 1),
          i18nIgnore: true,
        }))}
        onChange={(id) => set("statusId", id)}
      />
      <PropertyMenu
        compact
        label={t("Priority")}
        ariaLabel={t("Change project priority")}
        searchPlaceholder={t("Change priority…")}
        searchShortcut="P, then P"
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-standard"
        value={t(PRIORITIES[draft.priority] ?? "No priority")}
        selectedId={String(draft.priority)}
        triggerClassName={pill(true)}
        icon={draft.priority ? <PriorityIcon priority={draft.priority} size={14} /> : <NoPriorityGlyph size={14} />}
        options={priorityOptions(t)}
        onChange={(value) => set("priority", Number(value))}
      />
      <PersonPicker
        ariaLabel="Set project lead"
        label="Lead"
        emptyTriggerLabel="Lead"
        emptyOptionLabel="No lead"
        emptyOptionShortcut="0"
        people={people}
        hideSearch
        searchPlaceholder="Set lead…"
        searchShortcut="P, then A"
        selectedId={draft.leadId}
        unselectedGroupLabel="Users from the project teams"
        showUnselectedGroupWhenEmpty
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-lead"
        triggerClassName={pill(Boolean(lead))}
        trigger={
          <>
            {lead ? <UserAvatar className="avatar" name={lead.displayName} avatarUrl={lead.avatarUrl} /> : <TemplateGlyph name="lead" size={14} />}
            <span data-i18n-ignore={lead ? "" : undefined}>{lead ? lead.displayName : t("Lead")}</span>
          </>
        }
        onChange={(id) => set("leadId", id)}
      />
      <PersonPicker
        ariaLabel="Change project members"
        label="Members"
        emptyTriggerLabel="Members"
        multiple
        people={people}
        hideSearch
        searchPlaceholder="Change members…"
        searchShortcut="P, then M"
        selectedIds={draft.memberIds}
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-members"
        triggerClassName={pill(members.length > 0)}
        trigger={
          <>
            {members[0] ? <UserAvatar className="avatar" name={members[0].displayName} avatarUrl={members[0].avatarUrl} /> : <FlowGlyph name="members" width={14} height={14} />}
            <span data-i18n-ignore={members.length === 1 ? "" : undefined}>
              {members.length === 1 ? members[0].displayName : members.length ? countLabel(t, members.length, "1 member", "{count} members") : t("Members")}
            </span>
          </>
        }
        onChange={(id) => set("memberIds", toggleId(draft.memberIds, id))}
      />
      <PropertyMenu
        compact
        multiple
        label={t("Team")}
        ariaLabel={t("Change project teams")}
        searchPlaceholder={t("Change teams…")}
        searchShortcut="P, then T"
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-standard"
        value={teams.length === 1 ? teams[0].name : teams.length ? countLabel(t, teams.length, "1 team", "{count} teams") : t("Team")}
        valueIsEntityName={teams.length === 1}
        selectedIds={draft.teamIds}
        triggerClassName={pill(false, "pt-pill--team")}
        icon={teams[0] ? <TeamIcon team={teams[0]} size={14} /> : <FlowGlyph name="leadTeam" width={14} height={14} />}
        options={teamOptions(data).map((item) => ({ ...item, groupLabel: t("Your teams") }))}
        onChange={(id) => set("teamIds", toggleId(draft.teamIds, id))}
      />
      <PropertyMenu
        compact
        multiple
        label={t("Initiatives")}
        ariaLabel={t("Change project initiatives")}
        hideSearch
        searchPlaceholder={t("Change initiatives…")}
        searchShortcut="P, then N"
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-initiatives"
        value={initiatives.length === 1 ? initiatives[0].name : initiatives.length ? countLabel(t, initiatives.length, "1 initiative", "{count} initiatives") : t("Initiatives")}
        valueIsEntityName={initiatives.length === 1}
        selectedIds={draft.initiativeIds}
        triggerClassName={pill(initiatives.length > 0)}
        icon={<TemplateGlyph name="initiative" size={14} />}
        options={data.initiatives.map((item) => ({
          id: item.id,
          label: item.name,
          groupLabel: t(INITIATIVE_GROUPS[item.status] ?? "Active"),
          icon: <TemplateGlyph name="initiative" size={16} style={{ color: item.color }} />,
          i18nIgnore: true,
        }))}
        onChange={(id) => set("initiativeIds", toggleId(draft.initiativeIds, id))}
      />
      <PropertyMenu
        compact
        multiple
        kind="project-labels"
        label={t("Labels")}
        ariaLabel={t("Change labels")}
        searchPlaceholder={t("Add labels…")}
        searchShortcut="P, then L"
        surfaceClassName="pt-picker project-details-sidebar__property-menu is-labels"
        value={labels.length === 1 ? labels[0].label : labels.length ? countLabel(t, labels.length, "1 label", "{count} labels") : t("Labels")}
        valueIsEntityName={labels.length === 1}
        selectedIds={draft.labelIds}
        triggerClassName={pill(labels.length > 0)}
        icon={labels.length === 1 ? <span className="pt-label-dot" style={{ background: labels[0].color }} /> : <FlowGlyph name="labels" width={14} height={14} />}
        options={projectLabels}
        onChange={(id) => set("labelIds", toggleGroupedLabelIds(draft.labelIds, id, projectLabels))}
      />
      <ProjectDependencyPicker
        projects={data.projects
          .filter((item) => !item.archivedAt)
          .map((item) => ({ id: item.id, label: item.name, icon: item.icon, color: item.color }))}
        value={draft.dependencyRelations}
        onChange={(value) => set("dependencyRelations", value)}
        trigger={
          <button type="button" className={pill(draft.dependencyRelations.length > 0)} aria-label={t("Add dependencies")}>
            <FlowGlyph name="dependencies" width={14} height={14} />
            <span>
              {draft.dependencyRelations.length
                ? countLabel(t, draft.dependencyRelations.length, "1 dependency", "{count} dependencies")
                : t("Dependencies")}
            </span>
          </button>
        }
      />
      <button type="button" className={pill(issueCount > 0)} aria-label={t("Add issue")} onClick={onAddIssue}>
        <TemplateGlyph name="issues" size={14} />
        <span>{issueCount ? countLabel(t, issueCount, "1 issue", "{count} issues") : t("Issues")}</span>
      </button>
    </>
  );
}

/* ----------------------------------------------------------------------------------------------
 * Visibility
 * -------------------------------------------------------------------------------------------- */

function VisibilityMenu({
  teamId,
  teams,
  onChange,
}: {
  teamId: string;
  teams: BootstrapData["teams"];
  onChange: (teamId: string) => void;
}) {
  const { t } = useI18n();
  const team = teams.find((item) => item.id === teamId);
  const options: FlowMenuOption[] = [
    { id: "", label: "Workspace", icon: <TemplateGlyph name="building" /> },
    ...teams
      .filter((item) => !item.archivedAt)
      .map((item) => ({
        id: item.id,
        label: item.name,
        translate: false,
        group: "Your teams",
        icon: (
          <span className="pt-team-avatar">
            <TeamIcon team={item} size={14} />
          </span>
        ),
      })),
  ];
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button type="button" className="pt-button pt-visibility" aria-label={t("Template visibility")} title={t("Template visibility")}>
          {team ? <TeamIcon team={team} size={16} /> : <TemplateGlyph name="building" />}
          <span data-i18n-ignore={team ? "" : undefined}>{team ? team.name : t("Workspace")}</span>
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <FlowDropdownMenuContent label={t("Template visibility")} className="pt-visibility-menu">
          <FlowMenuOptions options={options} selected={new Set([teamId])} onChoose={onChange} />
        </FlowDropdownMenuContent>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

/* ----------------------------------------------------------------------------------------------
 * Milestones
 * -------------------------------------------------------------------------------------------- */

function SectionHeader({
  label,
  count,
  open,
  onToggle,
  onAdd,
  addLabel,
}: {
  label: string;
  count: number;
  open: boolean;
  onToggle: () => void;
  onAdd?: () => void;
  addLabel?: string;
}) {
  const { t } = useI18n();
  return (
    <header className="pt-section-header">
      <button type="button" className="pt-section-title" aria-expanded={count ? open : undefined} onClick={count ? onToggle : onAdd}>
        <span>{label}</span>
        {count > 0 && <span className="pt-section-count">{count}</span>}
        {count > 0 && <TemplateGlyph name="caret" className={open ? "is-open" : undefined} />}
      </button>
      {onAdd && (
        <button type="button" className="pt-icon-button" aria-label={t(addLabel ?? "Add")} onClick={onAdd}>
          <FlowGlyph name="plus" width={14} height={14} />
        </button>
      )}
    </header>
  );
}

function MilestonesSection({
  milestones,
  composer,
  onComposer,
  onChange,
}: {
  milestones: TemplateMilestone[];
  composer: MilestoneComposerState;
  onComposer: (state: MilestoneComposerState) => void;
  onChange: (milestones: TemplateMilestone[]) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(true);
  if (composer) {
    const editing = composer.id ? milestones.find((item) => item.id === composer.id) : undefined;
    return (
      <MilestoneComposer
        key={composer.id ?? "new"}
        milestone={editing}
        onCancel={() => onComposer(null)}
        onSubmit={(value) => {
          onChange(
            editing
              ? milestones.map((item) => (item.id === editing.id ? { ...item, ...value } : item))
              : [...milestones, { id: newId("template_milestone"), ...value }],
          );
          onComposer(null);
        }}
      />
    );
  }
  return (
    <div className="pt-section">
      <SectionHeader
        label={t("Milestones")}
        count={milestones.length}
        open={open}
        onToggle={() => setOpen((value) => !value)}
        onAdd={milestones.length ? undefined : () => onComposer({})}
      />
      {open &&
        milestones.map((item) => (
          <div className="pt-row" key={item.id}>
            <button type="button" className="pt-row-main" onClick={() => onComposer({ id: item.id })}>
              <MilestoneDiamond />
              <span data-i18n-ignore>{item.name}</span>
            </button>
            <button
              type="button"
              className="pt-icon-button"
              aria-label={t("Remove milestone")}
              onClick={() => onChange(milestones.filter((value) => value.id !== item.id))}
            >
              <FlowGlyph name="closeChat" width={14} height={14} />
            </button>
          </div>
        ))}
      {milestones.length > 0 && (
        <div className="pt-section-footer">
          <button type="button" className="pt-add-button" aria-label={t("Add milestone")} onClick={() => onComposer({})}>
            <FlowGlyph name="plus" width={14} height={14} />
            <span>{t("Add milestone")}</span>
          </button>
        </div>
      )}
    </div>
  );
}

function MilestoneComposer({
  milestone,
  onCancel,
  onSubmit,
}: {
  milestone?: TemplateMilestone;
  onCancel: () => void;
  onSubmit: (value: { name: string; description?: string }) => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(milestone?.name ?? "");
  const [description, setDescription] = useState(milestone?.description ?? "");
  const submit = () => {
    if (!name.trim()) return;
    onSubmit({ name: name.trim(), description: description.trim() || undefined });
  };
  const cancel = async () => {
    const changed = name !== (milestone?.name ?? "") || description !== (milestone?.description ?? "");
    if (changed && !(await confirmAction(t("Discard changes?"), { confirmLabel: t("Confirm"), danger: true }))) return;
    onCancel();
  };
  return (
    <div className="pt-section pt-composer pt-milestone-composer" onKeyDown={(event) => composerKeys(event, submit, () => void cancel())}>
      <header className="pt-composer-header">
        <span>{t(milestone ? "Edit milestone" : "Create milestone")}</span>
      </header>
      <div className="pt-composer-body">
        <div className="pt-milestone-fields">
          <div className="pt-milestone-name">
            <MilestoneDiamond />
            <input
              autoFocus
              autoComplete="off"
              aria-label={t("Milestone name")}
              placeholder={t("Milestone name")}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="pt-milestone-description">
            <MentionTextField
              ariaLabel={t("Milestone description template")}
              placeholder={t("Add a description template…")}
              value={description}
              onChange={setDescription}
              onSubmit={submit}
            />
          </div>
        </div>
        <div className="pt-composer-actions">
          <button type="button" className="pt-small-button" aria-label={t("Discard changes")} onClick={() => void cancel()}>
            {t("Cancel")}
          </button>
          <button
            type="button"
            className="pt-small-button pt-small-button--primary"
            aria-label={t(milestone ? "Save milestone" : "Add milestone")}
            aria-disabled={!name.trim() || undefined}
            onClick={submit}
          >
            {t(milestone ? "Save milestone" : "Add milestone")}
          </button>
        </div>
      </div>
    </div>
  );
}

function composerKeys(event: KeyboardEvent<HTMLElement>, submit: () => void, cancel: () => void) {
  if (event.key === "Escape") {
    event.preventDefault();
    event.stopPropagation();
    cancel();
  } else if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
    event.preventDefault();
    submit();
  }
}

/* ----------------------------------------------------------------------------------------------
 * Issues
 * -------------------------------------------------------------------------------------------- */

function issueOrder(issues: ProjectTemplateIssue[]) {
  const ids = new Set(issues.map((item) => item.id));
  const children = new Map<string, ProjectTemplateIssue[]>();
  const roots: ProjectTemplateIssue[] = [];
  for (const item of issues) {
    if (item.parentId && ids.has(item.parentId)) children.set(item.parentId, [...(children.get(item.parentId) ?? []), item]);
    else roots.push(item);
  }
  const ordered: Array<{ issue: ProjectTemplateIssue; depth: number }> = [];
  const visit = (item: ProjectTemplateIssue, depth: number) => {
    ordered.push({ issue: item, depth });
    for (const child of children.get(item.id) ?? []) visit(child, depth + 1);
  };
  roots.forEach((item) => visit(item, 0));
  return ordered;
}

function withoutIssue(issues: ProjectTemplateIssue[], id: string) {
  const removed = new Set([id]);
  let grew = true;
  while (grew) {
    grew = false;
    for (const item of issues)
      if (item.parentId && removed.has(item.parentId) && !removed.has(item.id)) {
        removed.add(item.id);
        grew = true;
      }
  }
  return issues.filter((item) => !removed.has(item.id));
}

function IssuesSection({
  data,
  issues,
  milestones,
  defaultTeamId,
  composer,
  onComposer,
  onChange,
}: {
  data: BootstrapData;
  issues: ProjectTemplateIssue[];
  milestones: TemplateMilestone[];
  defaultTeamId?: string;
  composer: IssueComposerState;
  onComposer: (state: IssueComposerState) => void;
  onChange: (issues: ProjectTemplateIssue[]) => void;
}) {
  const { t } = useI18n();
  const [open, setOpen] = useState(true);
  const update = (id: string, patch: Partial<ProjectTemplateIssue>) =>
    onChange(issues.map((item) => (item.id === id ? { ...item, ...patch } : item)));
  if (composer) {
    const editing = composer.id ? issues.find((item) => item.id === composer.id) : undefined;
    return (
      <IssueComposer
        key={composer.id ?? `new-${composer.parentId ?? ""}`}
        data={data}
        issue={editing}
        milestones={milestones}
        defaultTeamId={defaultTeamId}
        onCancel={() => onComposer(null)}
        onSubmit={(value, more) => {
          if (editing) onChange(issues.map((item) => (item.id === editing.id ? { ...item, ...value } : item)));
          else onChange([...issues, { ...value, id: newId("template_issue"), parentId: composer.parentId }]);
          if (!more || editing) onComposer(null);
        }}
      />
    );
  }
  if (!issues.length) return null;
  return (
    <div className="pt-section pt-issues">
      <SectionHeader label={t("Issues")} count={issues.length} open={open} onToggle={() => setOpen((value) => !value)} />
      {open &&
        issueOrder(issues).map(({ issue, depth }) => (
          <IssueRow
            key={issue.id}
            data={data}
            issue={issue}
            depth={depth}
            milestones={milestones}
            onEdit={() => onComposer({ id: issue.id })}
            onAddSubIssue={() => onComposer({ parentId: issue.id })}
            onRemove={() => onChange(withoutIssue(issues, issue.id))}
            onUpdate={(patch) => update(issue.id, patch)}
          />
        ))}
      <div className="pt-section-footer">
        <button type="button" className="pt-add-button" aria-label={t("Add issue")} onClick={() => onComposer({})}>
          <FlowGlyph name="plus" width={14} height={14} />
          <span>{t("Add issue")}</span>
        </button>
      </div>
    </div>
  );
}

function IssueRow({
  data,
  issue,
  depth,
  milestones,
  onEdit,
  onAddSubIssue,
  onRemove,
  onUpdate,
}: {
  data: BootstrapData;
  issue: ProjectTemplateIssue;
  depth: number;
  milestones: TemplateMilestone[];
  onEdit: () => void;
  onAddSubIssue: () => void;
  onRemove: () => void;
  onUpdate: (patch: Partial<ProjectTemplateIssue>) => void;
}) {
  const { t } = useI18n();
  const team = data.teams.find((item) => item.id === issue.teamId);
  const assignee = data.users.find((item) => item.id === issue.assigneeId);
  const issueLabels = labelOptions(data, "issue", issue.teamId);
  const priorityChoices: FlowMenuOption[] = PRIORITIES.map((label, index) => ({
    id: String(index),
    label,
    icon: index === 0 ? <NoPriorityGlyph /> : <PriorityIcon priority={index} size={16} />,
    shortcut: String(index),
  }));
  return (
    <div className="pt-row pt-issue-row" style={depth ? { paddingLeft: 16 + depth * 24 } : undefined}>
      <button type="button" className="pt-issue-title" onClick={onEdit}>
        <span data-i18n-ignore>{issue.title}</span>
      </button>
      <PropertyMenu
        compact
        label={t("Team")}
        ariaLabel={t("Set team")}
        searchPlaceholder={t("Set team…")}
        value={team?.name ?? t("Team")}
        valueIsEntityName={Boolean(team)}
        selectedId={issue.teamId ?? ""}
        triggerClassName="pt-team-chip"
        icon={team ? <TeamIcon team={team} size={14} /> : <FlowGlyph name="leadTeam" width={14} height={14} />}
        options={teamOptions(data, t("No team"))}
        onChange={(id) => onUpdate({ teamId: id || undefined })}
      />
      <div className="pt-issue-actions">
        <PropertyMenu
          compact
          label={t("Assignee")}
          ariaLabel={t("Assign to…")}
          searchPlaceholder={t("Assign to…")}
          value={assignee?.displayName ?? t("No assignee")}
          selectedId={issue.assigneeId ?? ""}
          triggerClassName="pt-assignee-trigger"
          trigger={
            assignee ? (
              <UserAvatar className="avatar" name={assignee.displayName} avatarUrl={assignee.avatarUrl} />
            ) : (
              <TemplateGlyph name="lead" size={18} />
            )
          }
          options={userOptions(data, t("No assignee"))}
          onChange={(id) => onUpdate({ assigneeId: id || undefined })}
        />
        <DropdownMenu.Root modal={false}>
          <DropdownMenu.Trigger asChild>
            <button type="button" className="pt-icon-button" aria-label={t("Open sub-issue menu")}>
              <FlowGlyph name="ellipsis" width={14} height={14} />
            </button>
          </DropdownMenu.Trigger>
          <DropdownMenu.Portal>
            <FlowDropdownMenuContent label={t("Issue actions")} align="start" className="pt-issue-menu">
              <FlowSubmenu icon={<FlowGlyph name="assignee" />} label="Assignee" shortcut="A" search>
                <FlowMenuOptions
                  placeholder="Assign to…"
                  options={[
                    { id: "", label: "No assignee", icon: <TemplateGlyph name="lead" size={16} /> },
                    ...data.users
                      .filter((item) => item.active)
                      .map((item) => ({
                        id: item.id,
                        label: item.displayName,
                        translate: false,
                        keywords: `${item.name} ${item.email}`,
                        icon: <UserAvatar className="avatar" name={item.displayName} avatarUrl={item.avatarUrl} />,
                      })),
                  ]}
                  selected={new Set([issue.assigneeId ?? ""])}
                  onChoose={(id) => onUpdate({ assigneeId: id || undefined })}
                />
              </FlowSubmenu>
              <FlowSubmenu icon={<FlowGlyph name="labels" />} label="Labels" shortcut="L" search>
                <FlowMenuOptions
                  multiple
                  placeholder="Change labels…"
                  options={issueLabels.map((item) => ({
                    id: item.id,
                    label: item.label,
                    translate: false,
                    icon: <span className="pt-label-dot pt-label-dot--menu" style={{ background: item.color }} />,
                  }))}
                  selected={new Set(issue.labelIds)}
                  onChoose={(id) => onUpdate({ labelIds: toggleGroupedLabelIds(issue.labelIds, id, issueLabels) })}
                />
              </FlowSubmenu>
              <FlowSubmenu icon={<FlowGlyph name="priority" />} label="Priority" shortcut="P">
                <FlowMenuOptions
                  options={priorityChoices}
                  selected={new Set([String(issue.priority)])}
                  onChoose={(id) => onUpdate({ priority: Number(id) })}
                />
              </FlowSubmenu>
              <FlowSubmenu icon={<MilestoneDiamond faded={false} />} label="Milestone" shortcut="shift+M" disabled={!milestones.length}>
                <FlowMenuOptions
                  options={[
                    { id: "", label: "No milestone", icon: <MilestoneDiamond /> },
                    ...milestones.map((item) => ({ id: item.id, label: item.name, translate: false, icon: <MilestoneDiamond faded={false} /> })),
                  ]}
                  selected={new Set([issue.milestoneId ?? ""])}
                  onChoose={(id) => onUpdate({ milestoneId: id || undefined })}
                />
              </FlowSubmenu>
              <FlowMenuSeparator />
              <FlowSubmenu icon={<TemplateGlyph name="circle" />} label="Set team" shortcut="mod+shift+M" search>
                <FlowMenuOptions
                  placeholder="Set team…"
                  options={data.teams
                    .filter((item) => !item.archivedAt)
                    .map((item) => ({ id: item.id, label: item.name, translate: false, icon: <TeamIcon team={item} size={16} /> }))}
                  selected={new Set([issue.teamId ?? ""])}
                  onChoose={(id) => onUpdate({ teamId: id || undefined })}
                />
              </FlowSubmenu>
              <FlowMenuSeparator />
              <FlowMenuItem icon={<TemplateGlyph name="subIssue" />} label="Add sub-issue" onSelect={onAddSubIssue} />
              <FlowMenuSeparator />
              <FlowMenuItem icon={<FlowGlyph name="delete" />} label="Remove issue" shortcut="Backspace" onSelect={onRemove} />
            </FlowDropdownMenuContent>
          </DropdownMenu.Portal>
        </DropdownMenu.Root>
      </div>
    </div>
  );
}

function IssueComposer({
  data,
  issue,
  milestones,
  defaultTeamId,
  onCancel,
  onSubmit,
}: {
  data: BootstrapData;
  issue?: ProjectTemplateIssue;
  milestones: TemplateMilestone[];
  defaultTeamId?: string;
  onCancel: () => void;
  onSubmit: (value: Omit<ProjectTemplateIssue, "id" | "parentId">, createMore: boolean) => void;
}) {
  const { t } = useI18n();
  const blank = () => ({
    title: issue?.title ?? "",
    description: issue?.description ?? "",
    teamId: issue?.teamId ?? defaultTeamId ?? "",
    priority: issue?.priority ?? 0,
    assigneeId: issue?.assigneeId ?? "",
    labelIds: issue?.labelIds ?? [],
    milestoneId: issue?.milestoneId ?? "",
  });
  const [value, setValue] = useState(blank);
  const [createMore, setCreateMore] = useState(false);
  const titleRef = useRef<HTMLInputElement>(null);
  const initial = useRef(blank());
  useEffect(() => {
    titleRef.current?.focus({ preventScroll: true });
  }, []);
  const team = data.teams.find((item) => item.id === value.teamId);
  const assignee = data.users.find((item) => item.id === value.assigneeId);
  const milestone = milestones.find((item) => item.id === value.milestoneId);
  const issueLabels = labelOptions(data, "issue", value.teamId || undefined);
  const labels = issueLabels.filter((item) => value.labelIds.includes(item.id));
  const patch = (next: Partial<typeof value>) => setValue((current) => ({ ...current, ...next }));
  const submit = () => {
    if (!value.title.trim()) {
      titleRef.current?.focus();
      return;
    }
    onSubmit(
      {
        title: value.title.trim(),
        description: value.description.trim() || undefined,
        teamId: value.teamId || undefined,
        priority: value.priority,
        assigneeId: value.assigneeId || undefined,
        labelIds: value.labelIds,
        milestoneId: value.milestoneId || undefined,
      },
      createMore,
    );
    if (createMore && !issue) {
      const next = { ...blank(), teamId: value.teamId };
      initial.current = next;
      setValue(next);
      titleRef.current?.focus();
    }
  };
  const cancel = async () => {
    if (
      JSON.stringify(value) !== JSON.stringify(initial.current) &&
      !(await confirmAction(t("Discard changes?"), { confirmLabel: t("Confirm"), danger: true }))
    )
      return;
    onCancel();
  };
  const pill = (isSet: boolean) => `pt-pill pt-pill--composer${isSet ? " is-set" : ""}`;
  return (
    <div className="pt-section pt-composer pt-issue-composer" onKeyDown={(event) => composerKeys(event, submit, () => void cancel())}>
      <header className="pt-composer-header">
        <span>{t(issue ? "Edit issue" : "Create issue")}</span>
      </header>
      <div className="pt-composer-body">
        <div className="pt-issue-fields">
          <div className="pt-issue-title-field">
            <input
              ref={titleRef}
              autoComplete="off"
              aria-label={t("Issue title")}
              placeholder={t("Issue title")}
              value={value.title}
              onChange={(event) => patch({ title: event.target.value })}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.metaKey && !event.ctrlKey && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  submit();
                }
              }}
            />
          </div>
          <div className="pt-issue-description">
            <MentionTextField
              ariaLabel={t("Issue description")}
              placeholder={t("Add description…")}
              value={value.description}
              users={data.users}
              onChange={(description) => patch({ description })}
              onSubmit={submit}
            />
          </div>
        </div>
        <div className="pt-composer-toolbar">
          <div className="pt-composer-pills">
            <PropertyMenu
              compact
              label={t("Team")}
              ariaLabel={t("Set team")}
              searchPlaceholder={t("Set team…")}
              value={team?.name ?? t("Team")}
              valueIsEntityName={Boolean(team)}
              selectedId={value.teamId}
              triggerClassName={pill(Boolean(team))}
              icon={team ? <TeamIcon team={team} size={14} /> : <FlowGlyph name="leadTeam" width={14} height={14} />}
              options={teamOptions(data)}
              onChange={(id) => patch({ teamId: id, labelIds: [] })}
            />
            <PropertyMenu
              compact
              label={t("Priority")}
              ariaLabel={t("Change priority")}
              searchPlaceholder={t("Change priority…")}
              value={value.priority ? t(PRIORITIES[value.priority]) : t("Priority")}
              selectedId={String(value.priority)}
              triggerClassName={pill(value.priority > 0)}
              icon={value.priority ? <PriorityIcon priority={value.priority} size={14} /> : <NoPriorityGlyph size={14} />}
              options={priorityOptions(t)}
              onChange={(id) => patch({ priority: Number(id) })}
            />
            <PropertyMenu
              compact
              label={t("Assignee")}
              ariaLabel={t("Change assignee")}
              searchPlaceholder={t("Assign to…")}
              value={assignee?.displayName ?? t("Assignee")}
              valueIsEntityName={Boolean(assignee)}
              selectedId={value.assigneeId}
              triggerClassName={pill(Boolean(assignee))}
              icon={assignee ? <UserAvatar className="avatar" name={assignee.displayName} avatarUrl={assignee.avatarUrl} /> : <TemplateGlyph name="lead" size={14} />}
              options={userOptions(data, t("No assignee"))}
              onChange={(id) => patch({ assigneeId: id })}
            />
            {milestones.length > 0 && (
              <PropertyMenu
                compact
                label={t("Milestone")}
                ariaLabel={t("Set milestone")}
                searchPlaceholder={t("Set milestone…")}
                value={milestone?.name ?? t("Milestone")}
                valueIsEntityName={Boolean(milestone)}
                selectedId={value.milestoneId}
                triggerClassName={pill(Boolean(milestone))}
                icon={<span className="pt-pill-milestone"><MilestoneDiamond size={14} faded={!milestone} /></span>}
                options={[
                  { id: "", label: t("No milestone"), icon: <MilestoneDiamond /> },
                  ...milestones.map((item) => ({ id: item.id, label: item.name, icon: <MilestoneDiamond faded={false} />, i18nIgnore: true })),
                ]}
                onChange={(id) => patch({ milestoneId: id })}
              />
            )}
            <PropertyMenu
              compact
              multiple
              kind="labels"
              label={t("Labels")}
              ariaLabel={t("Change labels")}
              searchPlaceholder={t("Change labels…")}
              value={labels.length === 1 ? labels[0].label : labels.length ? countLabel(t, labels.length, "1 label", "{count} labels") : t("Labels")}
              valueIsEntityName={labels.length === 1}
              selectedIds={value.labelIds}
              triggerClassName={pill(labels.length > 0)}
              icon={labels.length === 1 ? <span className="pt-label-dot" style={{ background: labels[0].color }} /> : <FlowGlyph name="labels" width={14} height={14} />}
              options={issueLabels}
              onChange={(id) => patch({ labelIds: toggleGroupedLabelIds(value.labelIds, id, issueLabels) })}
            />
          </div>
          <div className="pt-composer-end">
            <button
              type="button"
              className="pt-attach"
              aria-label={t("Attach images, files, or videos")}
              aria-disabled="true"
              title={t("Attachments can be added once the issue is created")}
            >
              <TemplateGlyph name="attach" size={14} />
            </button>
            <div className="pt-composer-submit">
              <label className="pt-create-more" aria-label={t("Create more issues")}>
                <input type="checkbox" role="switch" checked={createMore} disabled={Boolean(issue)} onChange={(event) => setCreateMore(event.target.checked)} />
                <span>{t("Create more")}</span>
              </label>
              <button type="button" className="pt-small-button" aria-label={t("Discard changes")} onClick={() => void cancel()}>
                {t("Cancel")}
              </button>
              <button type="button" className="pt-small-button pt-small-button--primary" aria-disabled={!value.title.trim() || undefined} onClick={submit}>
                {t(issue ? "Save issue" : "Add issue")}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ----------------------------------------------------------------------------------------------
 * Helpers
 * -------------------------------------------------------------------------------------------- */

let sequence = 0;
function newId(prefix: string) {
  sequence += 1;
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? `${prefix}_${crypto.randomUUID()}`
    : `${prefix}_${Date.now()}_${sequence}`;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}
