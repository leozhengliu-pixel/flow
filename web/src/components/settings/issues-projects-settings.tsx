import * as Dialog from "@radix-ui/react-dialog";
import * as Dropdown from "@radix-ui/react-dropdown-menu";
import * as Popover from "@radix-ui/react-popover";
import {
  Check,
  ChevronDown,
  Circle,
  ExternalLink,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import {
  useRef,
  useState,
  type CSSProperties,
  type Dispatch,
  type ReactNode,
  type RefObject,
  type SetStateAction,
} from "react";
import { toast } from "@/lib/toast";

import {
  createIssueTemplate,
  createProjectTemplate,
  createSLARule,
  createWorkspaceIssueTemplate,
  deleteIssueTemplate,
  deleteProjectTemplate,
  deleteSLARule,
  deleteWorkspaceIssueTemplate,
  updateIssueTemplate,
  updateProjectTemplate,
  updateProjectUpdateSettings,
  updateSLASettings,
  updateSLARule,
  updateWorkspaceIssueTemplate,
} from "@/lib/api";
import { markdownPlainText } from "@/lib/markdown-plain-text";
import { labelsForResource, toggleGroupedLabelIds } from "@/lib/labels";
import { useI18n } from "@/i18n/i18n";
import { ColorSwatchPicker } from "@/components/ui/color-swatch-picker";
import { normalizeHexColor, projectStatusStrokeColor } from "@/lib/project-status-color";
import { useResolvedTheme } from "@/hooks/use-resolved-theme";
import { MentionTextField } from "@/components/editor/mention-text-field";
import type {
  BootstrapData,
  IssueTemplate,
  ProjectStatus,
  ProjectTemplate,
  TemplateFormField,
  TemplateMilestone,
} from "@/types/flow";

import { SlackUpdates } from "./slack-updates";
import "./issues-projects-settings.css";
import "./issue-template-settings.css";
import { ProjectTemplateSettings } from "./project-template-settings";
import { IssueTemplateSettings } from "./issue-template-settings";
import { keepMentionMenuOpen } from "./mention-field-host";
import { SettingsSelect, SettingsToggle } from "./settings-primitives";

type TemplateKind = "issue" | "project";
type TemplateValue = IssueTemplate | ProjectTemplate;


type TemplateSettingsProps = {
  data: BootstrapData;
  mode?: "new" | "new-form" | "edit";
  templateId?: string;
  onNavigateList: () => void;
  onReload: () => Promise<void>;
} & (
  | {
      type: "issue";
      onCreateIssue: (form: boolean) => void;
      onOpenIssue: (template: IssueTemplate) => void;
      onDuplicateIssue: (template: IssueTemplate) => void;
    }
  | {
      type: "project";
      onCreateProject: () => void;
      onOpenProject: (template: ProjectTemplate) => void;
      onDuplicateProject: (template: ProjectTemplate) => void;
    }
);
export function TemplateSettings(props: TemplateSettingsProps) {
  if (props.type === "issue")
    return (
      <IssueTemplateSettings
        data={props.data}
        mode={props.mode}
        templateId={props.templateId}
        onCreate={props.onCreateIssue}
        onOpen={props.onOpenIssue}
        onDuplicate={props.onDuplicateIssue}
        onNavigateList={props.onNavigateList}
        onReload={props.onReload}
      />
    );
  return (
    <ProjectTemplateSettings
      data={props.data}
      mode={props.mode === "new-form" ? "new" : props.mode}
      templateId={props.templateId}
      onCreate={props.onCreateProject}
      onOpen={props.onOpenProject}
      onDuplicate={props.onDuplicateProject}
      onNavigateList={props.onNavigateList}
      onReload={props.onReload}
    />
  );
}

export function TemplateEditor({
  data,
  type,
  template,
  teamId: lockedTeamId,
  initialTemplateType,
  onClose,
  onSaved,
}: {
  data: BootstrapData;
  type: TemplateKind;
  template: TemplateValue | null;
  teamId?: string;
  initialTemplateType?: "standard" | "customForm";
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const { t } = useI18n();
  const issue = type === "issue" ? (template as IssueTemplate | null) : null;
  const project =
    type === "project" ? (template as ProjectTemplate | null) : null;
  const [name, setName] = useState(template?.name ?? "");
  const [templateDescription, setTemplateDescription] = useState(
    issue?.description ?? project?.templateDescription ?? "",
  );
  const [description, setDescription] = useState(project?.description ?? "");
  const [title, setTitle] = useState(issue?.title ?? "");
  const [projectName, setProjectName] = useState(project?.projectName ?? "");
  const [body, setBody] = useState(issue?.body ?? "");
  const [summary, setSummary] = useState(project?.summary ?? "");
  const [teamId, setTeamId] = useState(lockedTeamId ?? issue?.teamId ?? "");
  const [statusId, setStatusId] = useState(
    issue?.stateId ?? project?.statusId ?? "",
  );
  const [priority, setPriority] = useState(template?.priority ?? 0);
  const [ownerId, setOwnerId] = useState(
    issue?.assigneeId ?? project?.leadId ?? "",
  );
  const [projectId, setProjectId] = useState(issue?.projectId ?? "");
  const [labelIds, setLabelIds] = useState(template?.labelIds ?? []);
  const [teamIds, setTeamIds] = useState(
    project?.teamIds ?? (lockedTeamId ? [lockedTeamId] : []),
  );
  const [memberIds, setMemberIds] = useState(project?.memberIds ?? []);
  const [initiativeIds, setInitiativeIds] = useState(
    project?.initiativeIds ?? [],
  );
  const [dependencyIds, setDependencyIds] = useState(
    project?.dependencyIds ?? [],
  );
  const [issueIds, setIssueIds] = useState(project?.issueIds ?? []);
  const [milestones, setMilestones] = useState<TemplateMilestone[]>(
    project?.milestones ?? [],
  );
  const [visibility, setVisibility] = useState<"workspace" | "teams">(
    project?.visibility ?? (lockedTeamId ? "teams" : "workspace"),
  );
  const [templateType] = useState<"standard" | "customForm">(
    issue?.templateType ?? initialTemplateType ?? "standard",
  );
  const [formFields, setFormFields] = useState<TemplateFormField[]>(
    issue?.formFields ?? [],
  );
  const [milestoneOpen, setMilestoneOpen] = useState(false);
  const milestoneTriggerRef = useRef<HTMLButtonElement>(null);
  const [saving, setSaving] = useState(false);
  const labelOptions = labelsForResource(
    data.labels,
    type,
    data.labelGroups,
  ).filter((label) => !label.scope || label.scope === "Workspace");
  const toggle = (setter: Dispatch<SetStateAction<string[]>>, id: string) =>
    setter((current) =>
      current.includes(id)
        ? current.filter((value) => value !== id)
        : [...current, id],
    );
  const toggleLabel = (id: string) =>
    setLabelIds((current) => toggleGroupedLabelIds(current, id, labelOptions));
  const save = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      if (type === "issue") {
        const input = {
          name: name.trim(),
          title,
          description: templateDescription,
          body,
          teamId: teamId || undefined,
          stateId: statusId || undefined,
          priority,
          assigneeId: ownerId || undefined,
          projectId: projectId || undefined,
          labelIds,
          templateType,
          formFields,
        };
        if (lockedTeamId) {
          if (template)
            await updateIssueTemplate(lockedTeamId, template.id, input);
          else await createIssueTemplate(lockedTeamId, input);
        } else if (template)
          await updateWorkspaceIssueTemplate(template.id, input);
        else await createWorkspaceIssueTemplate(input);
      } else {
        const input = {
          name: name.trim(),
          projectName,
          templateDescription,
          description,
          summary,
          statusId: statusId || undefined,
          priority,
          leadId: ownerId || undefined,
          teamIds,
          memberIds,
          labelIds,
          initiativeIds,
          dependencyIds,
          issueIds,
          milestones,
          visibility,
        };
        if (template) await updateProjectTemplate(template.id, input);
        else await createProjectTemplate(input);
      }
      await onSaved();
      onClose();
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!template) return;
    try {
      if (type === "issue" && lockedTeamId)
        await deleteIssueTemplate(lockedTeamId, template.id);
      else if (type === "issue")
        await deleteWorkspaceIssueTemplate(template.id);
      else await deleteProjectTemplate(template.id);
      await onSaved();
      onClose();
    } catch (error) {
      toast.error(errorMessage(error));
    }
  };
  return (
    <div className="ip-settings-page ip-template-editor" data-i18n-ignore>
      <header className="ip-editor-header">
        <button onClick={onClose}>{t("Cancel")}</button>
        <strong>
          {t(template ? `Edit ${type} template` : `New ${type} template`)}
        </strong>
        <button
          className="primary"
          disabled={saving}
          onClick={() => void save()}
        >
          {t(saving ? "Saving…" : template ? "Save" : "Create")}
        </button>
      </header>
      <div className="ip-editor-content">
        <EditorField label={t("Template name")}>
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </EditorField>
        <EditorField label={t("Template description")}>
          <input
            value={templateDescription}
            onChange={(event) => setTemplateDescription(event.target.value)}
          />
        </EditorField>
        {type === "issue" ? (
          <>
            <EditorField label={t("Issue title")}>
              <input
                value={title}
                onChange={(event) => setTitle(event.target.value)}
              />
            </EditorField>
            <EditorField label={t("Issue description")}>
              <MentionTextField
                className="ip-mention-field"
                ariaLabel={t("Issue description")}
                value={body}
                users={data.users}
                onChange={setBody}
                onSubmit={() => {
                  if (!saving) void save();
                }}
              />
            </EditorField>
            <div className="ip-properties-grid">
              <SelectField
                label={t("Team")}
                value={teamId}
                onChange={setTeamId}
                options={data.teams.map((item) => [item.id, item.name])}
                empty={t("Choose when creating")}
                disabled={Boolean(lockedTeamId)}
              />
              <SelectField
                label={t("Priority")}
                value={String(priority)}
                onChange={(value) => setPriority(Number(value))}
                options={priorityOptions(t)}
              />
              <SelectField
                label={t("Assignee")}
                value={ownerId}
                onChange={setOwnerId}
                options={data.users.map((item) => [item.id, item.displayName])}
                empty={t("Unassigned")}
              />
              <SelectField
                label={t("Project")}
                value={projectId}
                onChange={setProjectId}
                options={data.projects.map((item) => [item.id, item.name])}
                empty={t("No project")}
              />
            </div>
            <SelectField
              label={t("Status")}
              value={statusId}
              onChange={setStatusId}
              options={data.states
                .filter((item) => !teamId || item.teamId === teamId)
                .map((item) => [item.id, item.name])}
              empty={t("Default")}
            />
            <CheckGroup
              label={t("Labels")}
              values={labelIds}
              options={labelOptions.map((item) => [item.id, item.name])}
              onToggle={toggleLabel}
            />
            {templateType === "customForm" && (
              <FormFields fields={formFields} onChange={setFormFields} />
            )}
          </>
        ) : (
          <>
            <EditorField label={t("Project name")}>
              <input
                value={projectName}
                onChange={(event) => setProjectName(event.target.value)}
              />
            </EditorField>
            <EditorField label={t("Project summary")}>
              <input
                value={summary}
                onChange={(event) => setSummary(event.target.value)}
              />
            </EditorField>
            <div className="ip-properties-grid">
              <SelectField
                label={t("Status")}
                value={statusId}
                onChange={setStatusId}
                options={data.projectStatuses.map((item) => [
                  item.id,
                  item.name,
                ])}
                empty={t("Default")}
              />
              <SelectField
                label={t("Priority")}
                value={String(priority)}
                onChange={(value) => setPriority(Number(value))}
                options={priorityOptions(t)}
              />
              <SelectField
                label={t("Lead")}
                value={ownerId}
                onChange={setOwnerId}
                options={data.users.map((item) => [item.id, item.displayName])}
                empty={t("Unassigned")}
              />
              <SelectField
                label={t("Visibility")}
                value={visibility}
                onChange={(value) =>
                  setVisibility(value as "workspace" | "teams")
                }
                options={[
                  ["workspace", t("Workspace")],
                  ["teams", t("Teams")],
                ]}
              />
            </div>
            <CheckGroup
              label={t("Members")}
              values={memberIds}
              options={data.users.map((item) => [item.id, item.displayName])}
              onToggle={(id) => toggle(setMemberIds, id)}
            />
            <CheckGroup
              label={t("Teams")}
              values={teamIds}
              options={data.teams.map((item) => [item.id, item.name])}
              onToggle={(id) => toggle(setTeamIds, id)}
              disabled={Boolean(lockedTeamId)}
            />
            <CheckGroup
              label={t("Initiatives")}
              values={initiativeIds}
              options={data.initiatives.map((item) => [item.id, item.name])}
              onToggle={(id) => toggle(setInitiativeIds, id)}
            />
            <CheckGroup
              label={t("Labels")}
              values={labelIds}
              options={labelOptions.map((item) => [item.id, item.name])}
              onToggle={toggleLabel}
            />
            <CheckGroup
              label={t("Dependencies")}
              values={dependencyIds}
              options={data.projects
                .filter((item) => item.id !== template?.id)
                .map((item) => [item.id, item.name])}
              onToggle={(id) => toggle(setDependencyIds, id)}
            />
            <CheckGroup
              label={t("Issues")}
              values={issueIds}
              options={data.issues
                .slice(0, 30)
                .map((item) => [item.id, `${item.identifier} ${item.title}`])}
              onToggle={(id) => toggle(setIssueIds, id)}
            />
            <EditorField label={t("Project description")}>
              <MentionTextField
                className="ip-mention-field"
                ariaLabel={t("Project description")}
                value={description}
                users={data.users}
                onChange={setDescription}
                onSubmit={() => {
                  if (!saving) void save();
                }}
              />
            </EditorField>
            <div className="ip-milestones">
              <div>
                <strong>{t("Milestones")}</strong>
                <button
                  ref={milestoneTriggerRef}
                  onClick={() => setMilestoneOpen(true)}
                >
                  <Plus size={13} />
                  {t("Add")}
                </button>
              </div>
              {milestones.length ? (
                milestones.map((item) => (
                  <div className="ip-milestone-row" key={item.id}>
                    <span>
                      <strong data-i18n-ignore>{item.name}</strong>
                      <small data-i18n-ignore>{markdownPlainText(item.description)}</small>
                    </span>
                    <button
                      aria-label={t("Remove milestone")}
                      onClick={() =>
                        setMilestones((current) =>
                          current.filter((value) => value.id !== item.id),
                        )
                      }
                    >
                      <X size={13} />
                    </button>
                  </div>
                ))
              ) : (
                <small>{t("No milestones")}</small>
              )}
            </div>
          </>
        )}
        {template && (
          <button className="ip-delete-button" onClick={() => void remove()}>
            <Trash2 size={14} />
            {t("Delete template")}
          </button>
        )}
      </div>
      <MilestoneDialog
        open={milestoneOpen}
        onOpenChange={setMilestoneOpen}
        triggerRef={milestoneTriggerRef}
        onAdd={(milestone) =>
          setMilestones((current) => [...current, milestone])
        }
      />
    </div>
  );
}

