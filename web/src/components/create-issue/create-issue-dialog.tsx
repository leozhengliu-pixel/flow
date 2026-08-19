import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import type { Editor } from '@tiptap/react'
import { ChevronRight, ExternalLink, FilePlus2, Link2, Maximize2, Minimize2, MoreHorizontal, Paperclip, Repeat2, X } from 'lucide-react'
import type { BootstrapData, Draft, Issue } from '@/types/flow'
import { PropertyMenu } from '@/components/property/property-menu'
import { CalendarIcon, LabelIcon, NoAssigneeIcon, NoProjectIcon, PriorityIcon, ProjectIcon, StatusIcon, TeamIcon } from '@/components/issue/issue-icons'
import { Avatar } from '@/components/issue/issue-row'
import { IssueTitleEditor } from '@/components/issue/issue-title-editor'
import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import type { DescriptionSnapshot } from '@/components/issue/editor/editor-content'
import { DueDateCommand } from '@/components/issue/due-date-picker'
import styles from './create-issue-dialog.module.css'
import { createDraft, deleteDraft, updateDraft } from '@/lib/api'
import { labelsForResource } from '@/lib/labels'

export interface CreateIssueInput {
  title: string
  description: string
  descriptionState?: string
  descriptionData?: Record<string, unknown>
  contentState?: string
  stateId?: string
  priority?: number
  assigneeId?: string
  projectId?: string
  dueDate?: string
  labelIds?: string[]
  templateId?: string
  teamId?: string
  createMore?: boolean
}

export interface CreateIssueDialogProps {
  open: boolean
  data: BootstrapData
  initialStateId?: string
  initialProjectId?: string
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
  description: DescriptionSnapshot | null
  stateId: string
  priority: number
  assigneeId: string
  projectId: string
  dueDate: string
  labelIds: string[]
  templateId?: string
}

