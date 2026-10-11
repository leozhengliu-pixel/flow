/**
 * Issue templates, laid out: the workspace list (Settings › Issues › Templates), the
 * "Choose issue template type" dialog and the full-page template editor shared by workspace and
 * team templates (standard and form templates, default properties, sub-issues).
 */
import * as Dialog from "@radix-ui/react-dialog";
import * as Popover from "@radix-ui/react-popover";
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";

import { toast } from "@/lib/toast";
import {
  createIssueTemplate,
  createWorkspaceIssueTemplate,
  deleteIssueTemplate,
  deleteWorkspaceIssueTemplate,
  updateIssueTemplate,
  updateWorkspaceIssueTemplate,
} from "@/lib/api";
import { markdownPlainText } from "@/lib/markdown-plain-text";
import { labelTeamScopeIds, labelsForResource, toggleGroupedLabelIds } from "@/lib/labels";
import { issueTemplateCreateUrl, relativeTemplateTime, workspaceIssueTemplates } from "@/lib/issue-templates";
import { useI18n } from "@/i18n/i18n";
import { Avatar } from "@/components/issue/issue-row";
import { MentionTextField } from "@/components/editor/mention-text-field";
import {
  NoAssigneeIcon,
  NoProjectIcon,
  PriorityIcon,
  ProjectIcon,
  StatusIcon,
  TeamIcon,
} from "@/components/issue/issue-icons";
import { PropertyMenu, type PropertyOption } from "@/components/property/property-menu";
import { ViewGlyph, ViewIconPicker } from "@/components/views/view-icon-picker";
import { ProjectGlyph } from "@/components/views/project-glyph";
import { Toggle } from "@/components/ui/toggle";
import { confirmTemplateAction } from "./template-confirm";
import { TRIAGE_STATUS } from "@/components/triage/triage-model";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { ScopedFlowTooltip } from "@/components/ui/tooltip";
import type {
  BootstrapData,
  IssueTemplate,
  IssueTemplateMutationInput,
  TemplateFormField,
  TemplateFormFieldType,
  TemplateSubIssue,
} from "@/types/flow";

import { NoPriorityGlyph, PriorityBarsGlyph, TemplateGlyph, type TemplateGlyphName } from "./template-glyphs";
import "./issue-template-editor.css";

type TemplateType = "standard" | "customForm";

const DEFAULT_TEMPLATE_ICON = "Page";
const DEFAULT_TEMPLATE_COLOR = "#bec2c8";
const PRIORITY_NAMES = ["No priority", "Urgent", "High", "Medium", "Low"];

/* ------------------------------------------------------------------ list */

