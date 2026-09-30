import { useEffect, useMemo, useRef, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { AlignLeft, ArrowRight, Diamond, FileText, Flag, Link2, MoreHorizontal, Plus, Trash2, X, Send } from 'lucide-react'
import { format, formatDistanceToNowStrict } from 'date-fns'
import { toast } from 'sonner'
import { PropertyMenu } from '@/components/property/property-menu'
import { ProjectLeadPicker } from '@/components/projects-page/project-lead-picker'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { Avatar } from '@/components/issue/issue-row'
import { CalendarIcon, PriorityIcon, ProjectStatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { MilestoneProgressIcon } from '@/components/issue/milestone-progress-icon'
import { isMilestoneDateOverdue } from '@/components/issue/milestone-progress'
import { projectStatusOptionColor } from '@/lib/project-status-color'
import { ViewIconPicker } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { ProjectDatePicker } from '@/components/projects-page/project-target-date-picker'
import { DocumentGlyph } from '@/components/documents/document-icon'
import { useI18n } from '@/i18n/i18n'
import type { ProjectMutationInput } from '@/components/projects-page/projects-page'
import type { BootstrapData, CustomerRequest, FlowDocument, Issue, Project, ProjectResource, Team } from '@/types/flow'
import type { ProjectDetailProps } from './project-detail-types'
import { PRIORITY_LABELS } from './project-detail-types'
import { ProjectLabelControl } from '@/components/property/project-label-control'
import { DetailLabelControl } from '@/components/property/detail-label-control'
import { EmbeddedCustomerNeedForm } from '@/components/customer/embedded-customer-need-form'
import { formatProjectPropertyDate, initiativeStatusLabel, inviteProjectMember, projectMilestoneLink } from './project-detail-helpers'
import { ProjectPropertiesMenu } from './project-properties-menu'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { RichComment } from '@/components/activity/rich-comment'
import { ResourceExternalArrow, ResourceLinkIcon } from './project-resource-link'
import { resourceDisplayTitle } from './project-resource-link-name'
import { ProjectResourceMenu } from './project-resource-menu'
import { projectShortcutLabels, useProjectPickerOpen, type ProjectPickerRequest } from './project-detail-shortcuts'
import { ProjectMilestoneMenu } from './project-milestone-menu'
import { DescriptionHistoryDialog } from './project-header-menus'
import { DisclosureTriangle } from '@/components/ui/disclosure-triangle'

type Props = ProjectDetailProps & { projectIssues: Issue[]; save: (input: ProjectMutationInput) => Promise<void> }

export function ProjectOverview({ issueData, issueSummary, project, projects, projectRelations, integrationConnections, viewer, onUpdate, initiatives, documents, projectStatuses, projectUpdates, users, teams, labels, labelGroups, projectIssues, save, onCreateLabel, onCreateResource, onUpdateResource, onDeleteResource, onCreateMilestone, onUpdateMilestone, onDeleteMilestone, onMoveMilestone, onConvertMilestone, onOpenMilestoneIssues = () => onTabChange('issues'), onTabChange, pickerRequest, onPickerRequestHandled, onOpenDocumentHistory, onReloadWorkspace }: Props & { onOpenMilestoneIssues?: (milestoneId?: string) => void; pickerRequest?: ProjectPickerRequest; onPickerRequestHandled?: () => void }) {
  const statuses = useMemo(() => uniqueById(projectStatuses.length ? projectStatuses : projects.map(item => item.status)), [projectStatuses, projects])
  const members = users.filter(user => (project.memberIds ?? []).includes(user.id))
  const selectedMemberIds = [...new Set([...(project.memberIds ?? []), ...(project.lead?.id ? [project.lead.id] : [])])]
  const projectTeams = teams.filter(team => (project.teamIds ?? []).includes(team.id))
  const [creatingMilestone, setCreatingMilestone] = useState(false)
  const [customerDialogOpen, setCustomerDialogOpen] = useState(false)
  const customers = project.customers ?? []
  const customersEnabled = issueData?.workspaceSettings.featureFlags['customer-requests'] !== false
  // Linear shows the customers row only once the project has requests (or one is being added).
  const hasCustomerRequests = issueData?.customerRequests ? issueData.customerRequests.some(request => request.projectId === project.id) : customers.length > 0
  const selectedInitiatives = initiatives.filter(initiative => (project.initiatives ?? []).includes(initiative.id))
  const selectedLabelIds = (project.labelIds ?? []).filter(id => labels.some(label => label.id === id))
  const outline = useMemo(() => projectOutline(project.description, project.milestones ?? []), [project.description, project.milestones])
  const { t } = useI18n()
  const shortcuts = projectShortcutLabels()
  const [statusOpen, setStatusOpen] = useProjectPickerOpen('status', pickerRequest, onPickerRequestHandled)
  const [priorityOpen, setPriorityOpen] = useProjectPickerOpen('priority', pickerRequest, onPickerRequestHandled)
  const [leadOpen, setLeadOpen] = useProjectPickerOpen('lead', pickerRequest, onPickerRequestHandled)
  const [membersOpen, setMembersOpen] = useProjectPickerOpen('members', pickerRequest, onPickerRequestHandled)
  const [startDateOpen, setStartDateOpen] = useProjectPickerOpen('startDate', pickerRequest, onPickerRequestHandled)
  const [targetDateOpen, setTargetDateOpen] = useProjectPickerOpen('targetDate', pickerRequest, onPickerRequestHandled)
  const [labelsOpen, setLabelsOpen] = useProjectPickerOpen('labels', pickerRequest, onPickerRequestHandled)
  const teamNames = projectTeams.map(team => team.name).join(', ')

  return <TooltipProvider delayDuration={450} skipDelayDuration={300}><div className="project-overview">
    {outline.length > 1 && <ProjectOutlineRail items={outline}/>}
    <section className="project-overview__intro">
      <ViewIconPicker color={project.color} icon={normalizeProjectIcon(project.icon)} onChange={visual => void save(visual)} triggerClassName="project-overview__icon"/>
      <ProjectEditableText ariaLabel="Project name" className="project-overview__name" placeholder="Project name" value={project.name} onCommit={name => save({ name })}/>
      <ProjectEditableText ariaLabel="Project summary" className="project-overview__summary" placeholder="Add a short summary…" value={project.summary} onCommit={summary => save({ summary })}/>
      <div className="project-overview__property-section">
        <h3>Properties</h3>
        <div className="project-overview__properties">
          <PropertyMenu compact label="Status" value={project.status.name} selectedId={project.status.id} icon={<ProjectStatusIcon color={project.status.color} name={project.status.name} size={16} type={project.status.type}/>} options={statuses.map((status, index) => ({ id: status.id, label: status.name, color: projectStatusOptionColor(status, project.status), icon: <ProjectStatusIcon color={projectStatusOptionColor(status, project.status)} name={status.name} size={14} type={status.type}/>, shortcut: String(index + 1) }))} searchPlaceholder="Change status…" searchShortcut="P, then S" surfaceClassName="project-details-sidebar__property-menu is-standard" open={statusOpen} onOpenChange={setStatusOpen} tooltip={t('Change project status')} tooltipShortcut={shortcuts.status} onChange={statusId => void save({ statusId })}/>
          <PropertyMenu compact label="Priority" value={project.priorityLabel} selectedId={String(project.priority)} icon={<PriorityIcon priority={project.priority} size={16}/>} options={[0,1,2,3,4].map(priority => ({ id: String(priority), label: PRIORITY_LABELS[priority], icon: <PriorityIcon priority={priority} size={14}/>, shortcut: String(priority) }))} searchPlaceholder="Change priority…" searchShortcut="P, then P" surfaceClassName="project-details-sidebar__property-menu is-standard" open={priorityOpen} onOpenChange={setPriorityOpen} tooltip={t('Change project priority')} tooltipShortcut={shortcuts.priority} onChange={priority => void save({ priority: Number(priority) })}/>
          <ProjectLeadPicker value={project.lead} users={users} teamIds={project.teamIds} onChange={leadId => save({ leadId })} onInvite={inviteProjectMember} open={leadOpen} onOpenChange={setLeadOpen} tooltip={t('Set project lead')} tooltipShortcut={shortcuts.lead}/>
          {members.length > 0 && <PropertyMenu compact multiple label="Members" value={members.length === 1 ? members[0].displayName : `${members.length} members`} valueIsEntityName={members.length === 1} selectedIds={selectedMemberIds} icon={<Avatar name={members[0].displayName}/>} options={[...users.map(user => ({ id: user.id, label: user.displayName, icon: <Avatar name={user.displayName}/>, end: project.lead?.id === user.id ? 'Project lead' : user.active ? undefined : 'Invited', i18nIgnore: true })), { id: '__invite-project-member__', label: 'Invite and add…', icon: <Send size={14}/>, groupLabel: 'New user', action: true }]} hideSearch searchPlaceholder="Change members…" searchShortcut="P, then M" surfaceClassName="project-details-sidebar__property-menu is-members" open={membersOpen} onOpenChange={setMembersOpen} tooltip={t('Change project members')} tooltipShortcut={shortcuts.members} onChange={memberId => { if (memberId === '__invite-project-member__') { inviteProjectMember(); return } if (memberId === project.lead?.id) return; void save({ memberIds: (project.memberIds ?? []).includes(memberId) ? project.memberIds.filter(id => id !== memberId) : [...(project.memberIds ?? []), memberId] }) }}/>}
          {project.startDate && <><DateProperty label="Start date" max={project.targetDate} open={startDateOpen} onOpenChange={setStartDateOpen} placeholder="Start date" resolution={project.startDateResolution} tooltip="Start date" tooltipShortcut={shortcuts.startDate} value={project.startDate} onChange={(startDate, startDateResolution) => void save({ startDate, startDateResolution: startDateResolution ?? '' })}/><ArrowRight aria-hidden="true" className="project-overview__date-arrow" size={16} strokeWidth={1.5}/></>}
          <DateProperty label="Target date" min={project.startDate} open={targetDateOpen} onOpenChange={setTargetDateOpen} placeholder="Target date" resolution={project.targetDateResolution} tooltip={project.targetDate ? 'Change target date' : 'Add target date'} tooltipShortcut={shortcuts.targetDate} value={project.targetDate} onChange={(targetDate, targetDateResolution) => void save({ targetDate, targetDateResolution: targetDateResolution ?? '' })}/>
          <FlowTooltip label={teamNames || undefined}><button aria-disabled="true" className="project-overview__team" data-i18n-ignore={projectTeams.length ? true : undefined} onClick={event => event.preventDefault()} type="button"><TeamIcon team={projectTeams[0]} size={16}/>{teamNames || 'Team'}</button></FlowTooltip>
          <ProjectPropertiesMenu featureFlags={issueData?.workspaceSettings.featureFlags} integrationConnections={integrationConnections} initiatives={initiatives} labelGroups={labelGroups} labels={labels} onCreateLabel={onCreateLabel} project={project} projectRelations={projectRelations} projects={projects} users={users} viewer={viewer} save={save} onUpdateProject={onUpdate} onAddCustomer={customersEnabled && !customers.length ? () => setCustomerDialogOpen(true) : undefined}/>
        </div>
      </div>
    </section>

    {issueData?.workspaceSettings.featureFlags.initiatives !== false && selectedInitiatives.length > 0 && <InitiativeSection initiatives={initiatives} project={project} save={save}/>}
    {selectedLabelIds.length > 0 && <ProjectLabelSection labels={labels} labelGroups={labelGroups} project={project} save={save} onCreateLabel={onCreateLabel} open={labelsOpen} onOpenChange={setLabelsOpen}/>}
    <ResourceSection data={issueData} documents={documents} onOpenDocumentHistory={onOpenDocumentHistory} onReload={onReloadWorkspace} project={project} projects={projects} users={users} onCreate={input => onCreateResource(project.id, input)} onDelete={resourceId => onDeleteResource(project.id, resourceId)} onUpdate={(resourceId, input) => onUpdateResource(project.id, resourceId, input)} resources={project.resources ?? []} teams={teams}/>
    {customersEnabled && (hasCustomerRequests || customerDialogOpen) && <ProjectCustomerNeedsSection issueData={issueData} project={project} save={save} adding={customerDialogOpen} onAddingChange={setCustomerDialogOpen}/>}

    <section className={`project-overview__latest${projectUpdates[0] ? '' : ' is-empty'}`}>
      {projectUpdates[0] ? <div aria-label="Open latest project update" className="project-overview__latest-update" onClick={event => { if (!(event.target as HTMLElement).closest('a')) onTabChange('activity') }} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onTabChange('activity') } }} role="button" tabIndex={0}><span className={`project-overview__health is-${projectUpdates[0].health}`}/><div><strong data-i18n-ignore>{projectUpdates[0].user.displayName}</strong><time>{formatDistanceToNowStrict(new Date(projectUpdates[0].createdAt), { addSuffix: true })}</time><div className="project-overview__latest-update-body" data-i18n-ignore><RichComment body={projectUpdates[0].body}/></div></div></div> : <button className="project-overview__first-update" onClick={() => onTabChange('activity')} type="button"><FileText size={14}/>Write first project update</button>}
    </section>

    <section className="project-overview__description" id="project-overview-description">
      <h3>Description</h3>
      <ProjectDescriptionEditor users={users} value={project.description} onCommit={description => save({ description })}/>
    </section>

    <section className="project-overview__milestones" id="project-overview-milestones">
      {(project.milestones?.length ?? 0) > 0 && <h3>Milestones</h3>}
      <AnimatedMilestones items={project.milestones ?? []}>{milestone => <OverviewMilestone totals={issueSummary ? issueSummary.milestones[milestone.id] ?? {total:0, completed:0} : undefined} issues={projectIssues.filter(issue => issue.projectMilestoneId === milestone.id)} milestone={milestone} onConvert={async () => { await onConvertMilestone(project.id, milestone.id); toast.success('Milestone converted to project') }} onDelete={() => onDeleteMilestone(project.id, milestone.id)} onMove={async targetProjectId => { await onMoveMilestone(project.id, milestone.id, targetProjectId); toast.success('Milestone moved') }} onOpenIssues={() => onOpenMilestoneIssues(milestone.id)} onUpdate={input => onUpdateMilestone(project.id, milestone.id, input)} projects={projects.filter(item => item.id !== project.id && !item.archivedAt)}/>}</AnimatedMilestones>
      {creatingMilestone && <OverviewMilestoneCreator
        onCancel={() => setCreatingMilestone(false)}
        onCreate={async input => { await onCreateMilestone(project.id, input); setCreatingMilestone(false) }}
      />}
      {!creatingMilestone && <button className="project-overview__milestone-link" type="button" onClick={() => setCreatingMilestone(true)}><Plus size={16}/>Milestone</button>}
    </section>
  </div></TooltipProvider>
}

