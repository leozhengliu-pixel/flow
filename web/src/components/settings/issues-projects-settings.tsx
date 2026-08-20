import * as Dialog from '@radix-ui/react-dialog'
import * as Dropdown from '@radix-ui/react-dropdown-menu'
import { Check, ChevronDown, Circle, ExternalLink, LayoutTemplate, MessageSquare, MoreHorizontal, Plus, Trash2, X } from 'lucide-react'
import { useMemo, useRef, useState, type CSSProperties, type Dispatch, type ReactNode, type RefObject, type SetStateAction } from 'react'
import { toast } from 'sonner'

import {
  connectIntegration, createIssueTemplate, createProjectStatus, createProjectTemplate, createSLARule,
  createWorkspaceIssueTemplate, deleteIssueTemplate, deleteProjectStatus, deleteProjectTemplate, deleteSLARule,
  deleteWorkspaceIssueTemplate, reorderProjectStatuses, updateIssueTemplate, updateProjectStatus, updateProjectTemplate,
  updateProjectUpdateSettings, updateSLASettings, updateSLARule, updateWorkspaceIssueTemplate,
} from '@/lib/api'
import { labelsForResource } from '@/lib/labels'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, IssueTemplate, ProjectStatus, ProjectTemplate, TemplateFormField, TemplateMilestone } from '@/types/flow'

import './issues-projects-settings.css'

type TemplateKind = 'issue' | 'project'
type TemplateValue = IssueTemplate | ProjectTemplate
type StatusType = 'backlog' | 'planned' | 'started' | 'completed' | 'canceled'

const STATUS_SECTIONS: Array<{ type: StatusType; label: string }> = [
  { type: 'backlog', label: 'Backlog' }, { type: 'planned', label: 'Planned' }, { type: 'started', label: 'In Progress' },
  { type: 'completed', label: 'Completed' }, { type: 'canceled', label: 'Canceled' },
]

export function TemplateSettings({ data, type, onReload }: { data: BootstrapData; type: TemplateKind; onReload: () => Promise<void> }) {
  const { t } = useI18n()
  const items: TemplateValue[] = type === 'issue'
    ? data.issueTemplates.filter(item => item.scope === 'workspace' || !item.teamId)
    : data.projectTemplates.filter(item => (item.visibility ?? 'workspace') === 'workspace')
  const [screen, setScreen] = useState<'list'|'choose'|'editor'>('list')
  const [editing, setEditing] = useState<TemplateValue|null>(null)
  const [templateType, setTemplateType] = useState<'standard'|'customForm'>('standard')
  const beginCreate = () => { setEditing(null); if (type === 'issue') setScreen('choose'); else setScreen('editor') }
  if (screen === 'choose') return <TemplateTypeChooser onBack={() => setScreen('list')} onChoose={value => { setTemplateType(value); setScreen('editor') }}/>
  if (screen === 'editor') return <TemplateEditor data={data} type={type} template={editing} initialTemplateType={templateType} onClose={() => setScreen('list')} onSaved={onReload}/>
  return <div className="ip-settings-page" data-i18n-ignore>
    <header className="settings-page-header ip-page-header"><div><h1>{t(type === 'issue' ? 'Issue templates' : 'Project templates')}</h1><p>{t(type === 'issue' ? 'Create reusable templates for issues in your workspace.' : 'Create reusable templates for projects in your workspace.')}{' '}<a href="https://linear.app/docs/templates" target="_blank" rel="noreferrer">{t('Docs')}<ExternalLink size={11}/></a></p></div><button className="settings-action primary" onClick={beginCreate}><Plus size={14}/>{t('New template')}</button></header>
    <section className="ip-template-list" aria-label={t(type === 'issue' ? 'Issue templates' : 'Project templates')}>
      {items.length ? items.map(item => <button className="ip-template-row" key={item.id} onClick={() => { setEditing(item); setTemplateType(type === 'issue' ? (item as IssueTemplate).templateType ?? 'standard' : 'standard'); setScreen('editor') }}><span className="ip-template-glyph"><LayoutTemplate size={15}/></span><span><strong data-i18n-ignore>{item.name}</strong><small>{item.description || t('No description')}</small></span><MoreHorizontal size={15}/></button>) : <div className="ip-empty-row"><span className="ip-template-glyph"><LayoutTemplate size={15}/></span><span><strong>{t(`No ${type} templates`)}</strong><small>{t('Templates you create will appear here.')}</small></span></div>}
    </section>
  </div>
}

