import { useEffect, useMemo, useState, type ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { Check, ChevronRight, Diamond, ExternalLink, FileText, Flag, Link2, MoreHorizontal, Plus, Trash2, X } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { PropertyMenu } from '@/components/property/property-menu'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { Avatar } from '@/components/issue/issue-row'
import { CalendarIcon, LabelIcon, NoAssigneeIcon, PriorityIcon, ProjectStatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { projectStatusOptionColor } from '@/lib/project-status-color'
import { ViewIconPicker } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { groupOptionSections } from '@/lib/group-options'
import { useI18n } from '@/i18n/i18n'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { Issue, ProjectResource, Team } from '@/types/flow'
import type { ProjectDetailProps } from './project-detail-types'
import { PRIORITY_LABELS } from './project-detail-types'
import { toggleGroupedLabelIds } from '@/lib/labels'
import { formatProjectPropertyDate, initiativeStatusLabel, inviteProjectMember } from './project-detail-helpers'

type Props = ProjectDetailProps & { projectIssues: Issue[]; save: (input: ProjectMutationInput) => Promise<void> }

export function ProjectOverview({ project, projects, initiatives, projectStatuses, projectUpdates, users, teams, labels, labelGroups, projectIssues, save, onCreateResource, onUpdateResource, onDeleteResource, onCreateMilestone, onUpdateMilestone, onDeleteMilestone, onOpenMilestoneIssues = () => onTabChange('issues'), onTabChange }: Props & { onOpenMilestoneIssues?: (milestoneId?: string) => void }) {
  const statuses = useMemo(() => uniqueById(projectStatuses.length ? projectStatuses : projects.map(item => item.status)), [projectStatuses, projects])
  const members = users.filter(user => (project.memberIds ?? []).includes(user.id))
  const selectedMemberIds = [...new Set([...(project.memberIds ?? []), ...(project.lead?.id ? [project.lead.id] : [])])]
  const projectTeams = teams.filter(team => (project.teamIds ?? []).includes(team.id))
  const [creatingMilestone, setCreatingMilestone] = useState(false)

  return <div className="project-overview">
    <section className="project-overview__intro">
      <ViewIconPicker color={project.color} icon={normalizeProjectIcon(project.icon)} onChange={visual => void save(visual)} triggerClassName="project-overview__icon"/>
      <ProjectEditableText ariaLabel="Project name" className="project-overview__name" placeholder="Project name" value={project.name} onCommit={name => save({ name })}/>
      <ProjectEditableText ariaLabel="Project summary" className="project-overview__summary" placeholder="Add a short summary…" value={project.summary} onCommit={summary => save({ summary })}/>
      <div className="project-overview__property-section">
        <h3>Properties</h3>
        <div className="project-overview__properties">
          <PropertyMenu compact label="Status" value={project.status.name} selectedId={project.status.id} icon={<ProjectStatusIcon color={project.status.color} name={project.status.name} size={14} type={project.status.type}/>} options={statuses.map((status, index) => ({ id: status.id, label: status.name, color: projectStatusOptionColor(status, project.status), icon: <ProjectStatusIcon color={projectStatusOptionColor(status, project.status)} name={status.name} size={14} type={status.type}/>, shortcut: String(index + 1) }))} searchPlaceholder="Change status…" searchShortcut="P, then S" surfaceClassName="project-details-sidebar__property-menu is-standard" onChange={statusId => void save({ statusId })}/>
          <PropertyMenu compact label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} icon={<PriorityIcon priority={project.priority} size={14}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={14}/>, shortcut: String(priority) }))} searchPlaceholder="Change priority…" searchShortcut="P, then P" surfaceClassName="project-details-sidebar__property-menu is-standard" onChange={priority => void save({ priority: Number(priority) })}/>
          <PropertyMenu compact label="Lead" value={project.lead?.displayName ?? 'Lead'} valueIsEntityName={Boolean(project.lead)} selectedId={project.lead?.id ?? ''} icon={project.lead ? <Avatar name={project.lead.displayName}/> : <NoAssigneeIcon size={14}/>} options={[{ id: '', label: 'No lead', icon: <NoAssigneeIcon size={14}/>, shortcut: '0' }, ...users.map(user => ({ id: user.id, label: user.displayName, keywords: `${user.name} ${user.email}`, icon: <Avatar name={user.displayName}/>, groupLabel: user.id === project.lead?.id ? undefined : 'Users from the project team', end: user.active ? undefined : 'Invited', i18nIgnore: true })), { id: '__invite-project-member__', label: 'Invite and add…', icon: <Plus size={14}/>, groupLabel: 'New user' }]} searchPlaceholder="Change lead…" searchShortcut="P, then A" surfaceClassName="project-details-sidebar__property-menu is-standard" onChange={leadId => leadId === '__invite-project-member__' ? inviteProjectMember() : void save({ leadId })}/>
          {members.length > 0 && <PropertyMenu compact multiple label="Members" value={members.length === 1 ? members[0].displayName : `${members.length} members`} valueIsEntityName={members.length === 1} selectedIds={selectedMemberIds} icon={<Avatar name={members[0].displayName}/>} options={[...users.map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/>, groupLabel: selectedMemberIds.includes(user.id) ? undefined : 'Users from the project team', end: project.lead?.id === user.id ? 'Project lead' : user.active ? undefined : 'Invited', i18nIgnore: true })), { id: '__invite-project-member__', label: 'Invite and add…', icon: <Plus size={14}/>, groupLabel: 'New user' }]} searchPlaceholder="Change members…" searchShortcut="P, then M" surfaceClassName="project-details-sidebar__property-menu is-members" onChange={memberId => { if (memberId === '__invite-project-member__') { inviteProjectMember(); return } if (memberId === project.lead?.id) return; void save({ memberIds: (project.memberIds ?? []).includes(memberId) ? project.memberIds.filter(id => id !== memberId) : [...(project.memberIds ?? []), memberId] }) }}/>}
          {project.startDate && <><DateProperty label="Start date" max={project.targetDate} placeholder="Start date" resolution={project.startDateResolution} value={project.startDate} onChange={(startDate, startDateResolution) => void save({ startDate, startDateResolution: startDateResolution ?? '' })}/><span aria-hidden="true" className="project-overview__date-arrow">→</span></>}
          <DateProperty label="Target date" min={project.startDate} placeholder="Target date" resolution={project.targetDateResolution} value={project.targetDate} onChange={(targetDate, targetDateResolution) => void save({ targetDate, targetDateResolution: targetDateResolution ?? '' })}/>
          <button className="project-overview__team" data-i18n-ignore={projectTeams.length ? true : undefined} disabled type="button"><TeamIcon size={14}/>{projectTeams.map(team => team.name).join(', ') || 'Team'}</button>
          <ProjectMoreMenu initiatives={initiatives} labelGroups={labelGroups} labels={labels} project={project} projects={projects} save={save}/>
        </div>
      </div>
    </section>

    <InitiativeSection initiatives={initiatives} project={project} save={save}/>
    <ProjectLabelSection labels={labels} labelGroups={labelGroups} project={project} save={save}/>
    <ResourceSection onCreate={input => onCreateResource(project.id, input)} onDelete={resourceId => onDeleteResource(project.id, resourceId)} onUpdate={(resourceId, input) => onUpdateResource(project.id, resourceId, input)} resources={project.resources ?? []} teams={teams}/>
    <InlineStringSection addLabel="Add customer request" items={project.customers ?? []} onChange={customers => void save({ customers })} title="Customers"/>

    <section className="project-overview__latest">
      {projectUpdates[0] ? <button className="project-overview__latest-update" onClick={() => onTabChange('activity')} type="button"><span className={`project-overview__health is-${projectUpdates[0].health}`}/><div><strong data-i18n-ignore>{projectUpdates[0].user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(projectUpdates[0].createdAt), { addSuffix: true })}</time><p data-i18n-ignore>{projectUpdates[0].body}</p></div></button> : <button className="project-overview__first-update" onClick={() => onTabChange('activity')} type="button"><FileText size={14}/>Write first project update</button>}
    </section>

    <section className="project-overview__description">
      <h3>Description</h3>
      <ProjectDescriptionEditor value={project.description} onCommit={description => save({ description })}/>
    </section>

    <section className="project-overview__milestones">
      {(project.milestones?.length ?? 0) > 0 && <h3>Milestones</h3>}
      {(project.milestones ?? []).map(milestone => <OverviewMilestone issues={projectIssues.filter(issue => issue.projectMilestoneId === milestone.id)} key={milestone.id} milestone={milestone} onDelete={() => onDeleteMilestone(project.id, milestone.id)} onOpenIssues={() => onOpenMilestoneIssues(milestone.id)} onUpdate={input => onUpdateMilestone(project.id, milestone.id, input)}/>)}
      {creatingMilestone && <OverviewMilestoneCreator
        onCancel={() => setCreatingMilestone(false)}
        onCreate={async input => { await onCreateMilestone(project.id, input); setCreatingMilestone(false) }}
      />}
      {!creatingMilestone && <button className="project-overview__milestone-link" type="button" onClick={() => setCreatingMilestone(true)}><Diamond size={15}/>Milestone</button>}
    </section>
    {projectIssues.length === 0 && <span className="project-overview__scope-note">No issues in scope</span>}
  </div>
}

