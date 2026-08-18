import { useEffect, useMemo, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { CalendarDays, Check, ChevronRight, Diamond, ExternalLink, FileText, Flag, Link2, MessageSquare as Slack, MoreHorizontal, Plus, Tags, Trash2, Users } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { PropertyMenu } from '@/components/property/property-menu'
import { Avatar } from '@/components/issue/issue-row'
import { NoAssigneeIcon, PriorityIcon, TeamIcon } from '@/components/issue/issue-icons'
import { ViewIconPicker } from '@/components/views/view-icon-picker'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { Issue, ProjectResource } from '@/types/flow'
import type { ProjectDetailProps } from './project-detail-types'
import { PRIORITY_LABELS } from './project-detail-types'

type Props = ProjectDetailProps & { projectIssues: Issue[]; save: (input: ProjectMutationInput) => Promise<void> }

export function ProjectOverview({ project, projects, projectUpdates, users, teams, labels, projectIssues, save, onCreateResource, onUpdateResource, onDeleteResource, onTabChange }: Props) {
  const statuses = useMemo(() => uniqueById(projects.map(item => item.status)), [projects])
  const members = users.filter(user => (project.memberIds ?? []).includes(user.id))
  const projectTeams = teams.filter(team => (project.teamIds ?? []).includes(team.id))

  return <div className="project-overview">
    <section className="project-overview__intro">
      <ViewIconPicker color={project.color} icon={project.icon || 'Project'} onChange={visual => void save(visual)} triggerClassName="project-overview__icon"/>
      <EditableText ariaLabel="Project name" className="project-overview__name" placeholder="Project name" value={project.name} onCommit={name => save({ name })}/>
      <EditableText ariaLabel="Project summary" className="project-overview__summary" placeholder="Add a short summary…" value={project.summary} onCommit={summary => save({ summary })}/>
      <div className="project-overview__property-section">
        <h3>Properties</h3>
        <div className="project-overview__properties">
          <PropertyMenu compact label="Status" value={project.status.name} selectedId={project.status.id} icon={<ProjectStatusDot color={project.status.color}/>} options={statuses.map(status => ({ id: status.id, label: status.name, color: status.color, icon: <ProjectStatusDot color={status.color}/> }))} onChange={statusId => void save({ statusId })}/>
          <PropertyMenu compact label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} icon={<PriorityIcon priority={project.priority} size={14}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={14}/>, shortcut: String(priority) }))} onChange={priority => void save({ priority: Number(priority) })}/>
          <PropertyMenu compact label="Lead" value={project.lead?.displayName ?? 'Lead'} selectedId={project.lead?.id ?? ''} icon={project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No lead', icon: <NoAssigneeIcon size={14}/> }, ...users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, keywords: `${user.name} ${user.email}`, icon: <Avatar name={user.displayName}/> }))]} onChange={leadId => void save({ leadId })}/>
          <PropertyMenu compact multiple label="Members" value={members.length === 1 ? members[0].displayName : members.length ? `${members.length} members` : 'Members'} selectedIds={project.memberIds ?? []} icon={members[0] ? <Avatar name={members[0].displayName}/> : <Users size={14}/>} options={users.filter(user => user.active).map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/> }))} onChange={memberId => void save({ memberIds: (project.memberIds ?? []).includes(memberId) ? project.memberIds.filter(id => id !== memberId) : [...(project.memberIds ?? []), memberId] })}/>
          <DateProperty label="Start date" placeholder="Start date" value={project.startDate} onChange={startDate => void save({ startDate })}/>
          <span aria-hidden="true" className="project-overview__date-arrow">→</span>
          <DateProperty label="Target date" placeholder="Target date" value={project.targetDate} onChange={targetDate => void save({ targetDate })}/>
          <button className="project-overview__team" disabled type="button"><TeamIcon size={14}/>{projectTeams.map(team => team.name).join(', ') || 'Team'}</button>
          <ProjectMoreMenu labels={labels} project={project} projects={projects} save={save}/>
        </div>
      </div>
    </section>

    <InitiativeSection project={project} projects={projects} save={save}/>
    <ResourceSection onCreate={input => onCreateResource(project.id, input)} onDelete={resourceId => onDeleteResource(project.id, resourceId)} onUpdate={(resourceId, input) => onUpdateResource(project.id, resourceId, input)} resources={project.resources ?? []}/>
    {(project.customers?.length ?? 0) > 0 && <InlineStringSection addLabel="Add customer request" items={project.customers ?? []} onChange={customers => void save({ customers })} title="Customers"/>}

    <section className="project-overview__latest">
      {projectUpdates[0] ? <button className="project-overview__latest-update" onClick={() => onTabChange('activity')} type="button"><span className={`project-overview__health is-${projectUpdates[0].health}`}/><div><strong>{projectUpdates[0].user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(projectUpdates[0].createdAt), { addSuffix: true })}</time><p>{projectUpdates[0].body}</p></div></button> : <button className="project-overview__first-update" onClick={() => onTabChange('activity')} type="button"><FileText size={14}/>Write first project update</button>}
    </section>

    <section className="project-overview__description">
      <h3>Description</h3>
      <EditableText ariaLabel="Project description" className="project-overview__description-editor" multiline placeholder="Add description…" value={project.description} onCommit={description => save({ description })}/>
    </section>

    <section className="project-overview__milestones">
      {(project.milestones?.length ?? 0) > 0 && <h3>Milestones</h3>}
      {(project.milestones ?? []).map(milestone => <article className="project-overview__milestone" key={milestone.id}>
        <header>
          <span className="project-overview__milestone-mark"><Diamond size={12}/></span>
          <strong>{milestone.name}</strong>
          <time>{milestone.targetDate ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : 'No target date'}</time>
          <button aria-label={`${milestone.name} actions`} onClick={() => document.querySelector<HTMLElement>('[data-project-milestone-add]')?.click()} type="button"><MoreHorizontal size={14}/></button>
        </header>
      </article>)}
      <button className="project-overview__milestone-link" type="button" onClick={() => document.querySelector<HTMLElement>('[data-project-milestone-add]')?.click()}><Diamond size={15}/>Milestone</button>
    </section>
    {projectIssues.length === 0 && <span className="project-overview__scope-note">No issues in scope</span>}
  </div>
}