function TemplateTypeChooser({ onBack, onChoose }: { onBack: () => void; onChoose: (value: 'standard'|'customForm') => void }) {
  const { t } = useI18n()
  return <div className="ip-settings-page ip-template-chooser" data-i18n-ignore><header className="ip-editor-header"><button onClick={onBack}>{t('Cancel')}</button><strong>{t('New issue template')}</strong><span/></header><div className="ip-chooser-content"><h2>{t('Choose a template type')}</h2><button onClick={() => onChoose('standard')}><LayoutTemplate size={20}/><span><strong>{t('Standard')}</strong><small>{t('Create an issue template with default issue properties.')}</small></span></button><button onClick={() => onChoose('customForm')}><LayoutTemplate size={20}/><span><strong>{t('Custom Form')}</strong><small>{t('Collect structured information when creating an issue.')}</small></span></button></div></div>
}

export function TemplateEditor({ data, type, template, teamId: lockedTeamId, initialTemplateType, onClose, onSaved }: { data: BootstrapData; type: TemplateKind; template: TemplateValue|null; teamId?: string; initialTemplateType?: 'standard'|'customForm'; onClose: () => void; onSaved: () => Promise<void> }) {
  const { t } = useI18n()
  const issue = type === 'issue' ? template as IssueTemplate|null : null
  const project = type === 'project' ? template as ProjectTemplate|null : null
  const [name, setName] = useState(template?.name ?? '')
  const [templateDescription, setTemplateDescription] = useState(issue?.description ?? project?.templateDescription ?? '')
  const [description, setDescription] = useState(project?.description ?? '')
  const [title, setTitle] = useState(issue?.title ?? '')
  const [projectName, setProjectName] = useState(project?.projectName ?? '')
  const [body, setBody] = useState(issue?.body ?? '')
  const [summary, setSummary] = useState(project?.summary ?? '')
  const [teamId, setTeamId] = useState(lockedTeamId ?? issue?.teamId ?? '')
  const [statusId, setStatusId] = useState(issue?.stateId ?? project?.statusId ?? '')
  const [priority, setPriority] = useState(template?.priority ?? 0)
  const [ownerId, setOwnerId] = useState(issue?.assigneeId ?? project?.leadId ?? '')
  const [projectId, setProjectId] = useState(issue?.projectId ?? '')
  const [labelIds, setLabelIds] = useState(template?.labelIds ?? [])
  const [teamIds, setTeamIds] = useState(project?.teamIds ?? (lockedTeamId ? [lockedTeamId] : []))
  const [memberIds, setMemberIds] = useState(project?.memberIds ?? [])
  const [initiativeIds, setInitiativeIds] = useState(project?.initiativeIds ?? [])
  const [dependencyIds, setDependencyIds] = useState(project?.dependencyIds ?? [])
  const [issueIds, setIssueIds] = useState(project?.issueIds ?? [])
  const [milestones, setMilestones] = useState<TemplateMilestone[]>(project?.milestones ?? [])
  const [visibility, setVisibility] = useState<'workspace'|'teams'>(project?.visibility ?? (lockedTeamId ? 'teams' : 'workspace'))
  const [templateType] = useState<'standard'|'customForm'>(issue?.templateType ?? initialTemplateType ?? 'standard')
  const [formFields, setFormFields] = useState<TemplateFormField[]>(issue?.formFields ?? [])
  const [milestoneOpen, setMilestoneOpen] = useState(false)
  const milestoneTriggerRef = useRef<HTMLButtonElement>(null)
  const [saving, setSaving] = useState(false)
  const labelOptions = labelsForResource(data.labels, type).filter(label => !label.scope || label.scope === 'Workspace')
  const toggle = (setter: Dispatch<SetStateAction<string[]>>, id: string) => setter(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])
  const save = async () => {
    if (!name.trim()) return
    setSaving(true)
    try {
      if (type === 'issue') {
        const input = { name: name.trim(), title, description: templateDescription, body, teamId: teamId || undefined, stateId: statusId || undefined, priority, assigneeId: ownerId || undefined, projectId: projectId || undefined, labelIds, templateType, formFields }
        if (lockedTeamId) { if (template) await updateIssueTemplate(lockedTeamId, template.id, input); else await createIssueTemplate(lockedTeamId, input) }
        else if (template) await updateWorkspaceIssueTemplate(template.id, input); else await createWorkspaceIssueTemplate(input)
      } else {
        const input = { name: name.trim(), projectName, templateDescription, description, summary, statusId: statusId || undefined, priority, leadId: ownerId || undefined, teamIds, memberIds, labelIds, initiativeIds, dependencyIds, issueIds, milestones, visibility }
        if (template) await updateProjectTemplate(template.id, input); else await createProjectTemplate(input)
      }
      await onSaved(); onClose()
    } catch (error) { toast.error(errorMessage(error)) } finally { setSaving(false) }
  }
  const remove = async () => {
    if (!template) return
    try { if (type === 'issue' && lockedTeamId) await deleteIssueTemplate(lockedTeamId, template.id); else if (type === 'issue') await deleteWorkspaceIssueTemplate(template.id); else await deleteProjectTemplate(template.id); await onSaved(); onClose() } catch (error) { toast.error(errorMessage(error)) }
  }
  return <div className="ip-settings-page ip-template-editor" data-i18n-ignore>
    <header className="ip-editor-header"><button onClick={onClose}>{t('Cancel')}</button><strong>{t(template ? `Edit ${type} template` : `New ${type} template`)}</strong><button className="primary" disabled={!name.trim() || saving} onClick={() => void save()}>{t(saving ? 'Saving…' : template ? 'Save' : 'Create')}</button></header>
    <div className="ip-editor-content">
      <EditorField label={t('Template name')}><input autoFocus value={name} onChange={event => setName(event.target.value)}/></EditorField>
      <EditorField label={t('Template description')}><input value={templateDescription} onChange={event => setTemplateDescription(event.target.value)}/></EditorField>
      {type === 'issue' ? <>
        <EditorField label={t('Issue title')}><input value={title} onChange={event => setTitle(event.target.value)}/></EditorField>
        <EditorField label={t('Issue description')}><textarea rows={5} value={body} onChange={event => setBody(event.target.value)}/></EditorField>
        <div className="ip-properties-grid"><SelectField label={t('Team')} value={teamId} onChange={setTeamId} options={data.teams.map(item => [item.id, item.name])} empty={t('Choose when creating')} disabled={Boolean(lockedTeamId)}/><SelectField label={t('Priority')} value={String(priority)} onChange={value => setPriority(Number(value))} options={priorityOptions(t)}/><SelectField label={t('Assignee')} value={ownerId} onChange={setOwnerId} options={data.users.map(item => [item.id, item.displayName])} empty={t('Unassigned')}/><SelectField label={t('Project')} value={projectId} onChange={setProjectId} options={data.projects.map(item => [item.id, item.name])} empty={t('No project')}/></div>
        <SelectField label={t('Status')} value={statusId} onChange={setStatusId} options={data.states.filter(item => !teamId || item.teamId === teamId).map(item => [item.id, item.name])} empty={t('Default')}/>
        <CheckGroup label={t('Labels')} values={labelIds} options={labelOptions.map(item => [item.id, item.name])} onToggle={id => toggle(setLabelIds, id)}/>
        {templateType === 'customForm' && <FormFields fields={formFields} onChange={setFormFields}/>}
      </> : <>
        <EditorField label={t('Project name')}><input value={projectName} onChange={event => setProjectName(event.target.value)}/></EditorField>
        <EditorField label={t('Project summary')}><input value={summary} onChange={event => setSummary(event.target.value)}/></EditorField>
        <div className="ip-properties-grid"><SelectField label={t('Status')} value={statusId} onChange={setStatusId} options={data.projectStatuses.map(item => [item.id, item.name])} empty={t('Default')}/><SelectField label={t('Priority')} value={String(priority)} onChange={value => setPriority(Number(value))} options={priorityOptions(t)}/><SelectField label={t('Lead')} value={ownerId} onChange={setOwnerId} options={data.users.map(item => [item.id, item.displayName])} empty={t('Unassigned')}/><SelectField label={t('Visibility')} value={visibility} onChange={value => setVisibility(value as 'workspace'|'teams')} options={[["workspace", t('Workspace')], ["teams", t('Teams')]]}/></div>
        <CheckGroup label={t('Members')} values={memberIds} options={data.users.map(item => [item.id, item.displayName])} onToggle={id => toggle(setMemberIds, id)}/>
        <CheckGroup label={t('Teams')} values={teamIds} options={data.teams.map(item => [item.id, item.name])} onToggle={id => toggle(setTeamIds, id)} disabled={Boolean(lockedTeamId)}/>
        <CheckGroup label={t('Initiatives')} values={initiativeIds} options={data.initiatives.map(item => [item.id, item.name])} onToggle={id => toggle(setInitiativeIds, id)}/>
        <CheckGroup label={t('Labels')} values={labelIds} options={labelOptions.map(item => [item.id, item.name])} onToggle={id => toggle(setLabelIds, id)}/>
        <CheckGroup label={t('Dependencies')} values={dependencyIds} options={data.projects.filter(item => item.id !== template?.id).map(item => [item.id, item.name])} onToggle={id => toggle(setDependencyIds, id)}/>
        <CheckGroup label={t('Issues')} values={issueIds} options={data.issues.slice(0, 30).map(item => [item.id, `${item.identifier} ${item.title}`])} onToggle={id => toggle(setIssueIds, id)}/>
        <EditorField label={t('Project description')}><textarea rows={5} value={description} onChange={event => setDescription(event.target.value)}/></EditorField>
        <div className="ip-milestones"><div><strong>{t('Milestones')}</strong><button ref={milestoneTriggerRef} onClick={() => setMilestoneOpen(true)}><Plus size={13}/>{t('Add')}</button></div>{milestones.length ? milestones.map(item => <div className="ip-milestone-row" key={item.id}><span><strong data-i18n-ignore>{item.name}</strong><small data-i18n-ignore>{item.description}</small></span><button aria-label={t('Remove milestone')} onClick={() => setMilestones(current => current.filter(value => value.id !== item.id))}><X size={13}/></button></div>) : <small>{t('No milestones')}</small>}</div>
      </>}
      {template && <button className="ip-delete-button" onClick={() => void remove()}><Trash2 size={14}/>{t('Delete template')}</button>}
    </div>
    <MilestoneDialog open={milestoneOpen} onOpenChange={setMilestoneOpen} triggerRef={milestoneTriggerRef} onAdd={milestone => setMilestones(current => [...current, milestone])}/>
  </div>
}