function OverviewMilestone({ totals, issues, milestone, onConvert, onDelete, onMove, onOpenIssues, onUpdate, projects }: { totals?: { total: number; completed: number }; issues: Issue[]; milestone: Props['project']['milestones'][number]; onConvert: () => Promise<void>; onDelete: () => Promise<void>; onMove: (targetProjectId: string) => Promise<void>; onOpenIssues: () => void; onUpdate: (input: { name?: string; description?: string; targetDate?: string }) => Promise<unknown>; projects: Project[] }) {
  const { formatDate, locale } = useI18n()
  const [expanded, setExpanded] = useState(false)
  const [editingName, setEditingName] = useState(false)
  const count = totals?.total ?? issues.length
  const completed = totals?.completed ?? issues.filter(issue => issue.state.type === 'completed').length
  const progress = count ? Math.round(completed / count * 100) : 0
  const [historyOpen, setHistoryOpen] = useState(false)
  const link = projectMilestoneLink(milestone.id)
  return <article className="project-overview__milestone" data-expanded={expanded} id={`milestone-${milestone.id}`}>
    <header>
      <span className="project-overview__milestone-mark"><MilestoneProgressIcon className="project-overview__milestone-progress" overdue={isMilestoneDateOverdue(milestone.targetDate)} progress={progress}/></span>
      {editingName
        ? <ProjectEditableText autoFocus ariaLabel="Milestone name" className="project-overview__milestone-name" placeholder="Milestone name" value={milestone.name} onCommit={name => onUpdate({ name }).then(() => undefined)} onDone={() => setEditingName(false)}/>
        : <button aria-label={`Rename ${milestone.name}`} className="project-overview__milestone-title" data-i18n-ignore onClick={() => setEditingName(true)} type="button">{milestone.name}</button>}
      <button aria-expanded={expanded} aria-label={expanded ? 'Collapse' : 'Expand'} className="project-overview__milestone-collapse" onClick={() => setExpanded(value => !value)} type="button"><DisclosureTriangle open={expanded}/></button>
      <span className="project-overview__milestone-spacer"/>
      <ProjectDatePicker buttonClassName={milestone.targetDate ? 'project-overview__milestone-date' : 'project-overview__milestone-date is-unset'} label="Target date" onChange={targetDate => void onUpdate({ targetDate })} value={milestone.targetDate}><span>{milestone.targetDate ? locale === 'en-US' ? format(new Date(`${milestone.targetDate}T00:00:00`), 'MMM d') : formatDate(`${milestone.targetDate}T00:00:00`, { month: 'short', day: 'numeric' }) : <span className="project-overview__milestone-date-placeholder">Set target date</span>}</span></ProjectDatePicker>
      {milestone.targetDate && <span aria-hidden="true" className="project-overview__milestone-dot">·</span>}
      <a aria-label="Open issues" className="project-overview__milestone-issues" href={link} onClick={event => { event.preventDefault(); onOpenIssues() }}>{count} {count === 1 ? 'issue' : 'issues'}<span>·</span>{progress}%</a>
      <ProjectMilestoneMenu count={count} milestone={milestone} onConvert={onConvert} onDelete={onDelete} onEdit={() => { setExpanded(true); requestAnimationFrame(() => setEditingName(true)) }} onMove={onMove} onShowHistory={() => setHistoryOpen(true)} projects={projects} trigger={<button aria-label="Open menu" className="project-overview__milestone-menu-trigger" type="button"><MoreHorizontal size={12}/></button>} variant="overview"/>
    </header>
    {expanded && <ProjectEditableText
      ariaLabel="Milestone description"
      className="project-overview__milestone-description"
      multiline
      placeholder="Add milestone description…"
      value={milestone.description ?? ''}
      onCommit={description => onUpdate({ description }).then(() => undefined)}
    />}
    <DescriptionHistoryDialog onOpenChange={setHistoryOpen} open={historyOpen} revisions={[]}/>
  </article>
}