export function CreateIssueDialog({ data, draftId, initialProjectId, initialStateId, onCreate, onDraftDeleted, onDraftSaved, onOpenChange, onUpload, open }: CreateIssueDialogProps) {
  const availableStates = useMemo(() => { const teamId=data.teams[0]?.id; const specific=data.states.some(state=>state.teamId===teamId); return data.states.filter(state=>specific?state.teamId===teamId:!state.teamId) }, [data.states,data.teams])
  const defaultState = useMemo(() => [...availableStates].sort((a, b) => a.position - b.position).find(state => state.type === 'unstarted') ?? availableStates[0], [availableStates])
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState<DescriptionSnapshot | null>(null)
  const [stateId, setStateId] = useState(defaultState.id)
  const [priority, setPriority] = useState(0)
  const [assigneeId, setAssigneeId] = useState(data.viewer.id)
  const [projectId, setProjectId] = useState('')
  const [dueDate, setDueDate] = useState('')
  const [labelIds, setLabelIds] = useState<string[]>([])
  const [templateId, setTemplateId] = useState('')
  const [serverDraftId, setServerDraftId] = useState('')
  const [createMore, setCreateMore] = useState(false)
  const [expanded, setExpanded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [files, setFiles] = useState<File[]>([])
  const [linkOpen, setLinkOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const titleEditorRef = useRef<Editor | null>(null)
  const descriptionEditorRef = useRef<Editor | null>(null)
  const fileRef = useRef<HTMLInputElement>(null)
  const draftKey = `${draftStoragePrefix}${data.teams[0]?.id ?? data.teams[0]?.key ?? 'default'}`
  const hasDraftContent = Boolean(title.trim() || description?.markdown.trim() || files.length)

  useEffect(() => {
    if (!open) return
    const draft = draftId ? null : readStoredDraft(draftKey)
    const remoteDraft = draftId
      ? data.drafts.find(item => item.id === draftId)
      : data.drafts.find(item => item.type === 'issue' && item.metadata?.teamId === data.teams[0]?.id)
    const remote = remoteDraft?.metadata as Partial<StoredIssueDraft> | undefined
    if (draft || remote) {
      const restored = draft ?? remote as StoredIssueDraft
      setTitle(restored.title ?? remoteDraft?.title ?? '')
      setDescription(restored.description ?? null)
      setStateId(restored.stateId || defaultState.id)
      setPriority(restored.priority ?? 0)
      setAssigneeId(restored.assigneeId ?? data.viewer.id)
      setProjectId(restored.projectId ?? '')
      setDueDate(restored.dueDate ?? '')
      setLabelIds(restored.labelIds ?? [])
      setTemplateId(restored.templateId ?? '')
      setServerDraftId(remoteDraft?.id ?? '')
    } else {
      if (initialStateId && availableStates.some(state => state.id === initialStateId)) setStateId(initialStateId)
      if (initialProjectId && data.projects.some(project => project.id === initialProjectId)) setProjectId(initialProjectId)
    }
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => titleEditorRef.current?.commands.focus('end')))
    return () => cancelAnimationFrame(frame)
  }, [availableStates, data.drafts, data.projects, data.teams, data.viewer.id, defaultState.id, draftId, draftKey, initialProjectId, initialStateId, open])

  const state = availableStates.find(item => item.id === stateId) ?? defaultState
  const assignee = data.users.find(user => user.id === assigneeId)
  const project = data.projects.find(item => item.id === projectId)
  const issueLabels = useMemo(() => labelsForResource(data.labels, 'issue'), [data.labels])
  const availableLabels = useMemo(() => issueLabels.filter(label => !label.scope || label.scope === 'Workspace' || label.scope === data.teams[0]?.id), [data.teams, issueLabels])
  const availableLabelIds = useMemo(() => new Set(availableLabels.map(label => label.id)), [availableLabels])
  const selectedLabels = availableLabels.filter(label => labelIds.includes(label.id))
  const labelGroupNames = useMemo(() => new Map(data.labelGroups.map(group => [group.id, group.name])), [data.labelGroups])

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
    setStateId(defaultState.id)
    setPriority(0)
    setAssigneeId(data.viewer.id)
    setProjectId('')
    setDueDate('')
    setLabelIds([])
    setTemplateId('')
    setCreateMore(false)
    setExpanded(false)
  }

  const saveDraft = async () => {
    if (!hasDraftContent) return
    const metadata = { title, description, stateId, priority, assigneeId, projectId, dueDate, labelIds, templateId, teamId: data.teams[0]?.id }
    setSaving(true)
    setError(undefined)
    try {
      const input = { type: 'issue', title: title || 'Untitled issue', body: description?.markdown ?? '', contentData: description?.document as Record<string,unknown>|undefined, metadata }
      const saved = serverDraftId ? await updateDraft(serverDraftId, input) : await createDraft(input)
      setServerDraftId(saved.id)
      writeStoredDraft(draftKey, metadata)
      await onDraftSaved?.(saved)
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
        description: description?.markdown.trim() ?? '',
        descriptionState: description?.documentJSON,
        descriptionData: description?.document as Record<string, unknown> | undefined,
        contentState: description?.contentState,
        stateId,
        priority,
        assigneeId,
        projectId,
        dueDate,
        labelIds: labelIds.filter(id => availableLabelIds.has(id)),
        templateId,
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
  }, [assigneeId, availableLabelIds, createMore, description, draftKey, dueDate, files, labelIds, onCreate, onDraftDeleted, onOpenChange, onUpload, priority, projectId, saving, serverDraftId, stateId, templateId, title])

  const changeOpen = (next: boolean) => {
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
            <button type="button" className={styles.team} aria-label="Set team" disabled><TeamIcon/><span>{data.teams[0].key}</span></button>
            <span className={styles.breadcrumb}>›</span><Dialog.Title>New issue</Dialog.Title>
            {hasDraftContent && <button type="button" className={styles.saveDraft} aria-label="Save draft" disabled={saving} onClick={() => void saveDraft()}>{saving ? 'Saving...' : 'Save as draft'}</button>}
            <button type="button" className={`${styles.iconButton} ${styles.expandButton}`} aria-label={expanded ? 'Collapse' : 'Expand'} aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? <Minimize2/> : <Maximize2/>}</button>
            <Dialog.Close asChild><button type="button" className={styles.iconButton} aria-label="Close"><X/></button></Dialog.Close>
          </header>

          <div className={styles.document}>
            <IssueTitleEditor value={title} onChange={setTitle} onEnter={() => descriptionEditorRef.current?.commands.focus('start')} onSubmit={() => void submit()} autoFocus={open} className={styles.titleField} editorRef={editor => { titleEditorRef.current = editor }}/>
            <IssueDescriptionEditor value={description?.markdown ?? ''} state={description?.documentJSON} onChange={setDescription} onSubmit={() => void submit()} className={styles.descriptionField} editorRef={editor => { descriptionEditorRef.current = editor }}/>
          </div>

          <div className={styles.properties}>
            {data.issueTemplates.length > 0 && <MiniProperty label="Template" value={data.issueTemplates.find(item=>item.id===templateId)?.name ?? 'Template'} selectedId={templateId} icon={<FilePlus2/>} options={[{id:'',label:'No template',icon:<FilePlus2/>},...data.issueTemplates.filter(item=>item.teamId===data.teams[0]?.id).map(item=>({id:item.id,label:item.name,description:item.description,icon:<FilePlus2/>}))]} onChange={id=>{setTemplateId(id);const template=data.issueTemplates.find(item=>item.id===id);if(!template)return;setTitle(current=>current||template.name);setStateId(template.stateId||defaultState.id);setPriority(template.priority);setAssigneeId(template.assigneeId??data.viewer.id);setProjectId(template.projectId??'');setLabelIds(template.labelIds);if(template.body)descriptionEditorRef.current?.commands.setContent(template.body,{contentType:'markdown'})}}/>}
            <MiniProperty label="Status" value={state.name} selectedId={stateId} icon={<StatusIcon state={state}/>} options={[...availableStates].sort((a,b) => a.position-b.position).map((item,index) => ({ id:item.id,label:item.name,color:item.color,icon:<StatusIcon state={item}/>,shortcut:index < 5 ? String(index+1) : undefined }))} onChange={setStateId}/>
            <MiniProperty label="Priority" value={priority ? priorityNames[priority] : 'Priority'} selectedId={String(priority)} icon={<PriorityIcon priority={priority}/>} options={[0,1,2,3,4].map(item => ({ id:String(item),label:priorityNames[item],icon:<PriorityIcon priority={item}/>,shortcut:String(item) }))} onChange={value => setPriority(Number(value))}/>
            <MiniProperty label="Assignee" value={assignee?.displayName ?? 'Assignee'} selectedId={assigneeId} icon={assignee ? <Avatar name={assignee.displayName}/> : <NoAssigneeIcon/>} options={[{id:'',label:'No assignee',icon:<NoAssigneeIcon/>},...data.users.filter(user => user.active).map(user => ({id:user.id,label:user.displayName,keywords:user.email,icon:<Avatar name={user.displayName}/>}))]} onChange={setAssigneeId}/>
            <MiniProperty label="Project" value={project?.name ?? 'Project'} selectedId={projectId} icon={<ProjectIcon/>} options={[{id:'',label:'No project',icon:<NoProjectIcon/>},...data.projects.map(item => ({id:item.id,label:item.name,color:item.color,icon:<ProjectIcon style={{ color: item.color }}/> }))]} onChange={setProjectId}/>
            <MiniProperty multiple label="Labels" value={selectedLabels.length ? selectedLabels.map(label => label.name).join(', ') : 'Labels'} selectedIds={labelIds} icon={<LabelIcon/>} options={availableLabels.map(item => ({ id: item.id, label: item.name, color: item.color, description: item.description, issueCount: item.issueCount, scope: item.scope, groupId: item.groupId, groupLabel: item.groupId ? labelGroupNames.get(item.groupId) : undefined }))} onChange={id => setLabelIds(current => current.includes(id) ? current.filter(value => value !== id) : [...current,id])}/>
            <MoreActions active={open && !linkOpen} dueDate={dueDate} onDueDateChange={setDueDate} onInsertLink={() => setLinkOpen(true)}/>
          </div>

          {files.length > 0 && <div className={styles.attachments}>{files.map((file, index) => <span key={`${file.name}-${index}`}><Paperclip/><span>{file.name}</span><button type="button" aria-label={`Remove ${file.name}`} onClick={() => setFiles(current => current.filter((_, item) => item !== index))}><X/></button></span>)}</div>}
          {error && <div className={styles.error} role="alert">{error}</div>}

          <footer className={styles.footer}>
            <button type="button" className={styles.attachButton} aria-label="Attach images, files, or videos" onClick={() => fileRef.current?.click()}><Paperclip/></button>
            <input ref={fileRef} type="file" hidden multiple onChange={addFiles}/>
            <label className={styles.createMore}><button type="button" role="checkbox" aria-checked={createMore} data-checked={createMore} onClick={() => setCreateMore(value => !value)}><i/></button><span>Create more</span></label>
            <button className={styles.submit} type="submit" disabled={!title.trim() || saving}>{saving ? 'Creating…' : 'Create issue'}</button>
          </footer>
        </form>
      </Dialog.Content>
      <AddLinkDialog open={linkOpen} issueLabel="new issue" onOpenChange={setLinkOpen} onAdd={(url, label) => {
        const editor = descriptionEditorRef.current
        if (!editor) return
        editor.chain().focus().insertContent(label ? `<a href="${escapeAttribute(url)}">${escapeHtml(label)}</a>` : url).run()
      }}/>
      <DraftConfirmDialog open={confirmOpen} saving={saving} onCancel={() => setConfirmOpen(false)} onDiscard={() => void discardDraft()} onSave={() => void saveDraft()}/>
    </Dialog.Portal>
  </Dialog.Root>
}

