import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type ReactNode, type RefObject } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import type { Editor } from '@tiptap/react'
import { ChevronRight, CircleDashed, Diamond, ExternalLink, FilePlus2, Link2, Maximize2, Minimize2, MoreHorizontal, Paperclip, Repeat2, Trash2, X } from 'lucide-react'
import type { BootstrapData, Draft, Issue } from '@/types/flow'
import { PropertyMenu } from '@/components/property/property-menu'
import { CalendarIcon, CycleIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { Avatar } from '@/components/issue/issue-row'
import { IssueTitleEditor } from '@/components/issue/issue-title-editor'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { DueDateCommand } from '@/components/issue/due-date-picker'
import styles from './create-issue-dialog.module.css'
import { createDraft, deleteDraft, updateDraft } from '@/lib/api'
import { labelsForResource, toggleGroupedLabelIds } from '@/lib/labels'
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { Toggle } from '@/components/ui/toggle'
import type { MyIssuesCreateContext } from '@/components/my-issues/my-issues-list'

export interface CreateIssueInput {
  title: string
  description: string
  descriptionState?: string
  descriptionData?: Record<string, unknown>
  contentState?: string
  stateId?: string
  priority?: number
  estimate?: number
  assigneeId?: string
  projectId?: string
  projectMilestoneId?: string
  cycleId?: string
  dueDate?: string
  labelIds?: string[]
  templateId?: string
  recurrence?: '' | 'daily' | 'weekly' | 'monthly'
  teamId?: string
  createMore?: boolean
}

export interface CreateIssueDialogProps {
  open: boolean
  data: BootstrapData
  initialStateId?: string
  initialProjectId?: string
  initialProjectMilestoneId?: string
  initialTeamId?: string
  initialTemplateId?: string
  initialContext?: MyIssuesCreateContext
  draftId?: string
  onOpenChange: (open: boolean) => void
  onCreate: (input: CreateIssueInput) => Promise<Issue>
  onUpload?: (issueId: string, file: File) => Promise<void>
  onDraftSaved?: (draft: Draft) => Promise<void> | void
  onDraftDeleted?: (draftId: string) => Promise<void> | void
}

const priorityNames = ['No priority', 'Urgent', 'High', 'Medium', 'Low']
const draftStoragePrefix = 'flow:create-issue-draft:'

interface StoredIssueDraft {
  title: string
  teamId: string
  description: DescriptionSnapshot | null
  stateId: string
  priority: number
  estimate: number
  assigneeId: string
  projectId: string
  projectMilestoneId: string
  cycleId: string
  dueDate: string
  labelIds: string[]
  templateId?: string
  recurrence?: '' | 'daily' | 'weekly' | 'monthly'
  updatedAt?: string
}

export function CreateIssueDialog({ data, draftId, initialContext, initialProjectId, initialProjectMilestoneId, initialTeamId, initialStateId, initialTemplateId, onCreate, onDraftDeleted, onDraftSaved, onOpenChange, onUpload, open }: CreateIssueDialogProps) {
  const requestedTeamId = initialContext?.teamId ?? initialTeamId
  const requestedStateId = initialContext?.stateId ?? initialStateId
  const [teamId, setTeamId] = useState(requestedTeamId || data.teams[0]?.id || '')
  const availableStates = useMemo(() => {
    const specific = data.states.some(state => state.teamId === teamId)
    const states = data.states.filter(state => specific ? state.teamId === teamId : !state.teamId)
    // A grouped create action may target a workspace state that is not in the
    // team's custom state set. Keep that state available so the preset is both
    // visible in the picker and sent with the create request.
    if (requestedStateId && !states.some(state => state.id === requestedStateId)) {
      const requested = data.states.find(state => state.id === requestedStateId)
      if (requested) return [...states, requested].sort((left, right) => (left.position??0) - (right.position??0))
    }
    return [...states].sort((left, right) => (left.position??0) - (right.position??0))
  }, [data.states, requestedStateId, teamId])
  const defaultState = useMemo(() => {
    const configured = data.teamSettings[teamId]?.defaultStateId
    return availableStates.find(state => state.id === configured)
      ?? availableStates.find(state => state.default)
      ?? [...availableStates].sort((a, b) => (a.position??0) - (b.position??0)).find(state => state.type === 'unstarted')
      ?? availableStates[0]
  }, [availableStates, data.teamSettings, teamId])
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState<DescriptionSnapshot | null>(null)
  const [stateId, setStateId] = useState(requestedStateId ?? defaultState.id)
  const [priority, setPriority] = useState<number>(initialContext?.priority ?? 0)
  const [estimate, setEstimate] = useState(0)
  const [assigneeId, setAssigneeId] = useState(initialContext?.assigneeId ?? data.viewer.id)
  const [projectId, setProjectId] = useState(initialContext?.projectId ?? '')
  const [projectMilestoneId, setProjectMilestoneId] = useState(initialContext?.projectMilestoneId ?? '')
  const [cycleId, setCycleId] = useState(initialContext?.cycleId ?? '')
  const [dueDate, setDueDate] = useState('')
  const [labelIds, setLabelIds] = useState<string[]>(initialContext?.labelIds ?? [])
  const [templateId, setTemplateId] = useState('')
  const [recurrence, setRecurrence] = useState<'' | 'daily' | 'weekly' | 'monthly'>('')
  const [serverDraftId, setServerDraftId] = useState('')
  const [createMore, setCreateMore] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [files, setFiles] = useState<File[]>([])
  const [linkOpen, setLinkOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [discardConfirmOpen, setDiscardConfirmOpen] = useState(false)
  const titleEditorRef = useRef<Editor | null>(null)
  const descriptionEditorRef = useRef<Editor | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const initializationKey = [open, draftId ?? '', initialProjectId ?? '', initialProjectMilestoneId ?? '', requestedStateId ?? '', requestedTeamId ?? '', JSON.stringify(initialContext ?? {})].join('\u001f')
  const initializedKeyRef = useRef('')
  const draftKey = `${draftStoragePrefix}${teamId || data.teams[0]?.key || 'default'}`
  useEffect(()=>{if(!open||!initialTemplateId)return;const template=data.issueTemplates.find(item=>item.id===initialTemplateId);if(!template)return;setTemplateId(template.id);setTitle(current=>current||template.title||template.name);setStateId(template.stateId||defaultState.id);setPriority(template.priority);setAssigneeId(template.assigneeId??data.viewer.id);setProjectId(template.projectId??'');setLabelIds(template.labelIds);const templateBody=template.body;if(templateBody)requestAnimationFrame(()=>descriptionEditorRef.current?.commands.setContent(templateBody,{contentType:'markdown'}))},[data.issueTemplates,data.viewer.id,defaultState.id,initialTemplateId,open])
  const hasDraftContent = Boolean(title.trim() || description?.markdown.trim() || files.length)

  useEffect(() => {
    if (!open || (draftId && !draftId.startsWith('local:')) || !hasDraftContent) return
    const timer = window.setTimeout(() => writeStoredDraft(draftKey, {
      title, description, stateId, priority, estimate, assigneeId, projectId,
      projectMilestoneId, cycleId, dueDate, labelIds, templateId, recurrence, teamId,
    }), 250)
    return () => window.clearTimeout(timer)
  }, [assigneeId, cycleId, description, draftId, draftKey, dueDate, estimate, hasDraftContent, labelIds, open, priority, projectId, projectMilestoneId, recurrence, stateId, teamId, templateId, title])

  useEffect(() => {
    if (!open) {
      initializedKeyRef.current = ''
      return
    }
    if (initializedKeyRef.current === initializationKey) return
    initializedKeyRef.current = initializationKey
    // A new issue action must always start clean. Drafts are restored only when
    // the user explicitly resumes one from the drafts page (local or remote).
    const draft = draftId?.startsWith('local:') ? readStoredDraft(draftKey) : null
    const remoteDraft = draftId && !draftId.startsWith('local:')
      ? data.drafts.find(item => item.id === draftId)
      : undefined
    const remote = remoteDraft?.metadata as Partial<StoredIssueDraft> | undefined
    const requestedProject = initialProjectId ? data.projects.find(project => project.id === initialProjectId) : undefined
    const requestedMilestone = requestedProject?.milestones.find(milestone => milestone.id === initialProjectMilestoneId)
    if (draft || remote) {
      const restored = draft ?? remote as StoredIssueDraft
      setTeamId(restored.teamId || (remoteDraft?.metadata?.teamId as string | undefined) || data.teams[0]?.id || '')
      setTitle(restored.title ?? remoteDraft?.title ?? '')
      setDescription(restored.description ?? null)
      setStateId(restored.stateId || requestedStateId || defaultState.id)
      setPriority(restored.priority ?? initialContext?.priority ?? 0)
      setEstimate(restored.estimate ?? 0)
      setAssigneeId(restored.assigneeId ?? initialContext?.assigneeId ?? data.viewer.id)
      setProjectId(restored.projectId ?? initialContext?.projectId ?? '')
      setProjectMilestoneId(restored.projectMilestoneId ?? initialContext?.projectMilestoneId ?? '')
      setCycleId(restored.cycleId ?? initialContext?.cycleId ?? '')
      setDueDate(restored.dueDate ?? '')
      setLabelIds(restored.labelIds ?? initialContext?.labelIds ?? [])
      setTemplateId(restored.templateId ?? '')
      setRecurrence(restored.recurrence ?? '')
      setServerDraftId(remoteDraft?.id ?? '')
    } else {
      setTitle(''); setDescription(null); setFiles([]); setError(undefined); setServerDraftId(''); setTeamId(requestedTeamId || data.teams[0]?.id || '')
      setStateId(requestedStateId || defaultState.id); setPriority(initialContext?.priority ?? 0); setEstimate(0); setAssigneeId(initialContext?.assigneeId ?? data.viewer.id); setProjectId(initialContext?.projectId ?? ''); setProjectMilestoneId(initialContext?.projectMilestoneId ?? ''); setCycleId(initialContext?.cycleId ?? ''); setDueDate(''); setLabelIds(initialContext?.labelIds ?? []); setTemplateId(''); setRecurrence(''); setCreateMore(false); setExpanded(false)
      titleEditorRef.current?.commands.clearContent(); descriptionEditorRef.current?.commands.clearContent()
      if (requestedStateId && availableStates.some(state => state.id === requestedStateId)) setStateId(requestedStateId)
    }
    if (requestedProject) {
      setProjectId(requestedProject.id)
      setProjectMilestoneId(requestedMilestone?.id ?? '')
    }
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => titleEditorRef.current?.commands.focus('end')))
    return () => cancelAnimationFrame(frame)
  }, [availableStates, data.drafts, data.projects, data.teams, data.viewer.id, defaultState.id, draftId, draftKey, initialContext, initialProjectId, initialProjectMilestoneId, initializationKey, open, requestedStateId, requestedTeamId, teamId])

  useEffect(() => { if (open && !availableStates.some(state => state.id === stateId)) setStateId(defaultState.id) }, [availableStates, defaultState.id, open, stateId])

  useEffect(() => {
    if (!open || draftId || initialTemplateId || !requestedStateId) return
    if (availableStates.some(state => state.id === requestedStateId)) setStateId(requestedStateId)
  }, [availableStates, draftId, initialTemplateId, open, requestedStateId])

  const state = availableStates.find(item => item.id === stateId) ?? defaultState
  const team = data.teams.find(item => item.id === teamId) ?? data.teams[0]
  const assignee = data.users.find(user => user.id === assigneeId)
  const project = data.projects.find(item => item.id === projectId)
  const projectMilestone = project?.milestones.find(item => item.id === projectMilestoneId)
  const cycle = data.cycles.find(item => item.id === cycleId)
  const cycles = data.cycles.filter(item => item.teamId === teamId && item.status !== 'completed')
  const nextUpcomingCycleId = [...cycles].filter(item => item.status === 'upcoming').sort((left,right)=>left.startsAt.localeCompare(right.startsAt))[0]?.id
  const estimateType = data.teamSettings[teamId]?.estimateType ?? 'notUsed'
  const estimateValues = estimateType === 'fibonacci' ? [0,1,2,3,5,8,13,21] : estimateType === 'exponential' ? [0,1,2,4,8,16] : [0,1,2,3,5,8]
  const issueLabels = useMemo(() => labelsForResource(data.labels, 'issue', data.labelGroups), [data.labelGroups, data.labels])
  const availableLabels = useMemo(() => issueLabels.filter(label => !label.scope || label.scope === 'Workspace' || label.scope === teamId), [issueLabels, teamId])
  const availableLabelIds = useMemo(() => new Set(availableLabels.map(label => label.id)), [availableLabels])
  const selectedLabels = availableLabels.filter(label => labelIds.includes(label.id))
  const labelGroupNames = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.name])), [data.labelGroups])
  const labelGroupColors = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.color])), [data.labelGroups])
  const toggleLabel = (id: string) => setLabelIds(current => toggleGroupedLabelIds(current, id, availableLabels))

  const resetBody = (focus = true) => {
    setTitle('')
    setDescription(null)
    titleEditorRef.current?.commands.clearContent()
    descriptionEditorRef.current?.commands.clearContent()
    setFiles([])
    setError(undefined)
    if (focus) requestAnimationFrame(() => titleEditorRef.current?.commands.focus('end'))
  }

  const resetDraft = () => {
    resetBody(false)
    setServerDraftId('')
    setStateId(defaultState.id)
    setPriority(0)
    setEstimate(0)
    setAssigneeId(data.viewer.id)
    setProjectId('')
    setProjectMilestoneId('')
    setCycleId('')
    setDueDate('')
    setLabelIds([])
    setTemplateId('')
    setRecurrence('')
    setCreateMore(false)
    setExpanded(false)
  }

  const saveDraft = async () => {
    if (!hasDraftContent) return
    const metadata = { title, description, stateId, priority, estimate, assigneeId, projectId, projectMilestoneId, cycleId, dueDate, labelIds, templateId, recurrence, teamId }
    setSaving(true)
    setError(undefined)
    try {
      const input = { type: 'issue', title: title || 'Untitled issue', body: description?.markdown ?? '', contentData: description?.document as Record<string,unknown>|undefined, metadata }
      const saved = serverDraftId ? await updateDraft(serverDraftId, input) : await createDraft(input)
      setServerDraftId(saved.id)
      await onDraftSaved?.(saved)
      removeStoredDraft(draftKey)
      setConfirmOpen(false)
      onOpenChange(false)
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not save draft')
    } finally {
      setSaving(false)
    }
  }

  const discardDraft = async () => {
    removeStoredDraft(draftKey)
    if (serverDraftId) {
      await deleteDraft(serverDraftId)
      await onDraftDeleted?.(serverDraftId)
    }
    setConfirmOpen(false)
    resetDraft()
    onOpenChange(false)
  }

  const submit = useCallback(async () => {
    const cleanTitle = title.trim()
    if (!cleanTitle || saving) return
    setSaving(true)
    setError(undefined)
    try {
      const issue = await onCreate({
        title: cleanTitle,
        teamId,
        description: description?.markdown.trim() ?? '',
        descriptionState: description?.documentJSON,
        descriptionData: description?.document as Record<string, unknown> | undefined,
        contentState: description?.contentState,
        stateId,
        priority,
        estimate,
        assigneeId,
        projectId,
        projectMilestoneId,
        cycleId,
        dueDate,
        labelIds: labelIds.filter(id => availableLabelIds.has(id)),
        templateId,
        recurrence,
        createMore,
      })
      const uploads = onUpload ? await Promise.allSettled(files.map(file => onUpload(issue.id, file))) : []
      const failedUploads = uploads.filter(result => result.status === 'rejected').length
      removeStoredDraft(draftKey)
      if (serverDraftId) {
        await deleteDraft(serverDraftId)
        await onDraftDeleted?.(serverDraftId)
      }
      resetBody()
      if (failedUploads) setError(`Issue created, but ${failedUploads} attachment${failedUploads === 1 ? '' : 's'} failed to upload`)
      if (!createMore) onOpenChange(false)
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not create issue')
    } finally {
      setSaving(false)
    }
  }, [assigneeId, availableLabelIds, createMore, cycleId, description, draftKey, dueDate, estimate, files, labelIds, onCreate, onDraftDeleted, onOpenChange, onUpload, priority, projectId, projectMilestoneId, recurrence, saving, serverDraftId, stateId, teamId, templateId, title])

  const changeOpen = (next: boolean) => {
    if (!next && serverDraftId && hasDraftContent) {
      void saveDraft()
      return
    }
    if (!next && hasDraftContent) {
      setConfirmOpen(true)
      return
    }
    onOpenChange(next)
  }

  const addFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const next = Array.from(event.target.files ?? [])
    setFiles(current => [...current, ...next])
    event.target.value = ''
  }

  return <Dialog.Root open={open} onOpenChange={changeOpen}>
    <Dialog.Portal>
      <Dialog.Overlay className={styles.overlay} onPointerDown={() => changeOpen(false)}/>
      <Dialog.Content className={styles.dialog} data-expanded={expanded} aria-label="Create issue" onPointerDownOutside={event => event.preventDefault()} onInteractOutside={event => event.preventDefault()} onEscapeKeyDown={event => { if (saving) event.preventDefault() }}>
        <form onSubmit={event => { event.preventDefault(); void submit() }}>
          <header className={styles.header}>
            <PropertyMenu compact label="Team" value={team?.key ?? 'Team'} selectedId={teamId} options={data.teams.filter(item=>!item.retiredAt).map(item=>({id:item.id,label:item.name,keywords:item.key,icon:<TeamIcon team={item}/>,i18nIgnore:true}))} trigger={<><TeamIcon team={team}/><span data-i18n-ignore>{team?.key ?? 'Team'}</span></>} triggerClassName={styles.team} ariaLabel="Set team" onChange={id=>{setTeamId(id);setCycleId('');setLabelIds([]);setTemplateId('')}}/>
            <span className={styles.breadcrumb}>›</span><Dialog.Title>New issue</Dialog.Title>
            {serverDraftId ? <button type="button" className={styles.saveDraft} aria-label="Discard draft" disabled={saving} onClick={() => setDiscardConfirmOpen(true)}><Trash2/></button> : hasDraftContent && <button type="button" className={styles.saveDraft} aria-label="Save draft" disabled={saving} onClick={() => void saveDraft()}>{saving ? 'Saving...' : 'Save as draft'}</button>}
            <button type="button" className={`${styles.iconButton} ${styles.expandButton}`} aria-label={expanded ? 'Collapse' : 'Expand'} aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? <Minimize2/> : <Maximize2/>}</button>
            <Dialog.Close asChild><button type="button" className={styles.iconButton} aria-label="Close"><X/></button></Dialog.Close>
          </header>

          <div className={styles.document}>
            <IssueTitleEditor value={title} onChange={setTitle} onEnter={() => descriptionEditorRef.current?.commands.focus('start')} onSubmit={() => void submit()} autoFocus={open} className={styles.titleField} editorRef={editor => { titleEditorRef.current = editor }}/>
            <IssueDescriptionEditor users={data.users} value={description?.markdown ?? ''} state={description?.documentJSON} onChange={setDescription} onSubmit={() => void submit()} className={styles.descriptionField} editorRef={editor => { descriptionEditorRef.current = editor }}/>
          </div>

          <div className={styles.properties}>
            {data.issueTemplates.some(item=>item.teamId===teamId) && <MiniProperty label="Template" value={data.issueTemplates.find(item=>item.id===templateId)?.name ?? 'Template'} selectedId={templateId} icon={<FilePlus2/>} options={[{id:'',label:'No template',icon:<FilePlus2/>},...data.issueTemplates.filter(item=>item.teamId===teamId).map(item=>({id:item.id,label:item.name,description:item.description,icon:<FilePlus2/>}))]} onChange={id=>{setTemplateId(id);const template=data.issueTemplates.find(item=>item.id===id);if(!template)return;setTitle(current=>current||template.name);setStateId(template.stateId||defaultState.id);setPriority(template.priority);setAssigneeId(template.assigneeId??data.viewer.id);setProjectId(template.projectId??'');setLabelIds(template.labelIds);if(template.body)descriptionEditorRef.current?.commands.setContent(template.body,{contentType:'markdown'})}}/>}
            <MiniProperty label="Status" value={state.name} selectedId={stateId} icon={<StatusIcon state={state}/>} options={[...availableStates].sort((a,b) => (a.position??0)-(b.position??0)).map((item,index) => ({ id:item.id,label:item.name,color:item.color,icon:<StatusIcon state={item}/>,shortcut:index < 5 ? String(index+1) : undefined }))} onChange={setStateId}/>
            <MiniProperty label="Priority" value={priority ? priorityNames[priority] : 'Priority'} selectedId={String(priority)} icon={<PriorityIcon priority={priority}/>} options={[0,1,2,3,4].map(item => ({ id:String(item),label:priorityNames[item],icon:<PriorityIcon priority={item}/>,shortcut:String(item) }))} onChange={value => setPriority(Number(value))}/>
            {estimateType!=='notUsed'&&<MiniProperty label="Estimate" value={estimate ? `${estimate} point${estimate===1?'':'s'}` : 'Estimate'} selectedId={String(estimate)} icon={<EstimateGlyph value={estimate}/>} options={estimateValues.map(value=>({id:String(value),label:value?`${value} point${value===1?'':'s'}`:'No estimate',icon:<EstimateGlyph value={value}/>}))} onChange={value=>setEstimate(Number(value))}/>}
            <MiniProperty label="Assignee" value={assignee?.displayName ?? 'Assignee'} selectedId={assigneeId} icon={assignee ? <Avatar name={assignee.displayName}/> : <NoAssigneeIcon/>} options={[{id:'',label:'No assignee',icon:<NoAssigneeIcon/>},...data.users.filter(user => user.active).map(user => ({id:user.id,label:user.displayName,keywords:user.email,icon:<Avatar name={user.displayName}/>}))]} onChange={setAssigneeId}/>
            <MiniProperty label="Project" value={project?.name ?? 'Project'} valueIsEntityName={Boolean(project)} selectedId={projectId} icon={<ProjectIcon/>} options={[{id:'',label:'No project',icon:<NoProjectIcon/>},...data.projects.map(item => ({id:item.id,label:item.name,color:item.color,icon:<ProjectIcon style={{ color: item.color }}/>,i18nIgnore:true }))]} onChange={value => { setProjectId(value); setProjectMilestoneId('') }}/>
            {project && project.milestones.length > 0 && <MiniProperty label="Milestone" value={projectMilestone?.name ?? 'Milestone'} valueIsEntityName={Boolean(projectMilestone)} selectedId={projectMilestoneId} icon={<Diamond size={14}/>} options={[{id:'',label:'No milestone',icon:<Diamond size={14}/>},...project.milestones.map(item => ({id:item.id,label:item.name,icon:<Diamond size={14}/>,i18nIgnore:true}))]} onChange={setProjectMilestoneId}/>}
            <MiniProperty multiple label="Labels" value={selectedLabels.length ? selectedLabels.map(label => label.name).join(', ') : 'Labels'} selectedIds={labelIds} icon={<LabelIcon/>} options={availableLabels.map(item => ({ id: item.id, label: item.name, color: item.color, description: item.description, issueCount: item.issueCount, scope: item.scope, resourceType: item.resourceType, groupId: item.groupId, groupLabel: item.groupId ? labelGroupNames.get(item.groupId) : undefined, groupColor: item.groupId ? labelGroupColors.get(item.groupId) : undefined }))} onChange={toggleLabel}/>
            <MiniProperty label="Cycle" value={cycle?.name ?? 'Cycle'} valueIsEntityName={Boolean(cycle)} selectedId={cycleId} icon={<CycleIcon cycle={cycle} nextUpcomingId={nextUpcomingCycleId} progress={cycle?cycleIssueProgress(data.issues,cycle.id):0}/>} options={[{id:'',label:'No cycle',icon:<CycleIcon noCycle/>},...cycles.map(item=>({id:item.id,label:item.name,icon:<CycleIcon cycle={item} nextUpcomingId={nextUpcomingCycleId} progress={cycleIssueProgress(data.issues,item.id)}/>,i18nIgnore:true}))]} onChange={setCycleId} ariaLabel="Add to cycle"/>
            <MoreActions active={open && !linkOpen} dueDate={dueDate} recurrence={recurrence} onDueDateChange={setDueDate} onRecurrenceChange={setRecurrence} onInsertLink={() => setLinkOpen(true)}/>
          </div>

          {files.length > 0 && <div className={styles.attachments}>{files.map((file, index) => <span key={`${file.name}-${index}`}><Paperclip/><span>{file.name}</span><AttachmentRemoveButton label={`Remove ${file.name}`} onClick={() => setFiles(current => current.filter((_, item) => item !== index))}/></span>)}</div>}
          {error && <div className={styles.error} role="alert">{error}</div>}

          <footer className={styles.footer}>
            <button type="button" className={styles.attachButton} aria-label="Attach images, files, or videos" onClick={() => fileRef.current?.click()}><Paperclip/></button>
            <input ref={fileRef} type="file" hidden multiple onChange={addFiles}/>
            <label className={styles.createMore}><Toggle checked={createMore} label="Create more" onChange={setCreateMore}/><span>Create more</span></label>
            <button className={styles.submit} type="submit" disabled={saving}>{saving ? 'Creating…' : 'Create issue'}</button>
          </footer>
        </form>
      </Dialog.Content>
      <AddLinkDialog open={linkOpen} issueLabel="new issue" onOpenChange={setLinkOpen} onAdd={(url, label) => {
        const editor = descriptionEditorRef.current
        if (!editor) return
        editor.chain().focus().insertContent(label ? `<a href="${escapeAttribute(url)}">${escapeHtml(label)}</a>` : url).run()
      }}/>
      <DraftConfirmDialog open={confirmOpen} saving={saving} onCancel={() => setConfirmOpen(false)} onDiscard={() => void discardDraft()} onSave={() => void saveDraft()}/>
      <ExistingDraftDiscardDialog open={discardConfirmOpen} saving={saving} onCancel={() => setDiscardConfirmOpen(false)} onDiscard={() => void discardDraft()}/>
    </Dialog.Portal>
  </Dialog.Root>
}