function OverviewMilestoneCreator({ onCancel, onCreate }: { onCancel: () => void; onCreate: (input: { name: string; description?: string; targetDate?: string }) => Promise<void> }) {
  const { formatDate, locale } = useI18n()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [targetDate, setTargetDate] = useState('')
  const [saving, setSaving] = useState(false)
  const submit = () => { if (!name.trim() || saving) return; setSaving(true); void onCreate({ name: name.trim(), description: description.trim(), targetDate }).finally(() => setSaving(false)) }
  return <form className="project-overview__milestone project-overview__milestone-creator" onSubmit={event => { event.preventDefault(); submit() }}>
    <header><span className="project-overview__milestone-mark"><MilestoneProgressIcon className="project-overview__milestone-progress" empty/></span><input autoFocus aria-label="Milestone name" className="project-overview__milestone-name" disabled={saving} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') onCancel() }} placeholder="Milestone name" value={name}/><span className="project-overview__milestone-spacer"/><ProjectDatePicker buttonClassName="project-overview__milestone-date" label="Target date" onChange={setTargetDate} value={targetDate}><span>{targetDate ? locale === 'en-US' ? format(new Date(`${targetDate}T00:00:00`), 'MMM d') : formatDate(`${targetDate}T00:00:00`, { month: 'short', day: 'numeric' }) : <span className="project-overview__milestone-date-placeholder">Set target date</span>}</span></ProjectDatePicker><button aria-label="Cancel" className="project-overview__milestone-menu-trigger" onClick={onCancel} type="button"><X size={12}/></button></header>
    <textarea aria-label="Milestone description" className="project-overview__milestone-description" disabled={saving} onChange={event => setDescription(event.target.value)} placeholder="Add milestone description…" value={description}/>
  </form>
}