function EditorField({ label, children }: { label: string; children: ReactNode }) { return <label className="ip-editor-field"><span>{label}</span>{children}</label> }
function SelectField({ label, value, options, empty, disabled, onChange }: { label: string; value: string; options: string[][]; empty?: string; disabled?: boolean; onChange: (value: string) => void }) { return <EditorField label={label}><select disabled={disabled} value={value} onChange={event => onChange(event.target.value)}>{empty !== undefined && <option value="">{empty}</option>}{options.map(([id, name]) => <option value={id} key={id} data-i18n-ignore>{name}</option>)}</select></EditorField> }
function CheckGroup({ label, values, options, disabled, onToggle }: { label: string; values: string[]; options: string[][]; disabled?: boolean; onToggle: (id: string) => void }) { const { t } = useI18n(); return <fieldset className="ip-check-group" disabled={disabled}><legend>{label}</legend>{options.length ? options.map(([id, name]) => <label key={id}><input type="checkbox" checked={values.includes(id)} onChange={() => onToggle(id)}/><span data-i18n-ignore>{name}</span></label>) : <small>{t('No options')}</small>}</fieldset> }
function priorityOptions(t: (value: string) => string) { return ['No priority', 'Urgent', 'High', 'Medium', 'Low'].map((item, index) => [String(index), t(item)]) }