function ExistingDraftDiscardDialog({ onCancel, onDiscard, open, saving }: { onCancel: () => void; onDiscard: () => void; open: boolean; saving: boolean }) {
  const discardRef = useRef<HTMLButtonElement>(null)
  return <DraftDialogFrame description="Your draft will be deleted." descriptionId="existing-draft-discard-description" focusRef={discardRef} onCancel={onCancel} open={open} title="Discard this draft?"><div className={styles.confirmActions}><span/><span/><button type="button" disabled={saving} onClick={onCancel}>Cancel</button><button ref={discardRef} type="button" className={styles.confirmDiscard} disabled={saving} onClick={onDiscard}>Discard</button></div></DraftDialogFrame>
}

function MiniProperty(props: React.ComponentProps<typeof PropertyMenu>) { return <PropertyMenu compact {...props}/> }
function EstimateGlyph({ value }: { value: number }) { return value ? <span aria-hidden="true" className="estimate-value-icon">{value}</span> : <CircleDashed aria-hidden="true"/> }
function cycleIssueProgress(issues: Issue[], cycleId: string) { const scoped=issues.filter(issue=>issue.cycleId===cycleId&&!issue.archivedAt);return scoped.length?Math.round(scoped.filter(issue=>issue.state.type==='completed'||issue.state.type==='canceled').length/scoped.length*100):0 }