function OverviewMilestone({ issues, milestone, onDelete, onOpenIssues, onUpdate }: { issues: Issue[]; milestone: Props['project']['milestones'][number]; onDelete: () => Promise<void>; onOpenIssues: () => void; onUpdate: (input: { name?: string; description?: string; targetDate?: string }) => Promise<unknown> }) {
  const { formatDate, locale } = useI18n()
  const [expanded, setExpanded] = useState(true)
  const completed = issues.filter(issue => issue.state.type === 'completed').length
  const progress = issues.length ? Math.round(completed / issues.length * 100) : 0
  const link = `${location.origin}${location.pathname.replace(/\/overview$/, '/issues')}?projectMilestoneId=${encodeURIComponent(milestone.id)}`
  const copy = (value: string, message: string) => void navigator.clipboard.writeText(value).then(() => toast.success(message))
  return <article className="project-overview__milestone" data-expanded={expanded} id={`milestone-${milestone.id}`}>
    <header>
      <span className="project-overview__milestone-mark"><MilestoneProgress progress={progress}/></span>
      <ProjectEditableText ariaLabel="Milestone name" className="project-overview__milestone-name" placeholder="Milestone name" value={milestone.name} onCommit={name => onUpdate({ name }).then(() => undefined)}/>
      <button aria-expanded={expanded} aria-label={expanded ? 'Collapse' : 'Expand'} className="project-overview__milestone-collapse" onClick={() => setExpanded(value => !value)} type="button"><ChevronRight size={16}/></button>
      <span className="project-overview__milestone-spacer"/>
      <ProjectDatePicker buttonClassName="project-overview__milestone-date" label="Target date" onChange={targetDate => void onUpdate({ targetDate })} value={milestone.targetDate}><span>{milestone.targetDate ? locale === 'en-US' ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : formatDate(`${milestone.targetDate}T00:00:00`, { month: 'short', day: 'numeric' }) : 'Choose date'}</span></ProjectDatePicker>
      <span aria-hidden="true" className="project-overview__milestone-dot">·</span>
      <a aria-label="Open issues" className="project-overview__milestone-issues" href={link} onClick={event => { event.preventDefault(); onOpenIssues() }}>{issues.length} {issues.length === 1 ? 'issue' : 'issues'}<span>·</span>{progress}%</a>
      <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="Open menu" className="project-overview__milestone-menu-trigger" type="button"><MoreHorizontal size={12}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu project-overview__milestone-menu" sideOffset={4}>
        <DropdownMenu.Item onSelect={() => copy(link, 'Milestone link copied')}><Link2 size={14}/><span>Copy link</span></DropdownMenu.Item>
        <DropdownMenu.Item onSelect={() => copy(`[${milestone.name}](${link})`, 'Milestone name and link copied')}><Link2 size={14}/><span>Copy name as link</span></DropdownMenu.Item>
        <DropdownMenu.Separator/>
        <DropdownMenu.Item className="is-danger" onSelect={() => { void confirmAction(`Delete “${milestone.name}”?`,{confirmLabel:'Delete milestone'}).then(confirmed=>{if(confirmed)return onDelete()}) }}><Trash2 size={14}/><span>Delete…</span></DropdownMenu.Item>
      </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
    </header>
    {expanded && <ProjectEditableText
      ariaLabel="Milestone description"
      className="project-overview__milestone-description"
      multiline
      placeholder="Add milestone description…"
      value={milestone.description ?? ''}
      onCommit={description => onUpdate({ description }).then(() => undefined)}
    />}
  </article>
}