function InlineStringSection({ addLabel, items, onChange, onOpenChange: setOpen, open, title }: { addLabel: string; items: string[]; onChange: (items: string[]) => void; onOpenChange: (open: boolean) => void; open: boolean; title: string }) {
  return <section className="project-overview__row-section"><h3>{title}</h3><div className="project-overview__row-content">
    {items.map(item => <span className="project-overview__string-item" key={item}><span>{item}</span><button aria-label={`Remove ${item}`} onClick={() => onChange(items.filter(value => value !== item))} type="button"><Trash2 size={11}/></button></span>)}
    <button className="project-overview__inline-add" onClick={() => setOpen(true)} type="button"><Plus size={13}/>{addLabel}</button>
  </div><StringInputDialog label={addLabel} onOpenChange={setOpen} open={open} onSubmit={value => { onChange([...items, value]); setOpen(false) }}/></section>
}

function ResourceSection({ data, documents, onCreate, onDelete, onOpenDocumentHistory, onReload, onUpdate, project, projects, resources, teams, users }: { data?: BootstrapData; project?: Project; projects?: Project[]; onOpenDocumentHistory?: (document: FlowDocument) => void; onReload?: () => Promise<void>; documents: Props['documents']; resources: ProjectResource[]; teams: Team[]; users: Props['users']; onCreate: (input: { type?: 'link'|'document'; title?: string; url?: string }) => Promise<ProjectResource>; onDelete: (id: string) => Promise<void>; onUpdate: (id: string, input: { type?: 'link'|'document'; title?: string; url?: string; pinnedTeamIds?: string[] }) => Promise<ProjectResource> }) {
  const [menuOpen, setMenuOpen] = useState(false)
  const [dialog, setDialog] = useState<{ mode: 'create'|'edit'; resource?: ProjectResource }>()
  const [deleteResource, setDeleteResource] = useState<ProjectResource>()
  const { t } = useI18n()
  const createDocument = async () => {
    await onCreate({ type: 'document', title: 'Untitled document' })
    setMenuOpen(false)
  }
  return <section className="project-overview__row-section project-overview__resources"><h3>Resources</h3><div className="project-overview__row-content">
    {resources.map(resource => { const document=resource.type==='document'?documents.find(item=>item.id===resource.id):undefined; const creator = document ? document.creator?.displayName : users.find(user => user.id === resource.creatorId)?.displayName; const age = resourceAge(document?.updatedAt ?? resource.createdAt); const title = resourceDisplayTitle(resource); const link = resource.type === 'link'; return <div className="project-overview__resource" data-kind={resource.type} key={resource.id}><FlowTooltip align="start" contentClassName="flow-tooltip-content--title" label={<span className="project-resource-tip"><strong data-i18n-ignore>{title}</strong>{link ? <span data-i18n-ignore>{resource.url}</span> : (creator || age) && <span data-i18n-ignore>{[creator, age].filter(Boolean).join(' · ')}</span>}</span>}><a data-i18n-ignore href={resource.url} rel={link ? 'noreferrer' : undefined} target={link ? '_blank' : undefined}>{document?<DocumentGlyph document={document}/>:link ? <ResourceLinkIcon url={resource.url}/> : <FileText size={16}/>}<span className="project-resource-title">{title}</span>{link && <ResourceExternalArrow/>}</a></FlowTooltip><ProjectResourceMenu data={data} document={document} onDeleteLink={() => setDeleteResource(resource)} onEditLink={() => setDialog({ mode: 'edit', resource })} onOpenDocumentHistory={onOpenDocumentHistory} onReload={onReload} onUpdate={onUpdate} project={project} projects={projects} resource={resource} teams={teams}/></div>})}
    <DropdownMenu.Root onOpenChange={setMenuOpen} open={menuOpen}><FlowTooltip disabled={menuOpen} label={t('Add document or link')}><DropdownMenu.Trigger asChild>{resources.length
      ? <button aria-label="Add document or link…" className="project-overview__inline-add project-resource-add is-icon" type="button"><Plus size={16}/></button>
      : <button className="project-overview__inline-add project-resource-add" type="button"><Plus size={16}/>Add document or link…</button>}</DropdownMenu.Trigger></FlowTooltip><DropdownMenu.Portal><DropdownMenu.Content data-flow-motion="floating" align="start" className="project-detail-page__menu project-overview__resource-menu" sideOffset={4}><DropdownMenu.Label className="sr-only">Add document or link…</DropdownMenu.Label><DropdownMenu.Item onSelect={() => void createDocument()}><FileText size={16}/><span>Create new document…</span></DropdownMenu.Item><DropdownMenu.Item onSelect={() => setDialog({ mode: 'create' })}><Link2 size={16}/><span>Add a link…</span><kbd>Ctrl L</kbd></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>
  </div><ProjectResourceDialog key={dialog?.resource?.id ?? dialog?.mode ?? 'closed'} onOpenChange={open => { if (!open) setDialog(undefined) }} open={Boolean(dialog)} resource={dialog?.resource} onSubmit={async input => { if (dialog?.resource) await onUpdate(dialog.resource.id, input); else await onCreate({ type: 'link', url: input.url!, title: input.title }); setDialog(undefined) }}/>
  <Dialog.Root onOpenChange={open => { if (!open) setDeleteResource(undefined) }} open={Boolean(deleteResource)}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby="project-resource-delete-description" className="project-detail-page__form-dialog"><Dialog.Title>{`Delete “${deleteResource?.title ?? ''}”?`}</Dialog.Title><Dialog.Description id="project-resource-delete-description">This resource will be removed from the project.</Dialog.Description><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-danger" onClick={() => { if (!deleteResource) return; void onDelete(deleteResource.id).then(() => setDeleteResource(undefined)) }} type="button">Delete</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
  </section>
}