function EditorField({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <label className="ip-editor-field">
      <span>{label}</span>
      {children}
    </label>
  );
}
function SelectField({
  label,
  value,
  options,
  empty,
  disabled,
  onChange,
}: {
  label: string;
  value: string;
  options: string[][];
  empty?: string;
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <EditorField label={label}>
      <SettingsSelect
        disabled={disabled}
        label={label}
        value={value}
        onChange={onChange}
        options={[
          ...(empty !== undefined ? [{ value: "", label: empty }] : []),
          ...options.map(([id, name]) => ({
            value: id,
            label: name,
            entityName: true,
          })),
        ]}
      />
    </EditorField>
  );
}
function CheckGroup({
  label,
  values,
  options,
  disabled,
  onToggle,
}: {
  label: string;
  values: string[];
  options: string[][];
  disabled?: boolean;
  onToggle: (id: string) => void;
}) {
  const { t } = useI18n();
  return (
    <fieldset className="ip-check-group" disabled={disabled}>
      <legend>{label}</legend>
      {options.length ? (
        options.map(([id, name]) => (
          <label key={id}>
            <input
              type="checkbox"
              checked={values.includes(id)}
              onChange={() => onToggle(id)}
            />
            <span data-i18n-ignore>{name}</span>
          </label>
        ))
      ) : (
        <small>{t("No options")}</small>
      )}
    </fieldset>
  );
}
function priorityOptions(t: (value: string) => string) {
  return ["No priority", "Urgent", "High", "Medium", "Low"].map(
    (item, index) => [String(index), t(item)],
  );
}