function MilestoneProgress({ progress }: { progress: number }) {
  const clamped = Math.max(0, Math.min(100, progress))
  return <svg aria-hidden="true" className="project-overview__milestone-progress" height="16" viewBox="0 0 16 16" width="16"><path d="M7.3406 2.32c.3468-.4267.972-.4267 1.3188 0l4.1309 5.082c.2796.344.2796.852 0 1.196L8.6594 13.68c-.3468.4267-.972.4267-1.3188 0L3.2097 8.598a.95.95 0 0 1 0-1.196L7.3406 2.32Z" fill="none" opacity=".3" stroke="currentColor" strokeLinejoin="round" strokeWidth="2"/><path d="M7.3406 2.32c.3468-.4267.972-.4267 1.3188 0l4.1309 5.082c.2796.344.2796.852 0 1.196L8.6594 13.68c-.3468.4267-.972.4267-1.3188 0L3.2097 8.598a.95.95 0 0 1 0-1.196L7.3406 2.32Z" fill="none" pathLength="100" stroke="currentColor" strokeDasharray={`${clamped} ${100 - clamped}`} strokeLinecap="round" strokeLinejoin="round" strokeWidth="2"/></svg>
}

function OverviewMilestoneCreator({ onCancel, onCreate }: { onCancel: () => void; onCreate: (input: { name: string; description?: string; targetDate?: string }) => Promise<void> }) {
  const { formatDate, locale } = useI18n()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [targetDate, setTargetDate] = useState('')
  const [saving, setSaving] = useState(false)
  const submit = () => { if (!name.trim() || saving) return; setSaving(true); void onCreate({ name: name.trim(), description: description.trim(), targetDate }).finally(() => setSaving(false)) }
  return <form className="project-overview__milestone project-overview__milestone-creator" onSubmit={event => { event.preventDefault(); submit() }}>
    <header><span className="project-overview__milestone-mark"><MilestoneProgress progress={0}/></span><input autoFocus aria-label="Milestone name" className="project-overview__milestone-name" disabled={saving} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') onCancel() }} placeholder="Milestone name" value={name}/><span className="project-overview__milestone-spacer"/><ProjectDatePicker buttonClassName="project-overview__milestone-date" label="Target date" onChange={setTargetDate} value={targetDate}><span>{targetDate ? locale === 'en-US' ? format(new Date(`${targetDate}T00:00:00`), 'MMM d') : formatDate(`${targetDate}T00:00:00`, { month: 'short', day: 'numeric' }) : 'Choose date'}</span></ProjectDatePicker><button aria-label="Cancel" className="project-overview__milestone-menu-trigger" onClick={onCancel} type="button"><X size={12}/></button></header>
    <textarea aria-label="Milestone description" className="project-overview__milestone-description" disabled={saving} onChange={event => setDescription(event.target.value)} placeholder="Add milestone description…" value={description}/>
  </form>
}