function ProjectResourceDialog({ onOpenChange, onSubmit, open, resource }: { onOpenChange: (open: boolean) => void; onSubmit: (input: { title?: string; url?: string }) => Promise<void>; open: boolean; resource?: ProjectResource }) {
  const [url, setUrl] = useState(resource?.url ?? '')
  const [title, setTitle] = useState(resource?.title ?? '')
  const [saving, setSaving] = useState(false)
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="project-detail-page__form-dialog"><Dialog.Title>{resource ? 'Edit project link' : 'Add link to project'}</Dialog.Title><label>URL<input autoFocus onChange={event => setUrl(event.target.value)} placeholder="https://…" value={url}/></label><label>Title <small>(optional)</small><input onChange={event => setTitle(event.target.value)} value={title}/></label><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!url.trim() || saving} onClick={() => { setSaving(true); void onSubmit({ url: url.trim(), title: title.trim() }).catch(error => toast.error('Could not save link', { description: error instanceof Error ? error.message : undefined })).finally(() => setSaving(false)) }} type="button">{saving ? 'Saving…' : resource ? 'Save' : 'Add link'}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function StringInputDialog({ label, onOpenChange, onSubmit, open }: { label: string; onOpenChange: (open: boolean) => void; onSubmit: (value: string) => void; open: boolean }) {
  const [value, setValue] = useState('')
  return <Dialog.Root onOpenChange={onOpenChange} open={open}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="project-detail-page__dialog-overlay"/><Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="project-detail-page__form-dialog project-detail-page__string-dialog"><Dialog.Title>{label.replace('…','')}</Dialog.Title><input autoFocus aria-label={label} onChange={event => setValue(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && value.trim()) onSubmit(value.trim()) }} placeholder="Name" value={value}/><footer><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button className="is-primary" disabled={!value.trim()} onClick={() => onSubmit(value.trim())} type="button">Add</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>
}