function InlineStringSection({ addLabel, items, onChange, title }: { addLabel: string; items: string[]; onChange: (items: string[]) => void; title: string }) {
  const [open, setOpen] = useState(false)
  return <section className="project-overview__row-section"><h3>{title}</h3><div className="project-overview__row-content">
    {items.map(item => <span className="project-overview__string-item" key={item}><span>{item}</span><button aria-label={`Remove ${item}`} onClick={() => onChange(items.filter(value => value !== item))} type="button"><Trash2 size={11}/></button></span>)}
    <button className="project-overview__inline-add" onClick={() => setOpen(true)} type="button"><Plus size={13}/>{addLabel}</button>
  </div><StringInputDialog label={addLabel} onOpenChange={setOpen} open={open} onSubmit={value => { onChange([...items, value]); setOpen(false) }}/></section>
}

function ResourceSection({ onCreate, onDelete, onUpdate, resources }: { resources: ProjectResource[]; onCreate: (input: { type?: 'link'|'document'; title?: string; url: string }) => Promise<ProjectResource>; onDelete: (id: string) => Promise<void>; onUpdate: (id: string, input: { type?: 'link'|'document'; title?: string; url?: string }) => Promise<ProjectResource> }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [dialog, setDialog] = useState<{ mode: 'create'|'edit'; resource?: ProjectResource }>()
  const createDocument = async () => {
    await onCreate({ type: 'document', title: 'Untitled document', url: `${location.origin}${location.pathname.replace(/\/(overview|activity|issues)$/, '')}/document/${Date.now()}` })
    setMenuOpen(false)
  }
  return <section className="project-overview__row-section"><h3>Resources</h3><div className="project-overview__row-content">
    {resources.map(resource => <div className="project-overview__resource" key={resource.id}>{resource.type === 'document' ? <FileText size={13}/> : <Link2 size={13}/>}<a href={resource.url} rel="noreferrer" target="_blank">{resource.title}</a><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${resource.title} actions`} type="button"><MoreHorizontal size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}><DropdownMenu.Item onSelect={() => window.open(resource.url, '_blank')}><ExternalLink size={14}/><span>Open</span></DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({ mode: 'edit', resource })}><Link2 size={14}/><span>Edit link</span></DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="is-danger" onSelect={() => void onDelete(resource.id)}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>)}
    <DropdownMenu.Root onOpenChange={setMenuOpen} open={menuOpen}><DropdownMenu.Trigger asChild><button className="project-overview__inline-add" type="button"><Plus size={13}/>Add document or link…</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu project-overview__resource-menu" sideOffset={4}><DropdownMenu.Label>Add document or link…</DropdownMenu.Label><DropdownMenu.Item onSelect={() => void createDocument()}><FileText size={14}/><span>Create new document…</span></DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({ mode: 'create' })}><Link2 size={14}/><span>Add a link…</span><kbd>Ctrl L</kbd></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  </div><ResourceDialog key={dialog?.resource?.id ?? dialog?.mode ?? 'closed'} onOpenChange={open => { if (!open) setDialog(undefined) }} open={Boolean(dialog)} resource={dialog?.resource} onSubmit={async input => { if (dialog?.resource) await onUpdate(dialog.resource.id, input); else await onCreate({ type: 'link', url: input.url!, title: input.title }); setDialog(undefined) }}/></section>
}

function ResourceDialog({ onOpenChange, onSubmit, open, resource }: { onOpenChange: (open: boolean) => void; onSubmit: (input: { title?: string; url?: string }) => Promise<void>; open: boolean; resource?: ProjectResource }) {
  const [url, setUrl] = useState(resource?.url ?? '')
  const [title, setTitle] = useState(resource?.title ?? '')
  const [saving, setSaving] = useState(false)
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__form-dialog"><Dialog.Title>{resource ? 'Edit project link' : 'Add link to project'}</Dialog.Title><label>URL<input autoFocus onChange={event => setUrl(event.target.value)} placeholder="https://…" value={url}/></label><label>Title <small>(optional)</small><input onChange={event => setTitle(event.target.value)} value={title}/></label><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!url.trim() || saving} onClick={() => { setSaving(true); void onSubmit({ url: url.trim(), title: title.trim() }).catch(error => toast.error('Could not save link', { description: error instanceof Error ? error.message : undefined })).finally(() => setSaving(false)) }} type="button">{saving ? 'Saving…' : resource ? 'Save' : 'Add link'}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function StringInputDialog({ label, onOpenChange, onSubmit, open }: { label: string; onOpenChange: (open: boolean) => void; onSubmit: (value: string) => void; open: boolean }) {
  const [value, setValue] = useState('')
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__form-dialog project-detail-page__string-dialog"><Dialog.Title>{label.replace('…','')}</Dialog.Title><input autoFocus aria-label={label} onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && value.trim()) onSubmit(value.trim()) }} placeholder="Name" value={value}/><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!value.trim()} onClick={() => onSubmit(value.trim())} type="button">Add</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function ProjectMoreMenu({ labels, project, projects, save }: { labels: Props['labels']; project: Props['project']; projects: Props['projects']; save: Props['save'] }) {
  const initiativeNames = [...new Set(projects.flatMap(item => item.initiatives ?? []))]
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="More project properties" className="project-overview__more" type="button">•••</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu project-overview__more-menu" sideOffset={4}>
    <MultiSubmenu icon={<Flag size={14}/>} label="Initiatives" shortcut="P then N" options={initiativeNames.map(name => ({ id: name, label: name }))} selected={project.initiatives ?? []} onToggle={name => void save({ initiatives: toggleString(project.initiatives ?? [], name) })}/>
    <MultiSubmenu icon={<Link2 size={14}/>} label="Dependencies" options={projects.filter(item => item.id !== project.id).map(item => ({ id: item.id, label: item.name, color: item.color }))} selected={project.dependencyIds ?? []} onToggle={id => void save({ dependencyIds: toggleString(project.dependencyIds ?? [], id) })}/>
    <MultiSubmenu icon={<Tags size={14}/>} label="Labels" shortcut="P then L" options={labels.map(label => ({ id: label.id, label: label.name, color: label.color }))} selected={project.labelIds ?? []} onToggle={id => void save({ labelIds: toggleString(project.labelIds ?? [], id) })}/>
    <DropdownMenu.Separator/><DropdownMenu.Item onSelect={() => toast.info('Slack integration is not connected in this workspace.')}><Slack size={14}/><span>Connect existing Slack channel…</span></DropdownMenu.Item>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function MultiSubmenu({ icon, label, onToggle, options, selected, shortcut }: { icon: ReactNode; label: string; onToggle: (id: string) => void; options: { id: string; label: string; color?: string }[]; selected: string[]; shortcut?: string }) {
  return <DropdownMenu.Sub><DropdownMenu.SubTrigger>{icon}<span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu project-overview__submenu" sideOffset={6}><DropdownMenu.Label>Change {label.toLowerCase()}…</DropdownMenu.Label>{options.length ? options.map(option => <DropdownMenu.CheckboxItem checked={selected.includes(option.id)} key={option.id} onCheckedChange={() => onToggle(option.id)} onSelect={event => event.preventDefault()}>{option.color && <i className="project-overview__option-dot" style={{ background: option.color }}/>}<span>{option.label}</span>{selected.includes(option.id) && <Check size={13}/>}</DropdownMenu.CheckboxItem>) : <DropdownMenu.Label>No options</DropdownMenu.Label>}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
}

function InitiativeSection({ project, projects, save }: { project: Props['project']; projects: Props['projects']; save: Props['save'] }) {
  const options = [...new Set(projects.flatMap(item => item.initiatives ?? []))].map(name => ({ id: name, label: name, icon: <Flag size={13}/> }))
  return <section className="project-overview__row-section"><h3>Initiatives</h3><div className="project-overview__row-content project-overview__initiatives">
    {(project.initiatives ?? []).map(name => <span className="project-overview__initiative" key={name}><Flag size={13}/>{name}</span>)}
    <PropertyMenu compact multiple label="Initiatives" value="Add initiative…" selectedIds={project.initiatives ?? []} options={options} icon={<Plus size={13}/>} onChange={name => void save({ initiatives: toggleString(project.initiatives ?? [], name) })}/>
  </div></section>
}

function DateProperty({ label, onChange, placeholder, value }: { label: 'Start date'|'Target date'; onChange: (value: string) => void; placeholder: string; value?: string }) {
  return <ProjectDatePicker buttonClassName="project-overview__date" label={label} onChange={onChange} value={value}><CalendarDays size={14}/><span>{value ? format(new Date(`${value}T00:00:00`), 'MMM do') : placeholder}</span></ProjectDatePicker>
}

function EditableText({ ariaLabel, className, multiline, onCommit, placeholder, value }: { ariaLabel: string; className: string; multiline?: boolean; onCommit: (value: string) => Promise<void>; placeholder: string; value: string }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const commit = () => { const next = draft.trim(); if (next !== value) void onCommit(next) }
  if (multiline) return <textarea aria-label={ariaLabel} className={className} onBlur={commit} onChange={event => setDraft(event.target.value)} placeholder={placeholder} value={draft}/>
  return <input aria-label={ariaLabel} className={className} onBlur={commit} onChange={event => setDraft(event.target.value)} placeholder={placeholder} value={draft}/>
}

function ProjectStatusDot({ color }: { color: string }) { return <span aria-hidden="true" className="project-overview__status" style={{ borderColor: color }}/>} 
function uniqueById<T extends { id: string }>(items: T[]) { return [...new Map(items.map(item => [item.id, item])).values()] }
function toggleString(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