function FormFields({ fields, onChange }: { fields: TemplateFormField[]; onChange: (value: TemplateFormField[]) => void }) {
  const { t } = useI18n()
  return <div className="ip-form-fields"><div><strong>{t('Form fields')}</strong><button onClick={() => onChange([...fields, { id: crypto.randomUUID(), label: '', type: 'text', required: false, options: [] }])}><Plus size={13}/>{t('Add field')}</button></div>{fields.map((field, index) => <div className="ip-form-field" key={field.id}><input aria-label={t('Field label')} placeholder={t('Field label')} value={field.label} onChange={event => onChange(fields.map((item, itemIndex) => itemIndex === index ? { ...item, label: event.target.value } : item))}/><select value={field.type} onChange={event => onChange(fields.map((item, itemIndex) => itemIndex === index ? { ...item, type: event.target.value as TemplateFormField['type'] } : item))}>{['text','textarea','select','checkbox','date'].map(value => <option key={value}>{value}</option>)}</select><label><input type="checkbox" checked={field.required} onChange={event => onChange(fields.map((item, itemIndex) => itemIndex === index ? { ...item, required: event.target.checked } : item))}/>{t('Required')}</label><button aria-label={t('Delete field')} onClick={() => onChange(fields.filter((_, itemIndex) => itemIndex !== index))}><Trash2 size={13}/></button></div>)}</div>
}