function InitiativeSection({ initiatives, project, save }: { initiatives: Props['initiatives']; project: Props['project']; save: Props['save'] }) {
  const options = initiatives.map(initiative => ({ id: initiative.id, label: initiative.name, icon: <Flag size={13}/>, groupLabel: initiativeStatusLabel(initiative.status), i18nIgnore: true }))
  const selected = initiatives.filter(initiative => (project.initiatives ?? []).includes(initiative.id))
  return <section className="project-overview__row-section"><h3>Initiatives</h3><div className="project-overview__row-content project-overview__initiatives">
    {selected.map(initiative => <span className="project-overview__initiative" data-i18n-ignore key={initiative.id}><Flag size={13}/>{initiative.name}</span>)}
    <PropertyMenu compact multiple label="Initiatives" value="Add initiative…" emptyLabel="No matching initiatives" selectedIds={project.initiatives ?? []} options={options} icon={<Plus size={13}/>} searchPlaceholder="Change initiatives…" searchShortcut="P, then N" surfaceClassName="project-details-sidebar__property-menu is-members is-initiatives" onChange={id => void save({ initiatives: toggleString(project.initiatives ?? [], id) })}/>
  </div></section>
}

function ProjectLabelSection({ labels, labelGroups, project, save, onCreateLabel, open, onOpenChange }: { labels: Props['labels']; labelGroups: Props['labelGroups']; project: Props['project']; save: Props['save']; onCreateLabel: Props['onCreateLabel']; open?: boolean; onOpenChange?: (open: boolean) => void }) {
  const { t } = useI18n()
  return <section className="project-overview__row-section"><h3>Labels</h3><div className="project-overview__row-content"><DetailLabelControl changeLabelAction="changeLabelAction" host="project" isArchived={Boolean(project.archivedAt)} isReadOnly={Boolean(project.archivedAt)}><ProjectLabelControl addTooltip={t('Add labels')} addTooltipShortcut={projectShortcutLabels().labels} labels={labels} labelGroups={labelGroups} open={open} onOpenChange={onOpenChange} selectedIds={project.labelIds ?? []} onChange={labelIds => void save({ labelIds })} onCreateLabel={onCreateLabel}/></DetailLabelControl></div></section>
}