function InlineStringSection({ addLabel, items, onChange, title }: { addLabel: string; items: string[]; onChange: (items: string[]) => void; title: string }) {
  const [open, setOpen] = useState(false)
  return <section className="project-overview__row-section"><h3>{title}</h3><div className="project-overview__row-content">
    {items.map(item => <span className="project-overview__string-item" key={item}><span>{item}</span><button aria-label={`Remove ${item}`} onClick={() => onChange(items.filter(value => value !== item))} type="button"><Trash2 size={11}/></button></span>)}
    <button className="project-overview__inline-add" onClick={() => setOpen(true)} type="button"><Plus size={13}/>{addLabel}</button>
  </div><StringInputDialog label={addLabel} onOpenChange={setOpen} open={open} onSubmit={value => { onChange([...items, value]); setOpen(false) }}/></section>
}

function ResourceSection({ onCreate, onDelete, onUpdate, resources, teams }: { resources: ProjectResource[]; teams: Team[]; onCreate: (input: { type?: 'link'|'document'; title?: string; url?: string }) => Promise<ProjectResource>; onDelete: (id: string) => Promise<void>; onUpdate: (id: string, input: { type?: 'link'|'document'; title?: string; url?: string; pinnedTeamIds?: string[] }) => Promise<ProjectResource> }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [dialog, setDialog] = useState<{ mode: 'create'|'edit'; resource?: ProjectResource }>()
  const [deleteResource, setDeleteResource] = useState<ProjectResource>()
  const createDocument = async () => {
    await onCreate({ type: 'document', title: 'Untitled document' })
    setMenuOpen(false)
  }
  return <section className="project-overview__row-section"><h3>Resources</h3><div className="project-overview__row-content">
    {resources.map(resource => <div className="project-overview__resource" key={resource.id}><a data-i18n-ignore href={resource.url} rel={resource.type === 'link' ? 'noreferrer' : undefined} target={resource.type === 'link' ? '_blank' : undefined}>{resource.type === 'document' ? <FileText size={16}/> : <Link2 size={16}/>}<span>{resource.title}</span>{resource.type === 'link' && <ExternalLink size={16}/>}</a><DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label={`${resource.title} actions`} data-i18n-ignore type="button"><MoreHorizontal size={13}/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="end" className="project-detail-page__menu" sideOffset={4}>
      <DropdownMenu.Item onSelect={() => void navigator.clipboard.writeText(resource.url).then(() => toast.success('Resource link copied'))}><Link2 size={14}/><span>Copy link</span></DropdownMenu.Item>
      <DropdownMenu.Sub><DropdownMenu.SubTrigger><TeamIcon size={14}/><span>Pin to team</span><ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu" sideOffset={6}>{teams.map(team => <DropdownMenu.CheckboxItem checked={(resource.pinnedTeamIds ?? []).includes(team.id)} key={team.id} onCheckedChange={() => void onUpdate(resource.id, { pinnedTeamIds: toggleString(resource.pinnedTeamIds ?? [], team.id) })}><TeamIcon size={14}/><span data-i18n-ignore>{team.name}</span>{(resource.pinnedTeamIds ?? []).includes(team.id) && <Check size={13}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Item onSelect={() => setDialog({ mode: 'edit', resource })}><Link2 size={14}/><span>Edit</span></DropdownMenu.Item>
      <DropdownMenu.Separator/><DropdownMenu.Item className="is-danger" onSelect={() => setDeleteResource(resource)}><Trash2 size={14}/><span>Delete</span></DropdownMenu.Item>
    </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root></div>)}
    <DropdownMenu.Root onOpenChange={setMenuOpen} open={menuOpen}><DropdownMenu.Trigger asChild><button className="project-overview__inline-add" type="button"><Plus size={13}/>Add document or link…</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu project-overview__resource-menu" sideOffset={4}><DropdownMenu.Label>Add document or link…</DropdownMenu.Label><DropdownMenu.Item onSelect={() => void createDocument()}><FileText size={14}/><span>Create new document…</span></DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({ mode: 'create' })}><Link2 size={14}/><span>Add a link…</span><kbd>Ctrl L</kbd></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  </div><ProjectResourceDialog key={dialog?.resource?.id ?? dialog?.mode ?? 'closed'} onOpenChange={open => { if (!open) setDialog(undefined) }} open={Boolean(dialog)} resource={dialog?.resource} onSubmit={async input => { if (dialog?.resource) await onUpdate(dialog.resource.id, input); else await onCreate({ type: 'link', url: input.url!, title: input.title }); setDialog(undefined) }}/>
  <Dialog.Root onOpenChange={open => { if (!open) setDeleteResource(undefined) }} open={Boolean(deleteResource)}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby="project-resource-delete-description" className="project-detail-page__form-dialog"><Dialog.Title>{`Delete “${deleteResource?.title ?? ''}”?`}</Dialog.Title><Dialog.Description id="project-resource-delete-description">This resource will be removed from the project.</Dialog.Description><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-danger" onClick={() => { if (!deleteResource) return; void onDelete(deleteResource.id).then(() => setDeleteResource(undefined)) }} type="button">Delete</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
  </section>
}