function MiniProperty(props: React.ComponentProps<typeof PropertyMenu>) { return <PropertyMenu compact {...props}/> }

function MoreActions({ active, dueDate, onDueDateChange, onInsertLink }: { active: boolean; dueDate: string; onDueDateChange: (date: string) => void; onInsertLink: () => void }) {
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
      <DropdownMenu.Item className={styles.menuItem} disabled><Repeat2/><span>Make recurring…</span></DropdownMenu.Item>
      <DropdownMenu.Item className={styles.menuItem} onSelect={onInsertLink}><Link2/><span>Add link…</span><kbd>Ctrl L</kbd></DropdownMenu.Item>
      <DropdownMenu.Item className={styles.menuItem} disabled><FilePlus2/><span>Add customer request…</span><kbd>Ctrl R</kbd></DropdownMenu.Item>
      <DropdownMenu.Separator className={styles.menuSeparator}/>
      <DropdownMenu.Item className={styles.menuItem} disabled><FilePlus2/><span>Add sub-issue</span><kbd>⌘ ⇧ O</kbd></DropdownMenu.Item>
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
  return <Dialog.Root open={open} onOpenChange={next => { if (!next) onCancel() }}>
    <Dialog.Portal>
      <Dialog.Overlay className={styles.confirmOverlay}/>
      <Dialog.Content className={styles.confirmDialog} aria-describedby="draft-confirm-description" onOpenAutoFocus={event => { event.preventDefault(); saveRef.current?.focus() }} onEscapeKeyDown={event => { event.preventDefault(); onCancel() }} onPointerDownOutside={event => event.preventDefault()}>
        <Dialog.Title>Save to drafts?</Dialog.Title>
        <Dialog.Description id="draft-confirm-description">You can finish this issue later from your drafts.</Dialog.Description>
        <div className={styles.confirmActions}>
          <button type="button" disabled={saving} onClick={onDiscard}>Discard</button>
          <span/>
          <button type="button" disabled={saving} onClick={onCancel}>Cancel</button>
          <button ref={saveRef} type="button" className={styles.confirmSave} disabled={saving} onClick={onSave}>{saving ? 'Saving...' : 'Save'}</button>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
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
  try { globalThis.localStorage?.setItem(key, JSON.stringify(draft)) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

function removeStoredDraft(key: string) {
  try { globalThis.localStorage?.removeItem(key) } catch { /* Draft persistence is best-effort in private browsing. */ }
}

function escapeHtml(value: string) { return value.replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]!) }
function escapeAttribute(value: string) { return escapeHtml(value) }