function MilestoneDialog({ open, onOpenChange, triggerRef, onAdd }: { open: boolean; onOpenChange: (value: boolean) => void; triggerRef: RefObject<HTMLButtonElement|null>; onAdd: (value: TemplateMilestone) => void }) {
  const { t } = useI18n(); const [name, setName] = useState(''); const [description, setDescription] = useState('')
  const add = () => { if (!name.trim()) return; onAdd({ id: crypto.randomUUID(), name: name.trim(), description }); setName(''); setDescription(''); onOpenChange(false) }
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="ip-dialog-overlay"/><Dialog.Content className="ip-dialog" data-i18n-ignore onCloseAutoFocus={event => { event.preventDefault(); triggerRef.current?.focus() }} onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) add() }}><Dialog.Title>{t('Add milestone')}</Dialog.Title><EditorField label={t('Milestone name')}><input autoFocus value={name} onChange={event => setName(event.target.value)}/></EditorField><EditorField label={t('Milestone description template')}><textarea rows={4} value={description} onChange={event => setDescription(event.target.value)}/></EditorField><footer><Dialog.Close asChild><button>{t('Cancel')}</button></Dialog.Close><button className="primary" disabled={!name.trim()} onClick={add}>{t('Add milestone')}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

export function SLASettings({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t } = useI18n(); const settings = (data.settings?.sla ?? {}) as Record<string, unknown>; const enabled = settings.enabled === true
  const [creating, setCreating] = useState(false); const [name, setName] = useState(''); const [targetMinutes, setTargetMinutes] = useState(1440); const [priority, setPriority] = useState(''); const [saving, setSaving] = useState(false)
  const run = async (action: () => Promise<unknown>) => { setSaving(true); try { await action(); await onReload() } catch (error) { toast.error(errorMessage(error)) } finally { setSaving(false) } }
  return <div className="ip-settings-page" data-i18n-ignore><header className="settings-page-header ip-page-header"><div><h1>{t('SLAs')}</h1><p>{t('Set response and resolution expectations for issues that match defined rules.')}{' '}<a href="https://linear.app/docs/sla" target="_blank" rel="noreferrer">{t('Docs')}<ExternalLink size={11}/></a></p></div></header><section className="ip-settings-section"><div className="ip-setting-row"><span><strong>{t('Enable SLAs')}</strong><small>{t('Apply SLA rules and deadlines across your workspace.')}</small></span><button role="switch" aria-checked={enabled} className="settings-toggle" disabled={saving} onClick={() => void run(() => updateSLASettings({ enabled: !enabled }))}><span/></button></div></section><section className="ip-settings-section"><header><h3>{t('Automation rules')}</h3><button className="settings-action" disabled={!enabled || creating} onClick={() => setCreating(true)}><Plus size={14}/>{t('Add rule')}</button></header>{creating && <form className="ip-rule-editor" onSubmit={event => { event.preventDefault(); if (!name.trim()) return; void run(() => createSLARule({ name: name.trim(), targetMinutes, teamIds: data.teams.map(item => item.id), filters: priority ? { priority: Number(priority) } : {}, pauseStatuses: ['completed','canceled'], enabled: true })).then(() => { setCreating(false); setName('') }) }}><EditorField label={t('Rule name')}><input autoFocus value={name} onChange={event => setName(event.target.value)}/></EditorField><div className="ip-properties-grid"><SelectField label={t('Target')} value={String(targetMinutes)} onChange={value => setTargetMinutes(Number(value))} options={[["60",t('1 hour')],["240",t('4 hours')],["480",t('8 hours')],["1440",t('24 hours')],["4320",t('3 days')]]}/><SelectField label={t('Priority')} value={priority} onChange={setPriority} empty={t('Any priority')} options={priorityOptions(t).slice(1)}/></div><footer><button type="button" onClick={() => setCreating(false)}>{t('Cancel')}</button><button className="primary" disabled={!name.trim() || saving}>{t('Create')}</button></footer></form>}{data.slaRules.length ? data.slaRules.map(rule => <div className="ip-setting-row" key={rule.id}><span><strong data-i18n-ignore>{rule.name}</strong><small>{formatDuration(rule.targetMinutes, t)} · {t(rule.enabled ? 'Active' : 'Disabled')}</small></span><div className="ip-row-actions"><button role="switch" aria-checked={rule.enabled} className="settings-toggle" disabled={!enabled || saving} onClick={() => void run(() => updateSLARule(rule.id, { enabled: !rule.enabled }))}><span/></button><button aria-label={t('Delete rule')} onClick={() => void run(() => deleteSLARule(rule.id))}><Trash2 size={14}/></button></div></div>) : !creating && <div className="ip-empty-row"><span className="ip-template-glyph"><Circle size={14}/></span><span><strong>{t('No automation rules')}</strong><small>{enabled ? t('Add a rule to begin applying SLAs.') : t('Enable SLAs to add automation rules.')}</small></span></div>}</section></div>
}