function MoreActions({ active, dueDate, recurrence, onDueDateChange, onRecurrenceChange, onInsertLink }: { active: boolean; dueDate: string; recurrence: '' | 'daily' | 'weekly' | 'monthly'; onDueDateChange: (date: string) => void; onRecurrenceChange: (value: '' | 'daily' | 'weekly' | 'monthly') => void; onInsertLink: () => void }) {
  const [open, setOpen] = useState(false)
  const [dateOpen, setDateOpen] = useState(false)
  useEffect(() => {
    if (!active) return
    const shortcut = (event: KeyboardEvent) => {
      if (event.shiftKey && !event.metaKey && !event.ctrlKey && event.key.toLowerCase() === 'd') {
        event.preventDefault()
        setOpen(true)
        setDateOpen(true)
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'l') {
        event.preventDefault()
        setOpen(false)
        onInsertLink()
      }
    }
    addEventListener('keydown', shortcut)
    return () => removeEventListener('keydown', shortcut)
  }, [active, onInsertLink])
  return <DropdownMenu.Root open={open} onOpenChange={setOpen}>
    <DropdownMenu.Trigger asChild><button type="button" className={styles.moreButton} aria-label="More actions"><MoreHorizontal/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal><DropdownMenu.Content className={styles.moreMenu} align="start" sideOffset={4} collisionPadding={10}>
      <DropdownMenu.Sub open={dateOpen} onOpenChange={setDateOpen}>
        <DropdownMenu.SubTrigger className={styles.menuItem}><CalendarIcon/><span>Set due date</span><kbd>⇧ D</kbd><ChevronRight/></DropdownMenu.SubTrigger>
        <DropdownMenu.Portal><DropdownMenu.SubContent className={styles.dateMenu} sideOffset={3} alignOffset={-5}><DueDateCommand value={dueDate} onSelect={async value => onDueDateChange(value)}/></DropdownMenu.SubContent></DropdownMenu.Portal>
      </DropdownMenu.Sub>
      <DropdownMenu.Sub><DropdownMenu.SubTrigger className={styles.menuItem}><Repeat2/><span>Make recurring…</span><ChevronRight/></DropdownMenu.SubTrigger><DropdownMenu.Portal><DropdownMenu.SubContent className={styles.moreMenu} sideOffset={3}>{(['daily','weekly','monthly'] as const).map(value=><DropdownMenu.CheckboxItem className={styles.menuItem} checked={recurrence===value} key={value} onCheckedChange={()=>onRecurrenceChange(recurrence===value?'':value)}><Repeat2/><span>{value[0].toUpperCase()+value.slice(1)}</span>{recurrence===value&&<span>✓</span>}</DropdownMenu.CheckboxItem>)}</DropdownMenu.SubContent></DropdownMenu.Portal></DropdownMenu.Sub>
      <DropdownMenu.Item className={styles.menuItem} onSelect={onInsertLink}><Link2/><span>Add link…</span><kbd>Ctrl L</kbd></DropdownMenu.Item>
    </DropdownMenu.Content></DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function AddLinkDialog({ issueLabel, onAdd, onOpenChange, open }: { issueLabel: string; onAdd: (url: string, label: string) => void; onOpenChange: (open: boolean) => void; open: boolean }) {
  const [url, setUrl] = useState('')
  const [label, setLabel] = useState('')
  const submitLink = () => {
    const value = url.trim()
    if (!value) return
    onAdd(/^https?:\/\//i.test(value) ? value : `https://${value}`, label.trim())
    setUrl('')
    setLabel('')
    onOpenChange(false)
  }
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay className={styles.linkOverlay}/>
      <Dialog.Content className={styles.linkDialog} aria-label={`Add link to ${issueLabel}`}>
        <Dialog.Title><ExternalLink/><span>Add link to {issueLabel}</span></Dialog.Title>
        <label><span>URL</span><input autoFocus aria-label="URL" placeholder="https://…" value={url} onChange={event => setUrl(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submitLink() } }}/></label>
        <label><span>Title <small>(optional)</small></span><input aria-label="Title (optional)" value={label} onChange={event => setLabel(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); submitLink() } }}/></label>
        <div><Dialog.Close asChild><button type="button">Cancel</button></Dialog.Close><button type="button" className={styles.addLink} disabled={!url.trim()} onClick={submitLink}>Add link</button></div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