function FormFields({
  fields,
  onChange,
}: {
  fields: TemplateFormField[];
  onChange: (value: TemplateFormField[]) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="ip-form-fields">
      <div>
        <strong>{t("Form fields")}</strong>
        <button
          onClick={() =>
            onChange([
              ...fields,
              {
                id: crypto.randomUUID(),
                label: "",
                type: "text",
                required: false,
                options: [],
              },
            ])
          }
        >
          <Plus size={13} />
          {t("Add field")}
        </button>
      </div>
      {fields.map((field, index) => (
        <div className="ip-form-field" key={field.id}>
          <input
            aria-label={t("Field label")}
            placeholder={t("Field label")}
            value={field.label}
            onChange={(event) =>
              onChange(
                fields.map((item, itemIndex) =>
                  itemIndex === index
                    ? { ...item, label: event.target.value }
                    : item,
                ),
              )
            }
          />
          <SettingsSelect
            label={t("Field type")}
            value={field.type}
            onChange={(value) =>
              onChange(
                fields.map((item, itemIndex) =>
                  itemIndex === index
                    ? {
                        ...item,
                        type: value as TemplateFormField["type"],
                      }
                    : item,
                ),
              )
            }
            options={["text", "textarea", "select", "checkbox", "date"]}
          />
          <label>
            <input
              type="checkbox"
              checked={field.required}
              onChange={(event) =>
                onChange(
                  fields.map((item, itemIndex) =>
                    itemIndex === index
                      ? { ...item, required: event.target.checked }
                      : item,
                  ),
                )
              }
            />
            {t("Required")}
          </label>
          <button
            aria-label={t("Delete field")}
            onClick={() =>
              onChange(fields.filter((_, itemIndex) => itemIndex !== index))
            }
          >
            <Trash2 size={13} />
          </button>
        </div>
      ))}
    </div>
  );
}