function ProjectResourceDialog({ onOpenChange, onSubmit, open, resource }: { onOpenChange: (open: boolean) => void; onSubmit: (input: { title?: string; url?: string }) => Promise<void>; open: boolean; resource?: ProjectResource }) {
  const [url, setUrl] = useState(resource?.url ?? '')
  const [title, setTitle] = useState(resource?.title ?? '')
  const [saving, setSaving] = useState(false)
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__form-dialog"><Dialog.Title>{resource ? 'Edit project link' : 'Add link to project'}</Dialog.Title><label>URL<input autoFocus onChange={event => setUrl(event.target.value)} placeholder="https://…" value={url}/></label><label>Title <small>(optional)</small><input onChange={event => setTitle(event.target.value)} value={title}/></label><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!url.trim() || saving} onClick={() => { setSaving(true); void onSubmit({ url: url.trim(), title: title.trim() }).catch(error => toast.error('Could not save link', { description: error instanceof Error ? error.message : undefined })).finally(() => setSaving(false)) }} type="button">{saving ? 'Saving…' : resource ? 'Save' : 'Add link'}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function StringInputDialog({ label, onOpenChange, onSubmit, open }: { label: string; onOpenChange: (open: boolean) => void; onSubmit: (value: string) => void; open: boolean }) {
  const [value, setValue] = useState('')
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay className="project-detail-page__dialog-overlay"/><Dialog.Content aria-describedby={undefined} className="project-detail-page__form-dialog project-detail-page__string-dialog"><Dialog.Title>{label.replace('…','')}</Dialog.Title><input autoFocus aria-label={label} onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && value.trim()) onSubmit(value.trim()) }} placeholder="Name" value={value}/><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!value.trim()} onClick={() => onSubmit(value.trim())} type="button">Add</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function ProjectMoreMenu({ initiatives, labels, labelGroups, project, projects, save }: { initiatives: Props['initiatives']; labels: Props['labels']; labelGroups: Props['labelGroups']; project: Props['project']; projects: Props['projects']; save: Props['save'] }) {
  const projectLabelIds = (project.labelIds ?? []).filter(id => labels.some(label => label.id === id))
  const groupNames = new Map(labelGroups.filter(group => group.resourceType === 'project').map(group => [group.id, group.name]))
  const toggleProjectLabel = (id: string) => { void save({ labelIds: toggleGroupedLabelIds(projectLabelIds, id, labels) }) }
  return <DropdownMenu.Root><DropdownMenu.Trigger asChild><button aria-label="More project properties" className="project-overview__more" type="button">•••</button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content align="start" className="project-detail-page__menu project-overview__more-menu" sideOffset={4}>
    <MultiSubmenu icon={<Flag size={14}/>} label="Initiatives" shortcut="P then N" options={initiatives.map(initiative => ({ id: initiative.id, label: initiative.name, color: initiative.color }))} selected={project.initiatives ?? []} onToggle={id => void save({ initiatives: toggleString(project.initiatives ?? [], id) })}/>
    <MultiSubmenu icon={<Link2 size={14}/>} label="Dependencies" options={projects.filter(item => item.id !== project.id).map(item => ({ id: item.id, label: item.name, color: item.color }))} selected={project.dependencyIds ?? []} onToggle={id => void save({ dependencyIds: toggleString(project.dependencyIds ?? [], id) })}/>
    <MultiSubmenu icon={<LabelIcon size={14}/>} label="Labels" shortcut="P then L" options={labels.map(label => ({ id: label.id, label: label.name, color: label.color, groupId: label.groupId, groupLabel: label.groupId ? groupNames.get(label.groupId) : undefined }))} selected={projectLabelIds} onToggle={toggleProjectLabel}/>
  </DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
}