export function IssueTemplateSettings({
  data,
  mode,
  templateId,
  onCreate,
  onOpen,
  onDuplicate,
  onNavigateList,
  onReload,
}: {
  data: BootstrapData;
  mode?: "new" | "new-form" | "edit";
  templateId?: string;
  onCreate: (form: boolean) => void;
  onOpen: (template: IssueTemplate) => void;
  onDuplicate: (template: IssueTemplate) => void;
  onNavigateList: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const items = workspaceIssueTemplates(data.issueTemplates);
  if (mode === "new" || mode === "new-form")
    return (
      <IssueTemplateEditor
        data={data}
        source={duplicateSource(data)}
        templateType={mode === "new-form" ? "customForm" : "standard"}
        backLabel="Issue templates"
        onClose={onNavigateList}
        onReload={onReload}
      />
    );
  if (mode === "edit") {
    const template = data.issueTemplates.find((item) => item.id === templateId);
    return template ? (
      <IssueTemplateEditor
        data={data}
        template={template}
        templateType={template.templateType ?? "standard"}
        backLabel="Issue templates"
        onClose={onNavigateList}
        onDuplicate={() => onDuplicate(template)}
        onReload={onReload}
      />
    ) : (
      <div className="tpl-not-found">{t("Issue template not found")}</div>
    );
  }
  return (
    <div className="tpl-page" data-i18n-ignore>
      <TemplatePageHeader
        title="Issue templates"
        docsHref="https://flow.app/docs/issue-templates"
        description="These templates are available when creating issues for any team in the workspace. To create templates that only apply to specific teams, add them as team templates."
      />
      <IssueTemplateListCard data={data} items={items} onCreate={onCreate} onOpen={onOpen} onDuplicate={onDuplicate} onDeleted={onReload} />
    </div>
  );
}

function duplicateSource(data: BootstrapData) {
  const id = new URLSearchParams(location.search).get("duplicate");
  return id ? data.issueTemplates.find((item) => item.id === id) : undefined;
}

/** Page title, description and the trailing "Docs ↗" link of a template settings page. */
export function TemplatePageHeader({ title, description, docsHref, children }: { title: string; description?: string; docsHref?: string; children?: ReactNode }) {
  const { t } = useI18n();
  return (
    <header className="tpl-page-header">
      <h1>{t(title)}</h1>
      {(description || children) && (
        <p>
          {children ?? t(description ?? "")}{" "}
          {docsHref && (
            <a className="tpl-docs-link" href={docsHref} target="_blank" rel="noopener noreferrer">
              {t("Docs")}
            </a>
          )}
        </p>
      )}
    </header>
  );
}

/** A the reference app settings card that lists templates of one kind, or says there are none, with a "New template" button. */
export function TemplateListCard({
  emptyLabel,
  createLabel,
  onCreate,
  children,
  count,
  countLabel,
}: {
  emptyLabel: string;
  /** Accessible name of the "New template" button, e.g. "Create new issue template". */
  createLabel: string;
  onCreate: () => void;
  children?: ReactNode;
  count: number;
  countLabel?: string;
}) {
  const { t } = useI18n();
  return (
    <section className="tpl-card tpl-list-card">
      <div className="tpl-list-card-header">
        <span>{count ? countLabel : t(emptyLabel)}</span>
        <ScopedFlowTooltip label={t(createLabel)}>
          <button type="button" className="tpl-ghost-button" aria-label={t(createLabel)} onClick={onCreate}>
            <FlowGlyph name="plus" size={16} />
            {t("New template")}
          </button>
        </ScopedFlowTooltip>
      </div>
      {children}
    </section>
  );
}

/** One template row: icon, name and a meta line, opening the template on click, with a trailing actions menu. */
export function TemplateListRow({ icon, color, name, meta, onOpen, menu }: { icon: ReactNode; color?: string; name: string; meta?: ReactNode; onOpen: () => void; menu?: ReactNode }) {
  return (
    <div className="tpl-list-row">
      <button type="button" aria-label={name} className="tpl-list-row-link" onClick={onOpen} />
      <span className="tpl-list-row-icon tpl-icon-tint" style={color ? ({ "--view-color": color } as CSSProperties) : undefined}>{icon}</span>
      <span className="tpl-list-row-text">
        <strong data-i18n-ignore>{name}</strong>
        {meta && <small>{meta}</small>}
      </span>
      {menu}
    </div>
  );
}

/** The "…" menu of a template row or editor (the reference app's dropdown menu metrics). */
export function TemplateActionsMenu({ items, ariaLabel = "Open menu", className }: { items: Array<{ label: string; icon: ReactNode; onSelect: () => void; separatorBefore?: boolean }>; ariaLabel?: string; className?: string }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button type="button" aria-label={t(ariaLabel)} className={`tpl-icon-button${className ? ` ${className}` : ""}`}>
          <FlowGlyph name="ellipsis" size={16} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" align="end" sideOffset={4} className="tpl-menu" data-i18n-ignore>
          {items.map((item) => (
            <div key={item.label}>
              {item.separatorBefore && <span className="tpl-menu-separator" />}
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  setOpen(false);
                  item.onSelect();
                }}
              >
                {item.icon}
                {t(item.label)}
              </button>
            </div>
          ))}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function IssueTemplateListCard({
  data,
  items,
  teamId,
  onCreate,
  onOpen,
  onDuplicate,
  onDeleted,
}: {
  data: BootstrapData;
  items: IssueTemplate[];
  /** Set for a team's templates: rows of a parent team's template are read-only here. */
  teamId?: string;
  onCreate: (form: boolean) => void;
  onOpen: (template: IssueTemplate) => void;
  onDuplicate?: (template: IssueTemplate) => void;
  onDeleted: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [chooser, setChooser] = useState(false);
  const copyUrl = async (template: IssueTemplate) => {
    await navigator.clipboard.writeText(issueTemplateCreateUrl(data, template));
    toast.success(t("Link copied"));
  };
  const remove = async (template: IssueTemplate) => {
    if (!(await confirmIssueTemplateDelete(template, t))) return;
    try {
      if (teamId && template.scope !== "workspace") await deleteIssueTemplate(template.teamId ?? teamId, template.id);
      else await deleteWorkspaceIssueTemplate(template.id);
      await onDeleted();
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  const countLabel = t(items.length === 1 ? "1 issue template" : "issue template count").replace("{count}", String(items.length));
  return (
    <>
      <TemplateListCard emptyLabel="No issue templates" createLabel="Create new issue template" onCreate={() => setChooser(true)} count={items.length} countLabel={countLabel}>
        {items.map((item) => {
          const inheritedFrom = teamId && item.teamId && item.teamId !== teamId && item.scope !== "workspace" ? data.teams.find((team) => team.id === item.teamId) : undefined;
          return (
            <TemplateListRow
              key={item.id}
              icon={<ViewGlyph color={item.color || DEFAULT_TEMPLATE_COLOR} icon={item.icon || DEFAULT_TEMPLATE_ICON} />}
              color={item.color || DEFAULT_TEMPLATE_COLOR}
              name={item.name}
              onOpen={() => onOpen(item)}
              meta={
                inheritedFrom ? (
                  <>
                    {t("Inherited from")} <b data-i18n-ignore>{inheritedFrom.name}</b>
                  </>
                ) : item.description ? (
                  <span data-i18n-ignore>{item.description}</span>
                ) : item.creator ? (
                  <>
                    {t("Created by")} <b data-i18n-ignore>{item.creator.displayName}</b> {relativeTemplateTime(item.createdAt, t)}
                  </>
                ) : undefined
              }
              menu={
                inheritedFrom ? undefined : (
                  <TemplateActionsMenu
                    className="tpl-list-row-menu"
                    items={[
                      { label: "Edit", icon: <FlowGlyph name="edit" size={16} />, onSelect: () => onOpen(item) },
                      ...(onDuplicate ? [{ label: "Duplicate…", icon: <FlowGlyph name="copy" size={16} />, onSelect: () => onDuplicate(item) }] : []),
                      { label: "Copy URL to create issue from template", icon: <FlowGlyph name="copyStack" size={16} />, onSelect: () => void copyUrl(item) },
                      { label: "Delete", icon: <FlowGlyph name="delete" size={16} />, onSelect: () => void remove(item), separatorBefore: true },
                    ]}
                  />
                )
              }
            />
          );
        })}
      </TemplateListCard>
      <IssueTemplateTypeDialog
        open={chooser}
        onOpenChange={setChooser}
        onChoose={(form) => {
          setChooser(false);
          onCreate(form);
        }}
      />
    </>
  );
}

/** A team's issue templates (team Settings › Templates): the list card plus the type chooser. */
export function TeamIssueTemplateList(props: {
  data: BootstrapData;
  teamId: string;
  items: IssueTemplate[];
  onCreate: (form: boolean) => void;
  onOpen: (template: IssueTemplate) => void;
  onDeleted: () => Promise<void>;
}) {
  return <IssueTemplateListCard {...props} />;
}


function confirmIssueTemplateDelete(template: IssueTemplate, t: (value: string) => string) {
  return confirmTemplateAction({
    title: t("Delete the template \"{name}\"?").replace("{name}", template.name),
    description: t("You cannot undo this action."),
    confirmLabel: t("Delete"),
    cancelLabel: t("Cancel"),
  });
}

/* ---------------------------------------------------------- type chooser */

export function IssueTemplateTypeDialog({ open, onOpenChange, onChoose }: { open: boolean; onOpenChange: (open: boolean) => void; onChoose: (form: boolean) => void }) {
  const { t } = useI18n();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay data-flow-motion="backdrop" className="tpl-dialog-overlay" />
        <Dialog.Content data-flow-motion="dialog" className="tpl-type-dialog" aria-describedby={undefined} data-i18n-ignore>
          <header>
            <Dialog.Title>{t("Choose issue template type")}</Dialog.Title>
            <Dialog.Close asChild>
              <button type="button" aria-label={t("Close modal dialog")} className="tpl-icon-button tpl-dialog-close">
                <FlowGlyph name="closeChat" size={14} />
              </button>
            </Dialog.Close>
          </header>
          <div className="tpl-type-options">
            <button type="button" onClick={() => onChoose(false)}>
              <span className="tpl-type-title">{t("Standard")}</span>
              <span className="tpl-type-description">{t("Basic template with title, description, and default attributes")}</span>
              <span className="tpl-type-preview">
                <span className="tpl-preview-title">{t("Issue title")}</span>
                <span className="tpl-preview-description">{t("Add description…")}</span>
                <span className="tpl-preview-pills">
                  <span className="tpl-preview-pill is-set">
                    <TemplateGlyph name="backlog" size={14} />
                    <span>{t("Backlog")}</span>
                  </span>
                  <span className="tpl-preview-pill">
                    <NoPriorityGlyph />
                    <span>{t("Priority")}</span>
                  </span>
                  <span className="tpl-preview-pill is-icon">
                    <FlowGlyph name="ellipsis" size={14} />
                  </span>
                </span>
              </span>
            </button>
            <button type="button" onClick={() => onChoose(true)}>
              <span className="tpl-type-title">{t("Custom Form")}</span>
              <span className="tpl-type-description">{t("Define structured fields and inputs. Ideal for Asks")}</span>
              <span className="tpl-type-preview is-form">
                <span className="tpl-preview-label">{t("Repro steps")}</span>
                <span className="tpl-preview-field">{t("Enter text")}</span>
                <span className="tpl-preview-label">{t("Platform")}</span>
                <span className="tpl-preview-field">
                  {t("Select an option")}
                  <ChevronDownGlyph />
                </span>
              </span>
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function ChevronDownGlyph() {
  return <TemplateGlyph name="chevronDown" />;
}

/* ---------------------------------------------------------------- editor */

type EditorState = {
  name: string;
  description: string;
  title: string;
  body: string;
  icon: string;
  color: string;
  teamId: string;
  stateId: string;
  visibilityTeamId: string;
  priority: number;
  assigneeId: string;
  projectId: string;
  labelIds: string[];
  formFields: TemplateFormField[];
  subIssues: TemplateSubIssue[];
  type: TemplateType;
};

function initialEditorState(base: IssueTemplate | undefined, copy: boolean, templateType: TemplateType, scopeTeamId: string | undefined, data: BootstrapData): EditorState {
  const teamId = base?.teamId ?? scopeTeamId ?? "";
  return {
    name: copy && base ? `Copy of ${base.name}` : (base?.name ?? ""),
    description: copy ? "" : (base?.description ?? ""),
    title: base?.title ?? "",
    body: base?.body ?? "",
    icon: base?.icon ?? DEFAULT_TEMPLATE_ICON,
    color: base?.color ?? DEFAULT_TEMPLATE_COLOR,
    teamId,
    stateId: base ? (base.stateId ?? "") : teamId ? defaultTeamStateId(data, teamId) : "",
    visibilityTeamId: base?.visibilityTeamId ?? "",
    priority: base?.priority ?? 0,
    assigneeId: base?.assigneeId ?? "",
    projectId: base?.projectId ?? "",
    labelIds: base?.labelIds ?? [],
    formFields: base?.formFields ?? [],
    subIssues: base?.subIssues ?? [],
    type: templateType,
  };
}

function teamStates(data: BootstrapData, teamId: string) {
  const specific = data.states.some((state) => state.teamId === teamId);
  return data.states.filter((state) => (specific ? state.teamId === teamId : !state.teamId)).sort((a, b) => (a.position ?? 0) - (b.position ?? 0));
}

/** The status a new issue in the team starts in (the team's configured default, else its default/first unstarted state). */
function defaultTeamStateId(data: BootstrapData, teamId: string) {
  const states = teamStates(data, teamId);
  const configured = data.teamSettings?.[teamId]?.defaultStateId;
  return (states.find((state) => state.id === configured) ?? states.find((state) => state.default) ?? states.find((state) => state.type === "unstarted") ?? states[0])?.id ?? "";
}

export function IssueTemplateEditor({
  data,
  template,
  source,
  templateType,
  scopeTeamId,
  backLabel,
  onClose,
  onDuplicate,
  onReload,
}: {
  data: BootstrapData;
  template?: IssueTemplate;
  /** A template to start from ("Duplicate…"). */
  source?: IssueTemplate;
  templateType: TemplateType;
  /** Set for a team template: it is created in, and saved through, this team. */
  scopeTeamId?: string;
  backLabel: string;
  onClose: () => void;
  onDuplicate?: () => void;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const base = template ?? source;
  const initial = useMemo(() => initialEditorState(base, Boolean(source && !template), templateType, scopeTeamId, data), [base, source, template, templateType, scopeTeamId, data]);
  const [state, setState] = useState<EditorState>(initial);
  const [saving, setSaving] = useState(false);
  const [composerOpen, setComposerOpen] = useState(false);
  const set = useCallback(<K extends keyof EditorState>(key: K, value: EditorState[K]) => setState((current) => ({ ...current, [key]: value })), []);
  const dirty = JSON.stringify(initial) !== JSON.stringify(state);
  const isTeamTemplate = Boolean(scopeTeamId) || Boolean(template && template.scope !== "workspace");
  const ownerTeamId = scopeTeamId ?? (isTeamTemplate ? template?.teamId : undefined);

  const close = async () => {
    if (dirty && !(await confirmTemplateAction({ title: t("Discard changes?"), description: t("Confirm that you want to discard your changes."), confirmLabel: t("Discard"), cancelLabel: t("Cancel") }))) return;
    onClose();
  };
  const nameRef = useRef<HTMLInputElement>(null);
  const save = async () => {
    if (saving) return;
    if (!state.name.trim()) {
      nameRef.current?.focus();
      return;
    }
    setSaving(true);
    try {
      const input: IssueTemplateMutationInput & { name: string } = {
        name: state.name.trim(),
        description: state.description,
        title: state.title,
        body: state.body,
        icon: state.icon,
        color: state.color,
        teamId: state.teamId,
        stateId: state.teamId ? state.stateId : "",
        priority: state.priority,
        assigneeId: state.assigneeId,
        projectId: state.projectId,
        labelIds: state.labelIds,
        templateType: state.type,
        formFields: state.formFields,
        subIssues: state.subIssues,
        ...(isTeamTemplate ? {} : { visibilityTeamId: state.visibilityTeamId }),
      };
      if (template) {
        if (isTeamTemplate && ownerTeamId) await updateIssueTemplate(ownerTeamId, template.id, input);
        else await updateWorkspaceIssueTemplate(template.id, input);
      } else if (ownerTeamId) await createIssueTemplate(ownerTeamId, input);
      else await createWorkspaceIssueTemplate(input);
      await onReload();
      onClose();
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!template || !(await confirmIssueTemplateDelete(template, t))) return;
    try {
      if (isTeamTemplate && ownerTeamId) await deleteIssueTemplate(ownerTeamId, template.id);
      else await deleteWorkspaceIssueTemplate(template.id);
      await onReload();
      onClose();
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  const copyUrl = async () => {
    if (!template) return;
    await navigator.clipboard.writeText(issueTemplateCreateUrl(data, template));
    toast.success(t("Link copied"));
  };
  // ⌘⇧O adds a sub-issue's template editor.
  useEffect(() => {
    if (state.type !== "standard") return;
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.shiftKey && event.key.toLowerCase() === "o") {
        event.preventDefault();
        setComposerOpen(true);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [state.type]);

  return (
    <div className="tpl-editor" data-i18n-ignore>
      <button type="button" className="tpl-back-link" onClick={() => void close()}>
        <FlowGlyph name="chevronLeft" size={14} />
        <span>{t(backLabel)}</span>
      </button>
      <div className="tpl-editor-column">
        <div className="tpl-heading">
          <ViewIconPicker
            ariaLabel={t("Template icon")}
            color={state.color}
            icon={state.icon}
            onChange={(visual) => setState((current) => ({ ...current, icon: visual.icon, color: visual.color }))}
            triggerClassName="tpl-icon-trigger tpl-icon-tint"
          />
          <input
            ref={nameRef}
            autoFocus
            className="tpl-name-input"
            aria-label={t("Template name")}
            placeholder={t("Template name")}
            value={state.name}
            onChange={(event) => set("name", event.target.value)}
          />
          {template && (
            <TemplateActionsMenu
              className="tpl-editor-menu"
              items={[
                ...(onDuplicate ? [{ label: "Duplicate…", icon: <FlowGlyph name="copy" size={16} />, onSelect: onDuplicate }] : []),
                ...(state.type === "standard" ? [{ label: "Convert to form template", icon: <TemplateGlyph name="text" />, onSelect: () => set("type", "customForm") }] : []),
                { label: "Copy URL to create issue from template", icon: <FlowGlyph name="copyStack" size={16} />, onSelect: () => void copyUrl(), separatorBefore: true },
                { label: "Delete", icon: <FlowGlyph name="delete" size={16} />, onSelect: () => void remove(), separatorBefore: true },
              ]}
            />
          )}
        </div>
        <input
          className="tpl-description-input"
          aria-label={t("Template description")}
          placeholder={t("Add an optional template description…")}
          value={state.description}
          onChange={(event) => set("description", event.target.value)}
        />
        {state.type === "standard" ? (
          <section className="tpl-card tpl-issue-card">
            <input
              className="tpl-issue-title"
              aria-label={t("Issue title")}
              placeholder={t("Issue title")}
              value={state.title}
              onChange={(event) => set("title", event.target.value)}
            />
            <MentionTextField
              className="tpl-issue-description"
              ariaLabel={t("Issue description")}
              placeholder={t("Add description…")}
              value={state.body}
              users={data.users}
              onChange={(markdown) => set("body", markdown)}
            />
          </section>
        ) : (
          <TemplateFormFields fields={state.formFields} onChange={(fields) => set("formFields", fields)} />
        )}
        <section className={`tpl-defaults${state.type === "customForm" ? " is-form" : ""}`}>
          <h3>{t("Default properties")}</h3>
          <p>{t(state.type === "standard" ? "Automatically applied upon issue creation, and editable when composing within Flow" : "Automatically applied upon issue creation")}</p>
          <div className="tpl-card tpl-defaults-card">
            <div className="tpl-pills">
              <TemplatePropertyPills
                data={data}
                teamId={state.teamId}
                stateId={state.stateId}
                priority={state.priority}
                assigneeId={state.assigneeId}
                projectId={state.projectId}
                labelIds={state.labelIds}
                onTeam={(teamId) =>
                  setState((current) => ({
                    ...current,
                    teamId,
                    stateId: teamId ? defaultTeamStateId(data, teamId) : "",
                    labelIds: teamId ? current.labelIds.filter((id) => templateLabelOptions(data, teamId).some((option) => option.id === id)) : current.labelIds,
                  }))
                }
                onState={(value) => set("stateId", value)}
                onPriority={(value) => set("priority", value)}
                onAssignee={(value) => set("assigneeId", value)}
                onProject={(value) => set("projectId", value)}
                onLabels={(value) => set("labelIds", value)}
              />
              {state.type === "standard" && <TemplateMoreMenu onAddSubIssue={() => setComposerOpen(true)} />}
            </div>
            {state.subIssues.length > 0 && (
              <div className="tpl-subissue-list">
                {state.subIssues.map((item) => (
                  <SubIssueTemplateRow
                    key={item.id}
                    item={item}
                    onChange={(next) => set("subIssues", state.subIssues.map((value) => (value.id === next.id ? next : value)))}
                    onRemove={() => set("subIssues", state.subIssues.filter((value) => value.id !== item.id))}
                  />
                ))}
              </div>
            )}
            {composerOpen && (
              <SubIssueTemplateComposer
                data={data}
                labelTeamId={state.teamId || undefined}
                onCancel={() => setComposerOpen(false)}
                onAdd={(item, more) => {
                  setState((current) => ({ ...current, subIssues: [...current.subIssues, item] }));
                  if (!more) setComposerOpen(false);
                }}
              />
            )}
          </div>
        </section>
        <footer className="tpl-editor-footer">
          <button type="button" className="tpl-secondary-button" onClick={() => void close()}>
            {t("Cancel")}
          </button>
          <button type="button" className="tpl-primary-button" aria-label={t(template ? "Save" : "Create")} disabled={saving} onClick={() => void save()}>
            {t(template ? "Save" : "Create")}
          </button>
        </footer>
      </div>
    </div>
  );
}

/** The defaults card's "…" menu (the reference app: a single "Add sub-issue ⌘ ⇧ O" row). */
function TemplateMoreMenu({ onAddSubIssue }: { onAddSubIssue: () => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button type="button" aria-label={t("Open menu")} className="tpl-pill tpl-pill-more">
          <FlowGlyph name="ellipsis" size={14} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" align="start" sideOffset={4} className="tpl-menu tpl-more-menu" data-i18n-ignore>
          <button
            type="button"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              onAddSubIssue();
            }}
          >
            <TemplateGlyph name="addSubIssue" />
            {t("Add sub-issue")}
            <span className="tpl-menu-shortcut" aria-label="Command Shift O">
              <kbd>⌘</kbd>
              <kbd>⇧</kbd>
              <kbd>O</kbd>
            </span>
          </button>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

/* ------------------------------------------------------- property pills */

function templateLabelOptions(data: BootstrapData, teamId?: string): PropertyOption[] {
  const scopes = teamId ? new Set(labelTeamScopeIds(teamId, data.teams, data.teamSettings)) : undefined;
  const groupNames = new Map(data.labelGroups.map((group) => [group.id, group.name]));
  const groupColors = new Map(data.labelGroups.map((group) => [group.id, group.color]));
  return labelsForResource(data.labels, "issue", data.labelGroups)
    .filter((label) => !label.scope || label.scope === "Workspace" || !scopes || scopes.has(label.scope))
    .map((label) => ({
      id: label.id,
      label: label.name,
      color: label.color,
      description: label.description,
      scope: label.scope,
      resourceType: label.resourceType,
      groupId: label.groupId,
      groupLabel: label.groupId ? groupNames.get(label.groupId) : undefined,
      groupColor: label.groupId ? groupColors.get(label.groupId) : undefined,
      i18nIgnore: true,
    }));
}

function pillClass(set: boolean) {
  return `tpl-pill${set ? " is-set" : ""}`;
}

/** Team, status, priority, assignee, project and labels pills (the reference app's compact composer pills). */
export function TemplatePropertyPills({
  data,
  teamId,
  stateId,
  priority,
  assigneeId,
  projectId,
  labelIds,
  showProject = true,
  showStatus = true,
  onTeam,
  onState,
  onPriority,
  onAssignee,
  onProject,
  onLabels,
}: {
  data: BootstrapData;
  teamId: string;
  stateId?: string;
  priority: number;
  assigneeId: string;
  projectId?: string;
  labelIds: string[];
  showProject?: boolean;
  showStatus?: boolean;
  onTeam: (id: string) => void;
  onState?: (id: string) => void;
  onPriority: (value: number) => void;
  onAssignee: (id: string) => void;
  onProject?: (id: string) => void;
  onLabels: (ids: string[]) => void;
}) {
  const { t } = useI18n();
  const team = data.teams.find((item) => item.id === teamId);
  const states = teamId ? teamStates(data, teamId) : [];
  const state = states.find((item) => item.id === stateId);
  // A triage team's backlog intake reads as "Triage", as in the composer and the reference app's template editor.
  const shownState = state && data.teamSettings?.[teamId]?.triageEnabled && state.type === "backlog" ? TRIAGE_STATUS : state;
  const assignee = data.users.find((item) => item.id === assigneeId);
  const project = data.projects.find((item) => item.id === projectId);
  const labelOptions = templateLabelOptions(data, teamId || undefined);
  const selectedLabels = labelOptions.filter((option) => labelIds.includes(option.id));
  const activeTeams = data.teams.filter((item) => !item.archivedAt && !item.retiredAt);
  return (
    <>
      <PropertyMenu
        compact
        label={t("Team")}
        ariaLabel={t("Set team")}
        searchPlaceholder={t("Set team…")}
        value={team?.name ?? t("Team")}
        valueIsEntityName={Boolean(team)}
        selectedId={teamId}
        icon={team ? <TeamIcon team={team} size={14} /> : <TemplateGlyph name="team" size={14} />}
        triggerClassName={pillClass(Boolean(team))}
        surfaceClassName="tpl-picker tpl-team-menu"
        hideSearch
        options={[
          { id: "", label: t("No default team"), groupLabel: t("Teams"), icon: <TemplateGlyph name="team" /> },
          ...activeTeams.map((item) => ({ id: item.id, label: item.name, end: item.key, groupLabel: t("Your teams"), icon: <span className="tpl-team-option-icon"><TeamIcon team={item} size={14} /></span>, i18nIgnore: true })),
        ]}
        onChange={onTeam}
      />
      {showStatus && team && onState && (
        <PropertyMenu
          surfaceClassName="tpl-picker"
          compact
          label={t("Status")}
          ariaLabel={t("Change status")}
          searchPlaceholder={t("Change status…")}
          value={shownState ? t(shownState.name) : t("Status")}
          valueIsEntityName={Boolean(shownState && shownState !== TRIAGE_STATUS)}
          selectedId={stateId ?? ""}
          icon={shownState ? <StatusIcon state={shownState} size={14} /> : <TemplateGlyph name="backlog" size={14} />}
          triggerClassName={pillClass(Boolean(state))}
          options={states.map((item, index) => ({ id: item.id, label: item.name, color: item.color, icon: <StatusIcon state={item} />, shortcut: index < 9 ? String(index + 1) : undefined, i18nIgnore: true }))}
          onChange={onState}
        />
      )}
      <PropertyMenu
        surfaceClassName="tpl-picker"
        compact
        label={t("Priority")}
        ariaLabel={priority ? t("Change priority") : t("Change priority. No priority is selected")}
        searchPlaceholder={t("Set priority to…")}
        value={priority ? t(PRIORITY_NAMES[priority]) : t("Priority")}
        selectedId={String(priority)}
        icon={priority ? <PriorityIcon priority={priority} size={14} /> : <NoPriorityGlyph />}
        triggerClassName={pillClass(priority > 0)}
        options={PRIORITY_NAMES.map((name, value) => ({ id: String(value), label: t(name), icon: value ? <PriorityIcon priority={value} size={16} /> : <NoPriorityGlyph size={16} />, shortcut: String(value) }))}
        onChange={(value) => onPriority(Number(value))}
      />
      <PropertyMenu
        surfaceClassName="tpl-picker"
        compact
        label={t("Assignee")}
        ariaLabel={assignee ? t("Change assignee") : t("Change assignee. Currently no one is assigned.")}
        searchPlaceholder={t("Assign to…")}
        value={assignee?.displayName ?? t("Assignee")}
        valueIsEntityName={Boolean(assignee)}
        selectedId={assigneeId}
        icon={assignee ? <Avatar name={assignee.displayName} /> : <TemplateGlyph name="noAssignee" size={14} />}
        triggerClassName={pillClass(Boolean(assignee))}
        options={[
          { id: "", label: t("No assignee"), icon: <NoAssigneeIcon /> },
          ...data.users.filter((user) => user.active !== false).map((user) => ({ id: user.id, label: user.displayName, keywords: user.email, icon: <Avatar name={user.displayName} />, i18nIgnore: true })),
        ]}
        onChange={onAssignee}
      />
      {showProject && onProject && (
        <PropertyMenu
          surfaceClassName="tpl-picker"
          compact
          kind="project"
          label={t("Project")}
          ariaLabel={t("Change project")}
          searchPlaceholder={t("Add to project…")}
          value={project?.name ?? t("Project")}
          valueIsEntityName={Boolean(project)}
          selectedId={projectId ?? ""}
          icon={project ? <ProjectGlyph project={project} /> : <ProjectIcon size={14} />}
          triggerClassName={pillClass(Boolean(project))}
          options={[
            { id: "", label: t("No project"), icon: <NoProjectIcon /> },
            ...data.projects.filter((item) => !item.archivedAt).map((item) => ({ id: item.id, label: item.name, color: item.color, icon: <ProjectGlyph project={item} />, i18nIgnore: true })),
          ]}
          onChange={onProject}
        />
      )}
      <PropertyMenu
        surfaceClassName="tpl-picker"
        compact
        multiple
        kind="labels"
        label={t("Labels")}
        ariaLabel={t("Change labels")}
        searchPlaceholder={t("Change labels…")}
        value={selectedLabels.length ? selectedLabels.map((option) => option.label).join(", ") : t("Labels")}
        valueIsEntityName={selectedLabels.length > 0}
        selectedIds={labelIds}
        icon={<TemplateGlyph name="labels" size={14} />}
        triggerClassName={pillClass(selectedLabels.length > 0)}
        options={labelOptions}
        onChange={(id) => onLabels(toggleGroupedLabelIds(labelIds, id, labelOptions))}
      />
    </>
  );
}

/* ------------------------------------------------------------ sub-issues */

export function SubIssueTemplateComposer({
  data,
  showTeam = true,
  labelTeamId,
  onCancel,
  onAdd,
}: {
  data: BootstrapData;
  showTeam?: boolean;
  /** The team the sub-issues are created in: only its usable labels are offered. */
  labelTeamId?: string;
  onCancel: () => void;
  /** `more` is true while "Create more" is on: the composer stays open for the next sub-issue. */
  onAdd: (item: TemplateSubIssue, more: boolean) => void;
}) {
  const { t } = useI18n();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [teamId, setTeamId] = useState("");
  const [priority, setPriority] = useState(0);
  const [assigneeId, setAssigneeId] = useState("");
  const [labelIds, setLabelIds] = useState<string[]>([]);
  const [more, setMore] = useState(false);
  const titleRef = useRef<HTMLInputElement>(null);
  const add = () => {
    if (!title.trim()) return;
    onAdd({ id: crypto.randomUUID(), title: title.trim(), description, teamId: teamId || undefined, priority, assigneeId: assigneeId || undefined, labelIds }, more);
    setTitle("");
    setDescription("");
    titleRef.current?.focus();
  };
  return (
    <section className="tpl-subissue-composer" aria-label={t("Create sub-issue")}>
      <span className="tpl-subissue-composer-title">{t("Create sub-issue")}</span>
      <input ref={titleRef} autoFocus className="tpl-subissue-title" aria-label={t("Issue title")} placeholder={t("Issue title")} value={title} onChange={(event) => setTitle(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); add(); } }} />
      <MentionTextField className="tpl-subissue-description" ariaLabel={t("Issue description")} placeholder={t("Add description…")} value={description} users={data.users} onChange={setDescription} onSubmit={add} />
      <div className="tpl-subissue-footer">
        <div className="tpl-pills">
          <TemplatePropertyPills
            data={data}
            teamId={showTeam ? teamId : (labelTeamId ?? "")}
            priority={priority}
            assigneeId={assigneeId}
            labelIds={labelIds}
            showProject={false}
            showStatus={false}
            onTeam={setTeamId}
            onPriority={setPriority}
            onAssignee={setAssigneeId}
            onLabels={setLabelIds}
          />
        </div>
        <label className="tpl-create-more">
          <Toggle checked={more} label={t("Create more")} onChange={setMore} />
          <span>{t("Create more")}</span>
        </label>
        <button type="button" className="tpl-small-button" aria-label={t("Discard changes")} onClick={onCancel}>
          {t("Cancel")}
        </button>
        <button type="button" className="tpl-small-button is-primary" disabled={!title.trim()} onClick={add}>
          {t("Add sub-issue")}
        </button>
      </div>
    </section>
  );
}

export function SubIssueTemplateRow({ item, onChange, onRemove }: { item: TemplateSubIssue; onChange: (value: TemplateSubIssue) => void; onRemove: () => void }) {
  const { t } = useI18n();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(item.title);
  const [description, setDescription] = useState(item.description ?? "");
  const commit = () => {
    if (!title.trim()) return;
    onChange({ ...item, title: title.trim(), description });
    setEditing(false);
  };
  if (editing)
    return (
      <div
        className="tpl-subissue-row is-editing"
        onKeyDown={(event) => {
          if (event.key !== "Escape" || event.defaultPrevented) return;
          setTitle(item.title);
          setDescription(item.description ?? "");
          setEditing(false);
        }}
      >
        <span className="tpl-subissue-row-text">
          <input autoFocus aria-label={t("Issue title")} value={title} onChange={(event) => setTitle(event.target.value)} />
          <MentionTextField className="tpl-subissue-description" ariaLabel={t("Issue description")} value={description} onChange={setDescription} onSubmit={commit} />
        </span>
        <button type="button" className="tpl-icon-button" aria-label={t("Save")} disabled={!title.trim()} onClick={commit}>
          <FlowGlyph name="check" size={14} />
        </button>
        <button type="button" className="tpl-icon-button" aria-label={t("Cancel")} onClick={() => setEditing(false)}>
          <FlowGlyph name="closeChat" size={14} />
        </button>
      </div>
    );
  return (
    <div className="tpl-subissue-row">
      <TemplateGlyph name="addSubIssue" className="tpl-subissue-row-icon" />
      <span className="tpl-subissue-row-text">
        <strong data-i18n-ignore>{item.title}</strong>
        {item.description && <small data-i18n-ignore>{markdownPlainText(item.description)}</small>}
      </span>
      <button type="button" className="tpl-icon-button" aria-label={t("Edit sub-issue")} onClick={() => setEditing(true)}>
        <FlowGlyph name="edit" size={14} />
      </button>
      <button type="button" className="tpl-icon-button" aria-label={t("Remove sub-issue")} onClick={onRemove}>
        <FlowGlyph name="closeChat" size={14} />
      </button>
    </div>
  );
}

/* ----------------------------------------------------------- form fields */

const FIELD_TYPES: Array<{ type: TemplateFormFieldType; label: string; glyph: TemplateGlyphName | "priority"; property?: boolean }> = [
  { type: "text", label: "Text", glyph: "text" },
  { type: "longText", label: "Long text", glyph: "longText" },
  { type: "dropdown", label: "Dropdown", glyph: "dropdown" },
  { type: "checkboxes", label: "Checkboxes", glyph: "checkboxes" },
  { type: "date", label: "Date", glyph: "date" },
  { type: "upload", label: "Upload file", glyph: "upload" },
  { type: "instructions", label: "Instructions", glyph: "instructions" },
  { type: "title", label: "Title", glyph: "title", property: true },
  { type: "labelGroup", label: "Label group", glyph: "labelGroup", property: true },
  { type: "priority", label: "Priority", glyph: "priority", property: true },
  { type: "dueDate", label: "Due date", glyph: "date", property: true },
];

/** Field types whose label is the issue property's name (not typed by the template author). */
const FIXED_LABEL_TYPES: TemplateFormFieldType[] = ["instructions", "title", "labelGroup", "priority", "dueDate"];

function FieldGlyph({ type }: { type: TemplateFormFieldType }) {
  const definition = FIELD_TYPES.find((item) => item.type === type) ?? FIELD_TYPES[0];
  if (definition.glyph === "priority") return <PriorityBarsGlyph />;
  return <TemplateGlyph name={definition.glyph} size={definition.type === "title" ? 14 : 16} />;
}

function fieldTypeOptions(t: (value: string) => string): PropertyOption[] {
  return FIELD_TYPES.map((item) => ({ id: item.type, label: t(item.label), icon: <FieldGlyph type={item.type} />, groupLabel: item.property ? t("Issue properties") : undefined, end: item.type === "labelGroup" ? "▸" : undefined }));
}

function TemplateFormFields({ fields, onChange }: { fields: TemplateFormField[]; onChange: (fields: TemplateFormField[]) => void }) {
  const { t } = useI18n();
  const add = (type: TemplateFormFieldType) => {
    const definition = FIELD_TYPES.find((item) => item.type === type);
    onChange([...fields, { id: crypto.randomUUID(), label: FIXED_LABEL_TYPES.includes(type) ? t(definition?.label ?? "") : "", type, required: false, options: type === "dropdown" || type === "checkboxes" ? [""] : [] }]);
  };
  const addButton = (
    <PropertyMenu
      surfaceClassName="tpl-picker"
      label="Add field"
      ariaLabel={t("Add field")}
      searchPlaceholder={t("Add field…")}
      hideSearch
      closeOnSelect
      showGroupHeadings
      selectedId=""
      align="end"
      options={fieldTypeOptions(t)}
      trigger={
        <>
          <FlowGlyph name="plus" size={16} />
          {t("Add field")}
        </>
      }
      triggerRole="button"
      triggerClassName="tpl-secondary-button tpl-add-field"
      onChange={(type) => add(type as TemplateFormFieldType)}
    />
  );
  return (
    <section className="tpl-form-fields">
      <h3>{t("Form fields")}</h3>
      <p>{t("Choose which form fields to show when creating an issue with this template")}</p>
      {fields.length === 0 ? (
        <div className="tpl-card tpl-fields-empty">
          <span>{t("No fields added yet")}</span>
          {addButton}
        </div>
      ) : (
        <>
          <div className="tpl-field-list">
            {fields.map((field, index) => (
              <TemplateFormFieldEditor
                key={field.id}
                field={field}
                onChange={(next) => onChange(fields.map((value, position) => (position === index ? next : value)))}
                onRemove={() => onChange(fields.filter((_, position) => position !== index))}
                onMove={(offset) => {
                  const target = index + offset;
                  if (target < 0 || target >= fields.length) return;
                  const next = [...fields];
                  const [moved] = next.splice(index, 1);
                  next.splice(target, 0, moved);
                  onChange(next);
                }}
              />
            ))}
          </div>
          <div className="tpl-add-field-row">{addButton}</div>
        </>
      )}
    </section>
  );
}

function TemplateFormFieldEditor({ field, onChange, onRemove, onMove }: { field: TemplateFormField; onChange: (field: TemplateFormField) => void; onRemove: () => void; onMove: (offset: number) => void }) {
  const { t } = useI18n();
  const definition = FIELD_TYPES.find((item) => item.type === field.type) ?? FIELD_TYPES[0];
  const fixedLabel = FIXED_LABEL_TYPES.includes(field.type);
  const hasOptions = field.type === "dropdown" || field.type === "checkboxes";
  return (
    <div
      className="tpl-field"
      onKeyDown={(event) => {
        if (!event.altKey || (event.key !== "ArrowUp" && event.key !== "ArrowDown")) return;
        event.preventDefault();
        onMove(event.key === "ArrowUp" ? -1 : 1);
      }}
    >
      <div className="tpl-field-header">
        <PropertyMenu
          surfaceClassName="tpl-picker"
          label={t("Field type")}
          ariaLabel={t(definition.label)}
          hideSearch
          closeOnSelect
          showGroupHeadings
          selectedId={field.type}
          options={fieldTypeOptions(t)}
          trigger={
            <>
              <FieldGlyph type={field.type} />
              <span>{t(definition.label)}</span>
              <FlowGlyph name="selectChevron" width={10} height={5} />
            </>
          }
          triggerRole="button"
          triggerClassName="tpl-field-type"
          onChange={(type) => {
            const next = type as TemplateFormFieldType;
            const nextDefinition = FIELD_TYPES.find((item) => item.type === next);
            onChange({ ...field, type: next, label: FIXED_LABEL_TYPES.includes(next) ? t(nextDefinition?.label ?? "") : FIXED_LABEL_TYPES.includes(field.type) ? "" : field.label, options: next === "dropdown" || next === "checkboxes" ? (field.options?.length ? field.options : [""]) : [] });
          }}
        />
        <span className="tpl-field-header-spacer" />
        {field.type !== "instructions" && (
          <label className="tpl-required">
            <input type="checkbox" checked={field.required} onChange={(event) => onChange({ ...field, required: event.target.checked })} />
            <span>{t("Required")}</span>
          </label>
        )}
        <button type="button" className="tpl-icon-button tpl-field-remove" aria-label={t("Remove field")} onClick={onRemove}>
          <FlowGlyph name="closeChat" size={14} />
        </button>
      </div>
      {field.type === "instructions" ? (
        <div className="tpl-field-body">
          <textarea
            autoFocus
            className="tpl-field-instructions"
            aria-label={t("Instructions content")}
            placeholder={t("Add instructions…")}
            value={field.description ?? ""}
            onChange={(event) => onChange({ ...field, description: event.target.value })}
          />
        </div>
      ) : (
      <div className="tpl-field-body">
        {fixedLabel ? (
          <span className="tpl-field-label is-fixed">{field.label || t(definition.label)}</span>
        ) : (
          <textarea
            autoFocus
            rows={1}
            className="tpl-field-label"
            aria-label={t("Field label")}
            placeholder={t("Add label…")}
            value={field.label}
            onChange={(event) => onChange({ ...field, label: event.target.value })}
          />
        )}
        <input className="tpl-field-description" aria-label={t("Field description")} placeholder={t("Add description…")} value={field.description ?? ""} onChange={(event) => onChange({ ...field, description: event.target.value })} />
        {(field.type === "text" || field.type === "title") && <div className="tpl-field-preview">{t("Enter text")}</div>}
        {field.type === "longText" && <div className="tpl-field-preview is-long">{t("Enter text")}</div>}
        {(field.type === "date" || field.type === "dueDate") && <div className="tpl-field-preview">{t("Select a date")}</div>}
        {field.type === "priority" && (
          <div className="tpl-field-preview">
            {t("Select an option")}
            <ChevronDownGlyph />
          </div>
        )}
        {field.type === "labelGroup" && (
          <div className="tpl-field-preview">
            {t("Select an option")}
            <ChevronDownGlyph />
          </div>
        )}
        {field.type === "upload" && (
          <div className="tpl-field-preview is-upload">
            <TemplateGlyph name="upload" />
            {t("Upload file")}
          </div>
        )}
        {hasOptions && <TemplateFieldOptions field={field} onChange={onChange} />}
      </div>
      )}
    </div>
  );
}

function TemplateFieldOptions({ field, onChange }: { field: TemplateFormField; onChange: (field: TemplateFormField) => void }) {
  const { t } = useI18n();
  const options = field.options ?? [];
  return (
    <div className="tpl-field-options">
      {options.map((option, index) => (
        <div className="tpl-field-option" key={index}>
          {field.type === "checkboxes" ? <span className="tpl-option-box" aria-hidden="true" /> : <span className="tpl-option-index">{index + 1}.</span>}
          <input aria-label={`${t("Option")} ${index + 1}`} placeholder={t("Option")} value={option} onChange={(event) => onChange({ ...field, options: options.map((value, position) => (position === index ? event.target.value : value)) })} />
          <button type="button" className="tpl-icon-button" aria-label={t("Remove option")} onClick={() => onChange({ ...field, options: options.filter((_, position) => position !== index) })}>
            <FlowGlyph name="closeChat" size={14} />
          </button>
        </div>
      ))}
      <button type="button" className="tpl-small-button" onClick={() => onChange({ ...field, options: [...options, ""] })}>
        <FlowGlyph name="plus" size={14} />
        {t("Add option")}
      </button>
    </div>
  );
}

/* --------------------------------------------------------------- helpers */


function errorMessage(error: unknown) {
  return error instanceof Error && error.message ? error.message : "Could not save template";
}