function MilestoneDialog({
  open,
  onOpenChange,
  triggerRef,
  onAdd,
}: {
  open: boolean;
  onOpenChange: (value: boolean) => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
  onAdd: (value: TemplateMilestone) => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const add = () => {
    if (!name.trim()) return;
    onAdd({ id: crypto.randomUUID(), name: name.trim(), description });
    setName("");
    setDescription("");
    onOpenChange(false);
  };
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay data-flow-motion="backdrop" className="ip-dialog-overlay" />
        <Dialog.Content data-flow-motion="dialog"
          className="ip-dialog"
          data-i18n-ignore
          onPointerDownOutside={keepMentionMenuOpen}
          onInteractOutside={keepMentionMenuOpen}
          onCloseAutoFocus={(event) => {
            event.preventDefault();
            triggerRef.current?.focus();
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey))
              add();
          }}
        >
          <Dialog.Title>{t("Add milestone")}</Dialog.Title>
          <EditorField label={t("Milestone name")}>
            <input
              autoFocus
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </EditorField>
          <EditorField label={t("Milestone description template")}>
            <MentionTextField
              className="ip-mention-field"
              ariaLabel={t("Milestone description template")}
              value={description}
              onChange={setDescription}
            />
          </EditorField>
          <footer>
            <Dialog.Close asChild>
              <button>{t("Cancel")}</button>
            </Dialog.Close>
            <button className="primary" disabled={!name.trim()} onClick={add}>
              {t("Add milestone")}
            </button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function SLASettings({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const settings = (data.settings?.sla ?? {}) as Record<string, unknown>;
  const enabled = settings.enabled === true;
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [targetMinutes, setTargetMinutes] = useState(1440);
  const [priority, setPriority] = useState("");
  const [saving, setSaving] = useState(false);
  const run = async (action: () => Promise<unknown>) => {
    setSaving(true);
    try {
      await action();
      await onReload();
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="ip-settings-page" data-i18n-ignore>
      <header className="settings-page-header ip-page-header">
        <div>
          <h1>{t("SLAs")}</h1>
          <p>
            {t(
              "Service-level agreements (SLAs) automatically apply deadlines to issues when they match predefined parameters. While often used to define response times to customer issues, they can also be used to define internal standards for bug and time-sensitive issue resolution.",
            )}{" "}
            <a
              href="https://flow.app/docs/sla"
              target="_blank"
              rel="noreferrer"
            >
              {t("Docs")}
              <ExternalLink size={11} />
            </a>
          </p>
        </div>
      </header>
      <section className="ip-settings-section">
        <div className="ip-setting-row">
          <span>
            <strong>{t("Enable SLAs")}</strong>
            <small>
              {t("Workspace-wide access to issue SLA automations and notifications")}
            </small>
          </span>
          <SettingsToggle
            checked={enabled}
            disabled={saving}
            label={t("Enable SLAs")}
            onChange={(value) =>
              run(() => updateSLASettings({ enabled: value }))
            }
          />
        </div>
        {enabled && (
          <div className="ip-setting-row">
            <span>
              <strong>{t("Work week")}</strong>
              <small>{t("Used to determine business day SLAs")}</small>
            </span>
            <SettingsSelect
              label={t("Work week")}
              value={settings.workWeek === "sunThu" ? "sunThu" : "monFri"}
              options={[
                { value: "monFri", label: t("Mon-Fri") },
                { value: "sunThu", label: t("Sun-Thu") },
              ]}
              onChange={(value) =>
                run(() => updateSLASettings({ workWeek: value as "monFri" | "sunThu" }))
              }
            />
          </div>
        )}
      </section>
      <section className="ip-settings-section">
        <header>
          <h3>{t("Automation rules")}</h3>
          <button
            className="settings-action ip-add-rule"
            disabled={!enabled || creating}
            onClick={() => setCreating(true)}
          >
            {t("Add rule")}
          </button>
        </header>
        {creating && (
          <form
            className="ip-rule-editor"
            onSubmit={(event) => {
              event.preventDefault();
              if (!name.trim()) return;
              void run(() =>
                createSLARule({
                  name: name.trim(),
                  targetMinutes,
                  teamIds: data.teams.map((item) => item.id),
                  filters: priority ? { priority: Number(priority) } : {},
                  pauseStatuses: ["completed", "canceled"],
                  enabled: true,
                }),
              ).then(() => {
                setCreating(false);
                setName("");
              });
            }}
          >
            <EditorField label={t("Rule name")}>
              <input
                autoFocus
                value={name}
                onChange={(event) => setName(event.target.value)}
              />
            </EditorField>
            <div className="ip-properties-grid">
              <SelectField
                label={t("Target")}
                value={String(targetMinutes)}
                onChange={(value) => setTargetMinutes(Number(value))}
                options={[
                  ["60", t("1 hour")],
                  ["240", t("4 hours")],
                  ["480", t("8 hours")],
                  ["1440", t("24 hours")],
                  ["4320", t("3 days")],
                ]}
              />
              <SelectField
                label={t("Priority")}
                value={priority}
                onChange={setPriority}
                empty={t("Any priority")}
                options={priorityOptions(t).slice(1)}
              />
            </div>
            <footer>
              <button type="button" onClick={() => setCreating(false)}>
                {t("Cancel")}
              </button>
              <button className="primary" disabled={saving}>
                {t("Create")}
              </button>
            </footer>
          </form>
        )}
        {data.slaRules.length
          ? data.slaRules.map((rule) => (
              <div className="ip-setting-row" key={rule.id}>
                <span>
                  <strong data-i18n-ignore>{rule.name}</strong>
                  <small>
                    {formatDuration(rule.targetMinutes, t)} ·{" "}
                    {t(rule.enabled ? "Active" : "Disabled")}
                  </small>
                </span>
                <div className="ip-row-actions">
                  <SettingsToggle
                    checked={rule.enabled}
                    disabled={!enabled || saving}
                    label={rule.name}
                    onChange={(value) =>
                      run(() => updateSLARule(rule.id, { enabled: value }))
                    }
                  />
                  <button
                    aria-label={t("Delete rule")}
                    onClick={() => void run(() => deleteSLARule(rule.id))}
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              </div>
            ))
          : !creating && (
              <div className="ip-empty-row">
                <span className="ip-template-glyph">
                  <Circle size={14} />
                </span>
                <span>
                  <strong>{t("No automation rules")}</strong>
                  <small>
                    {enabled
                      ? t("Add a rule to begin applying SLAs.")
                      : t("Enable SLAs to add automation rules.")}
                  </small>
                </span>
              </div>
            )}
      </section>
    </div>
  );
}

export function ProjectUpdateSettings({
  data,
  onReload,
}: {
  data: BootstrapData;
  onReload: () => Promise<void>;
}) {
  const { t } = useI18n();
  const current = (data.settings?.projectUpdates ?? {}) as Record<
    string,
    unknown
  >;
  const initial = Number(current.cadenceDays ?? 0);
  const [editing, setEditing] = useState(false);
  const [cadence, setCadence] = useState(initial);
  const [saving, setSaving] = useState(false);
  const cadenceLabel =
    cadenceOptions(t).find((item) => item[0] === cadence)?.[1] ??
    t("No expectation");
  const save = async () => {
    setSaving(true);
    try {
      await updateProjectUpdateSettings({ cadenceDays: cadence });
      await onReload();
      setEditing(false);
      toast.success(t("Update schedule saved"));
    } catch (error) {
      toast.error(errorMessage(error));
    } finally {
      setSaving(false);
    }
  };
  return (
    <div className="ip-settings-page" data-i18n-ignore>
      <header className="settings-page-header ip-page-header">
        <div>
          <h1>{t("Project updates")}</h1>
          <p>
            {t(
              "Short status reports about the progress and health of your projects. Project members regularly post updates, and subscribers automatically receive them in their inbox.",
            )}{" "}
            <a
              href="https://flow.app/docs/project-updates"
              target="_blank"
              rel="noreferrer"
            >
              {t("Docs")}
              <ExternalLink size={11} />
            </a>
          </p>
        </div>
      </header>
      <section className="ip-settings-section">
        <header>
          <h3>{t("Update schedule")}</h3>
          <p>{t("Configure how often updates are expected on projects. Project leads will receive reminders to post updates.")}</p>
        </header>
        <div className="ip-setting-row">
          <span>
            <strong>{cadence ? cadenceLabel : t("No expectation for updates")}</strong>
          </span>
          {editing ? (
            <div className="ip-update-editor">
              <CadenceMenu value={cadence} onChange={setCadence} />
              <button
                onClick={() => {
                  setCadence(initial);
                  setEditing(false);
                }}
              >
                {t("Cancel")}
              </button>
              <button
                className="primary"
                disabled={saving}
                onClick={() => void save()}
              >
                {t("Save")}
              </button>
            </div>
          ) : (
            <button
              className="settings-action"
              onClick={() => setEditing(true)}
            >
              {t("Edit")}
            </button>
          )}
        </div>
      </section>
      <section className="ip-settings-section">
        <SlackUpdates
          data={data}
          kind="project"
          variant="row"
          disabled={saving}
          onReload={onReload}
        />
      </section>
    </div>
  );
}

function CadenceMenu({
  value,
  onChange,
}: {
  value: number;
  onChange: (value: number) => void;
}) {
  const { t } = useI18n();
  const options = cadenceOptions(t);
  const label = options.find((item) => item[0] === value)?.[1];
  return (
    <Dropdown.Root>
      <Dropdown.Trigger asChild>
        <button className="ip-combobox" role="combobox">
          {label}
          <ChevronDown size={13} />
        </button>
      </Dropdown.Trigger>
      <Dropdown.Portal>
        <Dropdown.Content data-flow-motion="floating"
          className="ip-cadence-menu"
          align="end"
          sideOffset={5}
          data-i18n-ignore
        >
          {options.map(([days, text]) => (
            <Dropdown.Item
              className="ip-cadence-item"
              key={days}
              onSelect={() => onChange(days)}
            >
              {text}
              {days === value && <Check size={13} />}
            </Dropdown.Item>
          ))}
        </Dropdown.Content>
      </Dropdown.Portal>
    </Dropdown.Root>
  );
}
function cadenceOptions(t: (value: string) => string): Array<[number, string]> {
  return [
    [0, t("No expectation")],
    [7, t("Every week")],
    ...[2, 3, 4, 5, 6, 7, 8].map(
      (value) => [value * 7, t(`Every ${value} weeks`)] as [number, string],
    ),
  ];
}

export function StatusDragHandle() {
  return (
    <span className="ip-status-drag-handle" aria-hidden="true">
      <svg width="6" height="10" viewBox="0 0 6 10">
        <path
          fillRule="evenodd"
          d="M1 8a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM1 4a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2ZM1 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Zm4 0a1 1 0 1 1 0 2 1 1 0 0 1 0-2Z"
        />
      </svg>
    </span>
  );
}
export function StatusMenuIcon({ name }: { name: "edit" | "delete" }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      {name === "edit" ? (
        <>
          <path d="M10.1805 3.34195 4.14166 9.416a4.02 4.02 0 0 1 2.59842 2.4024L12.6877 5.8425a7.98 7.98 0 0 1-2.5072-2.50055Z" />
          <path d="M13.7391 4.71631c.4184-.68683.3336-1.59893-.2545-2.19441-.5938-.60118-1.5062-.68298-2.1866-.24541a6.04 6.04 0 0 0 2.4412 2.43982Z" />
          <path d="M3.03104 10.7502a2.5 2.5 0 0 1 2.46679 2.2612c-.66515.4146-2.09586.7808-2.96669.9772-.33104.0746-.61088-.2284-.51039-.5513.23251-.7471.62517-1.9237 1.01029-2.6871Z" />
        </>
      ) : (
        <>
          <path
            fillRule="evenodd"
            d="m2 3 1.652 9.911A2.5 2.5 0 0 0 6.118 15h3.764a2.5 2.5 0 0 0 2.466-2.089L14 3H2Zm1.77 1.5 1.361 8.164a1 1 0 0 0 .987.836h3.764a1 1 0 0 0 .987-.836l1.36-8.164H3.771Z"
            clipRule="evenodd"
          />
          <path d="M5.5 2.5A1.5 1.5 0 0 1 7 1h2a1.5 1.5 0 0 1 1.5 1.5v1h-5v-1Z" />
          <path d="M1 3.75A.75.75 0 0 1 1.75 3h12.5a.75.75 0 0 1 0 1.5H1.75A.75.75 0 0 1 1 3.75Z" />
        </>
      )}
    </svg>
  );
}

function ProjectStatusMark({
  status,
  progress = 0.3,
}: {
  status: ProjectStatus;
  progress?: number;
}) {
  const outer =
      "M2.95778 3.02069 5.70777 1.36023c.79467-.47982 1.78979-.47982 2.58446 0l2.74997 1.6605A2.5 2.5 0 0 1 12.25 5.16086v3.68717a2.5 2.5 0 0 1-1.2112 2.14217l-2.74766 1.6531a2.5 2.5 0 0 1-2.58103-.0021l-2.75236-1.662A2.5 2.5 0 0 1 1.75 8.83911V5.16082a2.5 2.5 0 0 1 1.20778-2.14013Z",
    inner =
      "M8.3779 4.74233c-.23352-.13626-.52228-.13626-.7558 0L5.37209 6.05513A.75.75 0 0 0 5 6.70311v2.63905c0 .26681.14168.51357.37209.648l2.25001 1.31284c.23352.1362.52228.1362.7558 0l2.25001-1.31284A.75.75 0 0 0 11 9.34216V6.70311a.75.75 0 0 0-.37209-.64798L8.3779 4.74233Z",
    maskId = `project-status-${status.id.replace(/[^a-z0-9_-]/gi, "")}-${Math.round(progress * 100)}`;
  const done = status.type === "completed",
    canceled = status.type === "canceled",
    started = status.type === "started";
  return (
    <span
      className={`ip-status-mark is-${status.type}`}
      style={{ "--status-color": status.color } as CSSProperties}
    >
      <svg viewBox="-1 -1 16 16" aria-hidden="true">
        <path className="ip-status-hex" d={outer} />
        {status.type === "backlog" && (
          <path className="ip-status-hex-dash" d={outer} />
        )}{" "}
        {started && (
          <>
            <mask id={maskId}>
              <path transform="translate(-1 -1)" d={inner} fill="white" />
            </mask>
            <circle
              className="ip-status-progress"
              cx="7"
              cy="7"
              r="4"
              pathLength="100"
              mask={`url(#${maskId})`}
              style={{ "--status-progress": progress * 100 } as CSSProperties}
            />
          </>
        )}{" "}
        {(done || canceled) && <path className="ip-status-fill" d={outer} />}{" "}
        {done && (
          <path className="ip-status-symbol" d="m3.75 7.1 2 2 4.5-4.5" />
        )}
        {canceled && (
          <path
            className="ip-status-symbol"
            d="m4.25 4.25 5.5 5.5m0-5.5-5.5 5.5"
          />
        )}
      </svg>
    </span>
  );
}

export function StatusColorPicker({
  color,
  type,
  onChange,
  preview,
  adjustContrast = false,
}: {
  color: string;
  type: string;
  onChange: (color: string) => void;
  preview?: ReactNode;
  /** Workflow statuses: note when the reference app darkens the colour for contrast on light surfaces. */
  adjustContrast?: boolean;
}) {
  const { t } = useI18n(),
    theme = useResolvedTheme(),
    [open, setOpen] = useState(false);
  const progress = type === "started" ? 0.7 : undefined;
  const hex = normalizeHexColor(color);
  const adjusted =
    adjustContrast && hex !== null && projectStatusStrokeColor(hex, theme) !== hex;
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button
          className="ip-status-color"
          aria-label={t("Color")}
          type="button"
        >
          {preview ?? (
            <ProjectStatusMark
              progress={progress}
              status={{
                id: "status-color-preview",
                name: "",
                color,
                type: type as ProjectStatus["type"],
                position: 0,
              }}
            />
          )}
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating"
          align="center"
          className="csp-popover"
          collisionPadding={8}
          sideOffset={2}
          data-i18n-ignore
          onCloseAutoFocus={(event) => event.preventDefault()}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.stopPropagation();
              event.preventDefault();
              setOpen(false);
            }
          }}
        >
          <ColorSwatchPicker
            color={color}
            framed
            note={adjusted ? t("Contrast has been adjusted") : undefined}
            surface="elevated"
            onChange={(next, source) => {
              if (next !== color.toLowerCase()) onChange(next);
              if (source === "swatch") setOpen(false);
            }}
          />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

function formatDuration(minutes: number, t: (value: string) => string) {
  if (minutes % 1440 === 0) return t(`${minutes / 1440} days`);
  if (minutes % 60 === 0) return t(`${minutes / 60} hours`);
  return t(`${minutes} minutes`);
}
function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "Could not save setting";
}