function MultiSubmenu({ icon, label, onToggle, options, selected, shortcut }: { icon: ReactNode; label: string; onToggle: (id: string) => void; options: { id: string; label: string; color?: string; groupId?: string; groupLabel?: string }[]; selected: string[]; shortcut?: string }) {
  const sections = groupOptionSections(options)
  return <DropdownMenu.Sub><DropdownMenu.SubTrigger>{icon}<span>{label}</span>{shortcut && <kbd>{shortcut}</kbd>}<ChevronRight size={13}/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className="project-detail-page__menu project-overview__submenu" sideOffset={6}><DropdownMenu.Label>Change {label.toLowerCase()}…</DropdownMenu.Label>{options.length ? sections.map(section => <DropdownMenu.Group key={section.id}>{section.label && <DropdownMenu.Label data-i18n-ignore>{section.label}</DropdownMenu.Label>}{section.options.map(option => <DropdownMenu.CheckboxItem checked={selected.includes(option.id)} key={option.id} onCheckedChange={() => onToggle(option.id)} onSelect={event => event.preventDefault()}>{option.color && <i className="project-overview__option-dot" style={{ background: option.color }}/>}<span data-i18n-ignore>{option.label}</span>{selected.includes(option.id) && <Check size={13}/>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.Group>) : <DropdownMenu.Label>No options</DropdownMenu.Label>}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
}

function InitiativeSection({ initiatives, project, save }: { initiatives: Props['initiatives']; project: Props['project']; save: Props['save'] }) {
  const options = initiatives.map(initiative => ({ id: initiative.id, label: initiative.name, icon: <Flag size={13}/>, groupLabel: initiativeStatusLabel(initiative.status), i18nIgnore: true }))
  const selected = initiatives.filter(initiative => (project.initiatives ?? []).includes(initiative.id))
  return <section className="project-overview__row-section"><h3>Initiatives</h3><div className="project-overview__row-content project-overview__initiatives">
    {selected.map(initiative => <span className="project-overview__initiative" data-i18n-ignore key={initiative.id}><Flag size={13}/>{initiative.name}</span>)}
    <PropertyMenu compact hideSearch multiple label="Initiatives" value="Add initiative…" selectedIds={project.initiatives ?? []} options={options} icon={<Plus size={13}/>} searchPlaceholder="Change initiatives…" searchShortcut="P, then N" surfaceClassName="project-details-sidebar__property-menu is-members is-initiatives" onChange={id => void save({ initiatives: toggleString(project.initiatives ?? [], id) })}/>
  </div></section>
}

function ProjectLabelSection({ labels, labelGroups, project, save }: { labels: Props['labels']; labelGroups: Props['labelGroups']; project: Props['project']; save: Props['save'] }) {
  const selectedIds = (project.labelIds ?? []).filter(id => labels.some(label => label.id === id))
  const selected = labels.filter(label => selectedIds.includes(label.id))
  const groups = new Map(labelGroups.filter(group => group.resourceType === 'project').map(group => [group.id, group]))
  const toggle = (id: string) => { void save({ labelIds: toggleGroupedLabelIds(selectedIds, id, labels) }) }
  return <section className="project-overview__row-section"><h3>Labels</h3><div className="project-overview__row-content">{selected.map(label=><span className="project-overview__string-item" key={label.id}><i style={{background:label.color}}/><span data-i18n-ignore>{label.name}</span></span>)}<PropertyMenu compact multiple label="Labels" value="Add label…" selectedIds={selectedIds} options={labels.map(label=>({id:label.id,label:label.name,color:label.color,description:label.description,issueCount:label.issueCount,scope:label.scope,resourceType:label.resourceType,groupId:label.groupId,groupLabel:label.groupId?groups.get(label.groupId)?.name:undefined,groupColor:label.groupId?groups.get(label.groupId)?.color:undefined,i18nIgnore:true}))} icon={<Plus size={13}/>} searchPlaceholder="Change labels…" searchShortcut="P, then L" showGroupHeadings={false} surfaceClassName="project-details-sidebar__property-menu is-labels" onChange={toggle}/></div></section>
}

function DateProperty({ label, max, min, onChange, placeholder, resolution, value }: { label: 'Start date'|'Target date'; max?: string; min?: string; onChange: (value: string, resolution?: 'halfYear'|'month'|'quarter'|'year') => void; placeholder: string; resolution?: 'halfYear'|'month'|'quarter'|'year'; value?: string }) {
  const { formatDate, locale } = useI18n()
  const display = formatProjectPropertyDate(value, resolution, placeholder, locale, formatDate, 'short')
  return <ProjectDatePicker buttonClassName="project-overview__date" label={label} max={max} min={min} onChange={onChange} resolution={resolution} value={value}><CalendarIcon size={14} variant={label === 'Start date' ? 'start' : 'target'}/><span>{display}</span></ProjectDatePicker>
}

function ProjectEditableText({ ariaLabel, className, multiline, onCommit, placeholder, value }: { ariaLabel: string; className: string; multiline?: boolean; onCommit: (value: string) => Promise<void>; placeholder: string; value: string }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const commit = () => { const next = draft.trim(); if (next !== value) void onCommit(next) }
  if (multiline) return <textarea aria-label={ariaLabel} className={className} onBlur={commit} onChange={event => setDraft(event.target.value)} placeholder={placeholder} value={draft}/>
  return <input aria-label={ariaLabel} className={className} onBlur={commit} onChange={event => setDraft(event.target.value)} placeholder={placeholder} value={draft}/>
}

function ProjectDescriptionEditor({ value, onCommit }: { value: string; onCommit: (value: string) => Promise<void> }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  return <IssueDescriptionEditor ariaLabel="Project description" className="project-overview__description-editor" placeholder="Add description…" value={value} onChange={snapshot => setDraft(snapshot.markdown)} onBlur={() => { const next = draft.trim(); if (next !== value) void onCommit(next) }}/>
}

function uniqueById<T extends { id: string }>(items: T[]) { return [...new Map(items.map(item => [item.id, item])).values()] }
function toggleString(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