export function ProjectUpdateSettings({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t } = useI18n(); const current = (data.settings?.projectUpdates ?? {}) as Record<string, unknown>; const initial = Number(current.cadenceDays ?? 0)
  const [editing, setEditing] = useState(false); const [cadence, setCadence] = useState(initial); const [saving, setSaving] = useState(false); const cadenceLabel = cadenceOptions(t).find(item => item[0] === cadence)?.[1] ?? t('No expectation')
  const save = async () => { setSaving(true); try { await updateProjectUpdateSettings({ cadenceDays: cadence }); await onReload(); setEditing(false); toast.success(t('Update schedule saved')) } catch (error) { toast.error(errorMessage(error)) } finally { setSaving(false) } }
  const connectSlack = async () => { setSaving(true); try { await connectIntegration('slack', { name: 'Slack', config: { source: 'project-updates' } }); toast.success(t('Slack connected')) } catch (error) { toast.error(errorMessage(error)) } finally { setSaving(false) } }
  return <div className="ip-settings-page" data-i18n-ignore><header className="settings-page-header ip-page-header"><div><h1>{t('Project updates')}</h1><p>{t('Configure when project updates are expected and where reminders are sent.')}{' '}<a href="https://linear.app/docs/project-updates" target="_blank" rel="noreferrer">{t('Docs')}<ExternalLink size={11}/></a></p></div></header><section className="ip-settings-section"><header><h3>{t('Update schedule')}</h3></header><div className="ip-setting-row"><span><strong>{t('Update cadence')}</strong><small>{cadenceLabel}</small></span>{editing ? <div className="ip-update-editor"><CadenceMenu value={cadence} onChange={setCadence}/><button onClick={() => { setCadence(initial); setEditing(false) }}>{t('Cancel')}</button><button className="primary" disabled={saving} onClick={() => void save()}>{t('Save')}</button></div> : <button className="settings-action" onClick={() => setEditing(true)}>{t('Edit')}</button>}</div></section><section className="ip-settings-section"><header><h3>Slack</h3></header><div className="ip-setting-row"><span className="ip-slack-label"><MessageSquare size={17}/><span><strong>{t('Project update notifications')}</strong><small>{t('Send project update reminders and notifications to Slack.')}</small></span></span><button className="settings-action" disabled={saving} onClick={() => void connectSlack()}>{t('Connect')}</button></div></section></div>
}