function DateProperty({ label, max, min, onChange, onOpenChange, open, placeholder, resolution, tooltip, tooltipShortcut, value }: { label: 'Start date'|'Target date'; max?: string; min?: string; onChange: (value: string, resolution?: 'halfYear'|'month'|'quarter'|'year') => void; onOpenChange?: (open: boolean) => void; open?: boolean; placeholder: string; resolution?: 'halfYear'|'month'|'quarter'|'year'; tooltip?: string; tooltipShortcut?: string; value?: string }) {
  const { formatDate, locale } = useI18n()
  const display = formatProjectPropertyDate(value, resolution, placeholder, locale, formatDate, 'short')
  return <ProjectDatePicker buttonClassName="project-overview__date" label={label} max={max} min={min} onChange={onChange} onOpenChange={onOpenChange} open={open} resolution={resolution} tooltip={tooltip} tooltipShortcut={tooltipShortcut} value={value}><CalendarIcon size={16} variant={label === 'Start date' ? 'start' : 'target'}/><span>{display}</span></ProjectDatePicker>
}

function ProjectEditableText({ ariaLabel, autoFocus, className, multiline, onCommit, onDone, placeholder, value }: { ariaLabel: string; autoFocus?: boolean; className: string; multiline?: boolean; onCommit: (value: string) => Promise<void>; onDone?: () => void; placeholder: string; value: string }) {
  const [draft, setDraft] = useState(value)
  const cancelled = useRef(false)
  useEffect(() => setDraft(value), [value])
  const commit = () => { const next = draft.trim(); const skip = cancelled.current || (onDone && !next); cancelled.current = false; if (!skip && next !== value) void onCommit(next); onDone?.() }
  if (multiline) return <textarea aria-label={ariaLabel} autoFocus={autoFocus} className={className} onBlur={commit} onChange={event => setDraft(event.target.value)} placeholder={placeholder} value={draft}/>
  return <input aria-label={ariaLabel} autoFocus={autoFocus} className={className} onBlur={commit} onChange={event => setDraft(event.target.value)} onKeyDown={onDone ? event => { if (event.key === 'Enter') event.currentTarget.blur(); if (event.key === 'Escape') { cancelled.current = true; setDraft(value); event.currentTarget.blur() } } : undefined} placeholder={placeholder} value={draft}/>
}

function ProjectDescriptionEditor({ users, value, onCommit }: { users: Props['users']; value: string; onCommit: (value: string) => Promise<void> }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  return <IssueDescriptionEditor users={users} ariaLabel="Project description" className="project-overview__description-editor" placeholder="Add description…" value={value} onChange={snapshot => setDraft(snapshot.markdown)} onBlur={() => { const next = draft.trim(); if (next !== value) void onCommit(next) }}/>
}

type OutlineItem = { key: string; label: string; level: 0 | 1; target: () => Element | null; userText?: boolean }

function projectOutline(description: string, milestones: Array<{ id: string; name: string }>): OutlineItem[] {
  const headings: string[] = []
  let fenced = false
  for (const line of (description ?? '').split('\n')) {
    if (/^\s*(```|~~~)/.test(line)) { fenced = !fenced; continue }
    const match = fenced ? null : /^(#{1,3})\s+(.+?)\s*#*\s*$/.exec(line)
    if (match) headings.push(match[2].replace(/[*_`~]/g, '').trim())
  }
  // Descriptions stored as HTML list their H1–H3 elements too.
  if (!headings.length && /<h[1-3][\s>]/i.test(description ?? '')) {
    for (const match of (description ?? '').matchAll(/<h([1-3])[^>]*>([\s\S]*?)<\/h\1>/gi)) {
      const text = match[2].replace(/<[^>]+>/g, '').replace(/&nbsp;/g, ' ').replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').trim()
      if (text) headings.push(text)
    }
  }
  const descriptionSection = () => document.getElementById('project-overview-description')
  return [
    { key: 'description', label: 'Description', level: 0, target: descriptionSection },
    ...headings.filter(Boolean).map((label, index) => ({ key: `heading-${index}`, label, level: 1 as const, userText: true, target: () => descriptionSection()?.querySelectorAll('h1,h2,h3')[index] ?? null })),
    ...(milestones.length ? [{ key: 'milestones', label: 'Milestones', level: 0 as const, target: () => document.getElementById('project-overview-milestones') }] : []),
    ...milestones.map(milestone => ({ key: `milestone-${milestone.id}`, label: milestone.name, level: 1 as const, userText: true, target: () => document.getElementById(`milestone-${milestone.id}`) })),
  ]
}