function DraftConfirmDialog({ onCancel, onDiscard, onSave, open, saving }: { onCancel: () => void; onDiscard: () => void; onSave: () => void; open: boolean; saving: boolean }) {
  const saveRef = useRef<HTMLButtonElement>(null)
  return <DraftDialogFrame description="You can finish this issue later from your drafts." descriptionId="draft-confirm-description" escapeLocked focusRef={saveRef} onCancel={onCancel} open={open} title="Save to drafts?"><div className={styles.confirmActions}><button type="button" disabled={saving} onClick={onDiscard}>Discard</button><span/><button type="button" disabled={saving} onClick={onCancel}>Cancel</button><button ref={saveRef} type="button" className={styles.confirmSave} disabled={saving} onClick={onSave}>{saving ? 'Saving...' : 'Save'}</button></div></DraftDialogFrame>
}

function DraftDialogFrame({ children, description, descriptionId, escapeLocked = false, focusRef, onCancel, open, title }: { children: ReactNode; description: string; descriptionId: string; escapeLocked?: boolean; focusRef: RefObject<HTMLButtonElement | null>; onCancel: () => void; open: boolean; title: string }) {
  return <Dialog.Root open={open} onOpenChange={next => { if (!next) onCancel() }}><Dialog.Portal><Dialog.Overlay className={styles.confirmOverlay}/><Dialog.Content className={styles.confirmDialog} aria-describedby={descriptionId} onOpenAutoFocus={event => { event.preventDefault(); focusRef.current?.focus() }} onEscapeKeyDown={escapeLocked ? event => { event.preventDefault(); onCancel() } : undefined} onPointerDownOutside={event => event.preventDefault()}><Dialog.Title>{title}</Dialog.Title><Dialog.Description id={descriptionId}>{description}</Dialog.Description>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>
}

function readStoredDraft(key: string): StoredIssueDraft | null {
  try {
    const value = globalThis.localStorage?.getItem(key)
    return value ? JSON.parse(value) as StoredIssueDraft : null
  } catch {
    return null
  }
}

function writeStoredDraft(key: string, draft: StoredIssueDraft) {
  try { globalThis.localStorage?.setItem(key, JSON.stringify({ ...draft, updatedAt: new Date().toISOString() })) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

function removeStoredDraft(key: string) {
  try { globalThis.localStorage?.removeItem(key) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

function escapeHtml(value: string) { return value.replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]!) }
function escapeAttribute(value: string) { return escapeHtml(value) }