function CadenceMenu({ value, onChange }: { value: number; onChange: (value: number) => void }) {
  const { t } = useI18n(); const options = cadenceOptions(t); const label = options.find(item => item[0] === value)?.[1]
  return <Dropdown.Root><Dropdown.Trigger asChild><button className="ip-combobox" role="combobox">{label}<ChevronDown size={13}/></button></Dropdown.Trigger><Dropdown.Portal><Dropdown.Content className="ip-cadence-menu" align="end" sideOffset={5} data-i18n-ignore>{options.map(([days, text]) => <Dropdown.Item className="ip-cadence-item" key={days} onSelect={() => onChange(days)}>{text}{days === value && <Check size={13}/>}</Dropdown.Item>)}</Dropdown.Content></Dropdown.Portal></Dropdown.Root>
}
function cadenceOptions(t: (value: string) => string): Array<[number,string]> { return [[0,t('No expectation')],[7,t('Every week')],...[2,3,4,5,6,7,8].map(value => [value * 7, t(`Every ${value} weeks`)] as [number,string])] }

export function ProjectStatusesSettings({ data, onReload }: { data: BootstrapData; onReload: () => Promise<void> }) {
  const { t } = useI18n(); const [creating, setCreating] = useState<StatusType|null>(null); const [editing, setEditing] = useState<string|null>(null); const ordered = useMemo(() => [...data.projectStatuses].sort((a,b) => (a.position ?? 0) - (b.position ?? 0)), [data.projectStatuses])
  const run = async (action: () => Promise<unknown>) => { try { await action(); await onReload() } catch (error) { toast.error(errorMessage(error)) } }
  const move = (status: ProjectStatus, delta: number) => { const group = ordered.filter(item => item.type === status.type); const index = group.findIndex(item => item.id === status.id); const target = index + delta; if (target < 0 || target >= group.length) return; const next = [...ordered]; const left = next.findIndex(item => item.id === group[index].id); const right = next.findIndex(item => item.id === group[target].id); [next[left], next[right]] = [next[right], next[left]]; void run(() => reorderProjectStatuses(next.map(item => item.id))) }
  return <div className="ip-settings-page" data-i18n-ignore><header className="settings-page-header ip-page-header"><div><h1>{t('Project statuses')}</h1><p>{t('Project statuses define the workflow projects move through.')}</p></div></header>{STATUS_SECTIONS.map(section => { const statuses = ordered.filter(item => item.type === section.type); return <section className="ip-status-section" key={section.type}><header><h3>{t(section.label)}</h3><button aria-label={t(`Add ${section.label} status`)} disabled={creating !== null} onClick={() => setCreating(section.type)}><Plus size={14}/></button></header>{creating === section.type && <StatusEditor type={section.type} onCancel={() => setCreating(null)} onSave={async input => { await run(() => createProjectStatus(input)); setCreating(null) }}/>} {statuses.map((status, index) => editing === status.id ? <StatusEditor key={status.id} type={section.type} status={status} onCancel={() => setEditing(null)} onSave={async input => { await run(() => updateProjectStatus(status.id, input)); setEditing(null) }}/> : <StatusRow key={status.id} status={status} usage={data.projects.filter(item => item.status.id === status.id).length} first={index === 0} last={index === statuses.length - 1} onEdit={() => setEditing(status.id)} onMove={delta => move(status, delta)} onDelete={() => run(() => deleteProjectStatus(status.id))}/>)}</section> })}</div>
}
function StatusEditor({ type, status, onCancel, onSave }: { type: StatusType; status?: ProjectStatus; onCancel: () => void; onSave: (input: { name: string; description: string; color: string; type: string }) => Promise<void> }) {
  const { t } = useI18n(); const [name, setName] = useState(status?.name ?? ''); const [description, setDescription] = useState(status?.description ?? ''); const [color, setColor] = useState(status?.color ?? '#8b8d98'); const [saving, setSaving] = useState(false)
  const submit = async () => { if (!name.trim()) return; setSaving(true); try { await onSave({ name: name.trim(), description: description.trim(), color, type }) } finally { setSaving(false) } }
  return <form className="ip-status-editor" onSubmit={event => { event.preventDefault(); void submit() }} onKeyDown={event => { if (event.key === 'Escape') onCancel() }}><label className="ip-color-input" style={{ '--status-color': color } as CSSProperties}><input aria-label={t('Color')} type="color" value={color} onChange={event => setColor(event.target.value)}/></label><div><input autoFocus placeholder={t('Status name')} value={name} onChange={event => setName(event.target.value)}/><input placeholder={t('Description')} value={description} onChange={event => setDescription(event.target.value)}/></div><footer><button type="button" onClick={onCancel}>{t('Cancel')}</button><button className="primary" disabled={!name.trim() || saving}>{t(status ? 'Save' : 'Create')}</button></footer></form>
}
function StatusRow({ status, usage, first, last, onEdit, onMove, onDelete }: { status: ProjectStatus; usage: number; first: boolean; last: boolean; onEdit: () => void; onMove: (delta: number) => void; onDelete: () => Promise<void> }) {
  const { t } = useI18n()
  return <div className="ip-status-row"><span className="ip-status-dot" style={{ background: status.color }}/><span><strong data-i18n-ignore>{status.name}</strong><small data-i18n-ignore>{status.description}</small></span><small>{usage} {t(usage === 1 ? 'project' : 'projects')}</small><Dropdown.Root><Dropdown.Trigger asChild><button aria-label={t(`Actions for ${status.name}`)}><MoreHorizontal size={15}/></button></Dropdown.Trigger><Dropdown.Portal><Dropdown.Content className="ip-row-menu" align="end" sideOffset={4} data-i18n-ignore><Dropdown.Item onSelect={onEdit}>{t('Edit')}</Dropdown.Item><Dropdown.Item disabled={first} onSelect={() => onMove(-1)}>{t('Move up')}</Dropdown.Item><Dropdown.Item disabled={last} onSelect={() => onMove(1)}>{t('Move down')}</Dropdown.Item><Dropdown.Separator/><Dropdown.Item className="danger" disabled={usage > 0} onSelect={() => void onDelete()}><Trash2 size={13}/>{t('Delete')}</Dropdown.Item></Dropdown.Content></Dropdown.Portal></Dropdown.Root></div>
}

function formatDuration(minutes: number, t: (value: string) => string) { if (minutes % 1440 === 0) return t(`${minutes / 1440} days`); if (minutes % 60 === 0) return t(`${minutes / 60} hours`); return t(`${minutes} minutes`) }
function errorMessage(error: unknown) { return error instanceof Error ? error.message : 'Could not save setting' }