function ProjectOutlineRail({ items }: { items: OutlineItem[] }) {
  const sections = useMemo(() => items.filter(item => item.level === 0), [items])
  const [activeSection, setActiveSection] = useState(sections[0]?.key ?? '')
  useEffect(() => {
    let frame = 0
    const measure = () => {
      frame = 0
      // The section in view is the last top-level section whose start has scrolled into the upper third of
      // the viewport; before any has, the first section (Description) is current.
      let next = sections[0]?.key ?? ''
      for (const section of sections) {
        const top = section.target()?.getBoundingClientRect().top
        if (top !== undefined && top <= window.innerHeight / 3) next = section.key
      }
      setActiveSection(current => current === next ? current : next)
    }
    const schedule = () => { if (!frame) frame = requestAnimationFrame(measure) }
    measure()
    window.addEventListener('scroll', schedule, true)
    window.addEventListener('resize', schedule)
    return () => {
      if (frame) cancelAnimationFrame(frame)
      window.removeEventListener('scroll', schedule, true)
      window.removeEventListener('resize', schedule)
    }
  }, [sections])
  const sectionOf = (index: number) => {
    for (let cursor = index; cursor >= 0; cursor--) if (items[cursor].level === 0) return items[cursor].key
    return ''
  }
  return <nav aria-label="Document outline" className="project-overview__outline">
    <div className="project-overview__outline-anchor">
      <div aria-hidden="true" className="project-overview__outline-rail">
        {items.map((item, index) => <span className="project-overview__outline-bar" data-active={item.level === 0 && item.key === activeSection || undefined} data-level={item.level} data-section-active={sectionOf(index) === activeSection || undefined} key={item.key}/>)}
      </div>
      <div className="project-overview__outline-card">
        {items.map((item, index) => <button aria-current={item.level === 0 && item.key === activeSection ? 'location' : undefined} aria-label={item.label} className="project-overview__outline-item" data-level={item.level} data-section-active={sectionOf(index) === activeSection || undefined} key={item.key} onClick={() => item.target()?.scrollIntoView({ behavior: 'smooth', block: 'start' })} type="button">
          {item.level === 0 && (item.key === 'milestones' ? <Diamond aria-hidden="true" size={16}/> : <AlignLeft aria-hidden="true" size={16}/>)}
          <span className="project-overview__outline-label" data-i18n-ignore={item.userText || undefined}>{item.label}</span>
        </button>)}
      </div>
    </div>
  </nav>
}

function uniqueById<T extends { id: string }>(items: T[]) { return [...new Map(items.map(item => [item.id, item])).values()] }
function toggleString(values: string[], value: string) { return values.includes(value) ? values.filter(item => item !== value) : [...values, value] }
import { AnimatedMilestones } from '@/components/ui/motion';


function ProjectCustomerNeedsSection({ issueData, project, save, adding: controlledAdding, onAddingChange }: { issueData?: BootstrapData; project: Project; save: (input: ProjectMutationInput) => Promise<void>; adding?: boolean; onAddingChange?: (adding: boolean) => void }) {
  const [internalAdding, setInternalAdding] = useState(false)
  const adding = controlledAdding ?? internalAdding
  const setAdding = onAddingChange ?? setInternalAdding
  const [showArchived, setShowArchived] = useState(false)
  const [localRequests, setLocalRequests] = useState<CustomerRequest[]>([])
  if (!issueData?.customerRequests) {
    return <InlineStringSection addLabel="Add customer request" items={project.customers ?? []} onChange={customers => void save({ customers })} onOpenChange={setAdding} open={adding} title="Customers"/>
  }
  const merged = [...localRequests, ...issueData.customerRequests.filter(item => item.projectId === project.id)]
  const seen = new Set<string>()
  const requests = merged.filter(item => {
    if (seen.has(item.id)) return false
    seen.add(item.id)
    return true
  })
  const archivedCount = requests.filter(item => item.archivedAt).length
  const visible = requests.filter(item => showArchived || !item.archivedAt)
  return (
    <section className="project-overview__row-section">
      <h3>Customer requests</h3>
      <div className="project-overview__row-content project-customer-needs">
        {visible.map(request => {
          const customer = issueData.customers.find(item => item.id === request.customerId)
          return (
            <span className={`project-overview__string-item${request.archivedAt ? ' is-archived' : ''}`} key={request.id}>
              <span>{customer?.name ?? 'Customer'}: {request.body}</span>
              {request.priority ? <em>Important</em> : null}
            </span>
          )
        })}
        {(project.customers ?? []).map(item => (
          <span className="project-overview__string-item" key={`name:${item}`}>
            <span>{item}</span>
            <button aria-label={`Remove ${item}`} onClick={() => void save({ customers: (project.customers ?? []).filter(value => value !== item) })} type="button"><Trash2 size={11}/></button>
          </span>
        ))}
        <button className="project-overview__inline-add" onClick={() => setAdding(true)} type="button"><Plus size={13}/>Add customer request</button>
        {archivedCount > 0 && (
          <button className="project-overview__inline-add" type="button" onClick={() => setShowArchived(value => !value)}>
            {showArchived ? 'Hide archived' : `Show archived (${archivedCount})`}
          </button>
        )}
      </div>
      {adding && (
        <EmbeddedCustomerNeedForm
          data={issueData}
          host="projectPage"
          projectId={project.id}
          onCancel={() => setAdding(false)}
          onCreated={async (request) => {
            setLocalRequests(current => [request, ...current])
            const customer = issueData.customers.find(item => item.id === request.customerId)
            if (customer && !(project.customers ?? []).includes(customer.name)) {
              await save({ customers: [...(project.customers ?? []), customer.name] })
            }
            setAdding(false)
          }}
        />
      )}
    </section>
  )
}

/** Linear's compact resource age: "3min", "5h", "2d", "4mo", "1y". */
function resourceAge(value: string | undefined, now = Date.now()) {
  const time = value ? Date.parse(value) : Number.NaN
  if (!Number.isFinite(time)) return ''
  const minutes = Math.max(1, Math.floor((now - time) / 60_000))
  if (minutes < 60) return `${minutes}min`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days}d`
  return days < 365 ? `${Math.floor(days / 30)}mo` : `${Math.floor(days / 365)}y`
}
