import * as Dialog from '@radix-ui/react-dialog'
import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import {
  Bell,
  CalendarDays,
  Check,
  ChevronRight,
  Copy,
  FilePlus2,
  History,
  Link,
  Link2,
  RefreshCw,
  Repeat2,
  Rocket,
  Star,
  Trash2,
  UserRoundPlus,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'

import { DueDateCommand } from '@/components/issue/due-date-picker'
import { FlowOptionsIcon } from '@/components/issue/flow-header-icons'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import type { ActivityEvent, BootstrapData, Issue, IssueRelationType, IssueUpdateInput } from '@/types/flow'

export type RelatedIssueCreationKind = 'issue' | 'sub-issue' | 'parent' | 'blocked' | 'blocking'
export type IssueConversionKind = 'project' | 'template'

export interface IssueOptionsActions {
  addLink: (input: { url: string; title?: string }) => Promise<void>
  addCustomerRequest: (input: { customerId?: string; customerName?: string; body: string }) => Promise<void>
  addDocument: () => Promise<void>
  toggleRelease: (releaseId: string) => Promise<void>
  createRelated: (kind: RelatedIssueCreationKind, title: string) => Promise<void>
  convert: (kind: IssueConversionKind) => Promise<void>
  setRecurring: (recurrence: 'daily' | 'weekly' | 'monthly') => Promise<void>
  toggleFavorite: () => Promise<void>
  remind: (remindAt: string) => Promise<void>
  runLoop: (prompt: string) => Promise<void>
  restoreDescription: (description: string, descriptionState?: string) => Promise<void>
}

interface IssueOptionsMenuProps {
  issue: Issue
  onUpdate: (input: IssueUpdateInput) => Promise<void>
  onDelete: () => Promise<void>
  onRelation: (type: IssueRelationType) => void
  trigger?: ReactNode
  issueUrl?: string
  data?: BootstrapData
  activities?: ActivityEvent[]
  actions?: IssueOptionsActions
  favorited?: boolean
  onFavoriteChange?: () => void
  onRemind?: () => void
  onShowDescriptionHistory?: () => void
}

type Submenu = 'release' | 'create' | 'mark' | 'copy' | 'convert' | 'recurrence' | 'remind'
type DialogName = 'link' | 'customer' | 'related' | 'reminder' | 'loop' | 'history' | null

export function IssueOptionsMenu({
  issue,
  onUpdate,
  onDelete,
  onRelation,
  trigger,
  issueUrl,
  data,
  activities = [],
  actions,
  favorited = false,
  onFavoriteChange,
  onRemind,
  onShowDescriptionHistory,
}: IssueOptionsMenuProps) {
  const [open, setOpen] = useState(false)
  const [submenu, setSubmenu] = useState<Submenu | null>(null)
  const [datePickerOpen, setDatePickerOpen] = useState(false)
  const [dialog, setDialog] = useState<DialogName>(null)
  const [linkUrl, setLinkUrl] = useState('')
  const [linkTitle, setLinkTitle] = useState('')
  const [customerId, setCustomerId] = useState('')
  const [customerName, setCustomerName] = useState('')
  const [customerBody, setCustomerBody] = useState(issue.title)
  const [relatedKind, setRelatedKind] = useState<RelatedIssueCreationKind>('issue')
  const [relatedTitle, setRelatedTitle] = useState('')
  const [customReminder, setCustomReminder] = useState('')
  const [loopPrompt, setLoopPrompt] = useState(issue.description)
  const [selectedHistoryId, setSelectedHistoryId] = useState('current')
  const [busy, setBusy] = useState(false)
  const nestedSurfaceRef = useRef<HTMLDivElement>(null)
  const parentFilterRef = useRef<HTMLInputElement>(null)

  const history = useMemo(() => descriptionHistory(issue, activities), [activities, issue])
  const selectedHistory = history.find(item => item.id === selectedHistoryId) ?? history[0]
  const confirmDelete = useCallback(() => {
    if (window.confirm(`Delete ${issue.identifier}? This cannot be undone.`)) void onDelete()
  }, [issue.identifier, onDelete])

  const closeMenu = useCallback(() => {
    setOpen(false)
    setSubmenu(null)
    setDatePickerOpen(false)
  }, [])
  const openDialog = useCallback((name: Exclude<DialogName, null>) => {
    closeMenu()
    setDialog(name)
  }, [closeMenu])
  const chooseRelation = (type: IssueRelationType) => {
    closeMenu()
    window.requestAnimationFrame(() => onRelation(type))
  }
  const perform = useCallback(async (work: () => Promise<void>, success?: string) => {
    if (busy) return
    setBusy(true)
    try {
      await work()
      if (success) toast.success(success)
      setDialog(null)
      closeMenu()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : 'Action failed')
    } finally {
      setBusy(false)
    }
  }, [busy, closeMenu])

  const beginAddLink = useCallback(() => {
    setLinkUrl('')
    setLinkTitle('')
    openDialog('link')
  }, [openDialog])

  const copy = async (value: string, message: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(message)
      closeMenu()
    } catch {
      toast.error('Could not write to clipboard')
    }
  }

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (isEditableTarget(event.target)) return
      const key = event.key.toLowerCase()
      if (event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey && key === 'd') {
        event.preventDefault()
        setSubmenu(null)
        setOpen(true)
        setDatePickerOpen(true)
      } else if ((event.ctrlKey || event.metaKey) && !event.shiftKey && key === 'l') {
        event.preventDefault()
        beginAddLink()
      } else if (event.altKey && !event.metaKey && !event.ctrlKey && key === 'f') {
        event.preventDefault()
        if (actions) void perform(actions.toggleFavorite)
        else onFavoriteChange?.()
      } else if (event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey && key === 'h') {
        event.preventDefault()
        if (actions) {
          setOpen(true)
          setSubmenu('remind')
        } else onRemind?.()
      } else if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key === 'Backspace') {
        event.preventDefault()
        confirmDelete()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [actions, beginAddLink, confirmDelete, onFavoriteChange, onRemind, perform])

  const url = issueUrl ?? `${window.location.origin}/${data?.workspace.urlKey ?? ''}/issue/${issue.identifier}`
  const titleLink = `[${issue.title}](${url})`
  const issuePrompt = `# ${issue.identifier}: ${issue.title}\n\n${issue.description || 'No description provided.'}\n\nIssue URL: ${url}`
  useDismissibleLayer({
    open: submenu !== null || datePickerOpen,
    refs: [nestedSurfaceRef],
    onDismiss: () => {
      setSubmenu(null)
      setDatePickerOpen(false)
    },
    restoreFocusRef: parentFilterRef,
  })

  return <>
    <Popover.Root open={open} onOpenChange={value => {
      setOpen(value)
      if (!value) {
        setSubmenu(null)
        setDatePickerOpen(false)
      }
    }}>
      <Popover.Trigger asChild>
        {trigger ?? <button className="issue-header-icon" type="button" aria-label="Issue options"><FlowOptionsIcon/></button>}
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content className="issue-options-popover" side="bottom" align="start" sideOffset={3.75} collisionPadding={{ right: 11 }} role="dialog" aria-label="Issue options" onOpenAutoFocus={event => event.preventDefault()} onCloseAutoFocus={event => event.preventDefault()}>
          <Command loop>
            <Command.Input ref={parentFilterRef} className="issue-options-filter" aria-label="Filter..." placeholder="Filter..." autoFocus/>
            <Command.List>
              <Command.Empty className="issue-options-empty">No results found.</Command.Empty>
              <Option icon={<CalendarDays/>} label="Due date" shortcut="Shift D" nested onSelect={() => { setSubmenu(null); setDatePickerOpen(true) }}/>
              <Option icon={<Rocket/>} label="Release" shortcut="Option R" nested onSelect={() => setSubmenu('release')}/>
              <Option icon={<Link/>} label="Add link..." shortcut="Ctrl L" onSelect={beginAddLink}/>
              <Option icon={<UserRoundPlus/>} label="Add customer request..." shortcut="Ctrl R" onSelect={() => {
                setCustomerId(data?.customers[0]?.id ?? '')
                setCustomerName('')
                setCustomerBody(issue.title)
                openDialog('customer')
              }}/>
              <Option icon={<FilePlus2/>} label="Add document..." onSelect={() => actions && void perform(actions.addDocument, 'Document created')}/>
              <Separator/>
              <Option icon={<Repeat2/>} label="Create related" nested onSelect={() => setSubmenu('create')}/>
              <Option icon={<Link2/>} label="Mark as" nested onSelect={() => setSubmenu('mark')}/>
              <Separator/>
              <Option icon={<Copy/>} label="Copy" nested onSelect={() => setSubmenu('copy')}/>
              <Option icon={<RefreshCw/>} label="Convert to" nested onSelect={() => setSubmenu('convert')}/>
              <Separator/>
              <Option icon={<Star fill={favorited ? 'currentColor' : 'none'}/>} label={favorited ? 'Unfavorite' : 'Favorite'} shortcut="Option F" onSelect={() => {
                if (actions) void perform(actions.toggleFavorite)
                else {
                  closeMenu()
                  onFavoriteChange?.()
                }
              }}/>
              <Option icon={<Bell/>} label="Remind me" shortcut="Shift H" nested onSelect={() => actions ? setSubmenu('remind') : (closeMenu(), onRemind?.())}/>
              <Separator/>
              <Option icon={<Repeat2/>} label={`Run loop on ${issue.identifier}...`} onSelect={() => {
                setLoopPrompt(issue.description)
                openDialog('loop')
              }}/>
              <Separator/>
              <Option icon={<History/>} label="Show description history" onSelect={() => {
                if (onShowDescriptionHistory && !actions) {
                  closeMenu()
                  onShowDescriptionHistory()
                } else {
                  setSelectedHistoryId('current')
                  openDialog('history')
                }
              }}/>
              <Option danger icon={<Trash2/>} label="Delete" shortcut="Command Backspace" onSelect={() => { closeMenu(); confirmDelete() }}/>
            </Command.List>
          </Command>

          {submenu === 'release' && <SubmenuSurface label="Release" top={38} innerRef={nestedSurfaceRef} searchable>
            {data?.releases.length ? data.releases.map(release => {
              const selected = release.issueIds.includes(issue.id)
              return <Option icon={selected ? <Check/> : <span/>} key={release.id} label={`${release.name}${release.version ? ` · ${release.version}` : ''}`} onSelect={() => actions && void perform(() => actions.toggleRelease(release.id))}/>
            }) : <div className="issue-options-empty">No releases</div>}
          </SubmenuSurface>}
          {submenu === 'create' && <SubmenuSurface label="Create related" top={166} innerRef={nestedSurfaceRef}>
            <Option icon={<FilePlus2/>} label="Issue..." onSelect={() => beginRelated('issue')}/>
            <Option icon={<FilePlus2/>} label="Sub-issue..." shortcut="Command Shift O" onSelect={() => beginRelated('sub-issue')}/>
            <Option icon={<FilePlus2/>} label="Parent issue..." onSelect={() => beginRelated('parent')}/>
            <Option icon={<Link2/>} label="Blocked issue..." onSelect={() => beginRelated('blocked')}/>
            <Option icon={<Link2/>} label="Blocking issue..." onSelect={() => beginRelated('blocking')}/>
          </SubmenuSurface>}
          {submenu === 'mark' && <SubmenuSurface label="Mark as" top={198} innerRef={nestedSurfaceRef}>
            <Option icon={<Link2/>} label="Parent of..." onSelect={() => chooseRelation('parent_of')}/>
            <Option icon={<Link2/>} label="Sub-issue of..." shortcut="Command Shift P" onSelect={() => chooseRelation('sub_issue_of')}/>
            <Option icon={<Link2/>} label="Related to..." shortcut="M, then R" onSelect={() => chooseRelation('related')}/>
            <Option icon={<Link2/>} label="Blocked by..." shortcut="M, then B" onSelect={() => chooseRelation('blocked_by')}/>
            <Option icon={<Link2/>} label="Blocking..." shortcut="M, then X" onSelect={() => chooseRelation('blocks')}/>
            <Option icon={<Copy/>} label="Duplicate of..." shortcut="M, then M" onSelect={() => chooseRelation('duplicate')}/>
          </SubmenuSurface>}
          {submenu === 'copy' && <SubmenuSurface label="Copy" top={250} innerRef={nestedSurfaceRef}>
            <Option icon={<Link/>} label="Copy issue URL" onSelect={() => void copy(url, 'Issue URL copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy issue ID" onSelect={() => void copy(issue.identifier, 'Issue ID copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy issue title" onSelect={() => void copy(issue.title, 'Issue title copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy title as link" onSelect={() => void copy(titleLink, 'Title link copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy description as Markdown" onSelect={() => void copy(issue.description, 'Description copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy as prompt" onSelect={() => void copy(issuePrompt, 'Prompt copied to clipboard')}/>
          </SubmenuSurface>}
          {submenu === 'convert' && <SubmenuSurface label="Convert to" top={282} innerRef={nestedSurfaceRef}>
            <Option icon={<RefreshCw/>} label="Project..." onSelect={() => {
              if (actions && window.confirm(`Convert ${issue.identifier} to a project? The original issue will be deleted.`)) void perform(() => actions.convert('project'), 'Converted to project')
            }}/>
            <Option icon={<RefreshCw/>} label="Template..." onSelect={() => actions && void perform(() => actions.convert('template'), 'Issue template created')}/>
            <Option icon={<Repeat2/>} label="Recurring issue..." onSelect={() => setSubmenu('recurrence')}/>
          </SubmenuSurface>}
          {submenu === 'recurrence' && <SubmenuSurface label="Recurring issue" top={282} innerRef={nestedSurfaceRef}>
            <Option icon={<Repeat2/>} label="Daily" detail={issue.recurrence === 'daily' ? 'Selected' : undefined} onSelect={() => setRecurrence('daily')}/>
            <Option icon={<Repeat2/>} label="Weekly" detail={issue.recurrence === 'weekly' ? 'Selected' : undefined} onSelect={() => setRecurrence('weekly')}/>
            <Option icon={<Repeat2/>} label="Monthly" detail={issue.recurrence === 'monthly' ? 'Selected' : undefined} onSelect={() => setRecurrence('monthly')}/>
          </SubmenuSurface>}
          {submenu === 'remind' && <SubmenuSurface label="Remind me" top={354} innerRef={nestedSurfaceRef}>
            <Option icon={<CalendarDays/>} label="An hour from now" detail={formatDate(reminderDate('hour'))} onSelect={() => remindAt(reminderDate('hour'))}/>
            <Option icon={<CalendarDays/>} label="Tomorrow" detail={formatDate(reminderDate('tomorrow'))} onSelect={() => remindAt(reminderDate('tomorrow'))}/>
            <Option icon={<CalendarDays/>} label="Next week" detail={formatDate(reminderDate('week'))} onSelect={() => remindAt(reminderDate('week'))}/>
            <Option icon={<CalendarDays/>} label="A month from now" detail={formatDate(reminderDate('month'))} onSelect={() => remindAt(reminderDate('month'))}/>
            <Option icon={<CalendarDays/>} label="Custom..." onSelect={() => {
              setCustomReminder(toLocalDateTime(reminderDate('tomorrow')))
              openDialog('reminder')
            }}/>
          </SubmenuSurface>}
          {datePickerOpen && <div className="issue-options-due-date" ref={nestedSurfaceRef} role="dialog" aria-label="Due date">
            <DueDateCommand value={issue.dueDate} onSelect={async dueDate => {
              await onUpdate({ dueDate })
              closeMenu()
            }}/>
          </div>}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>

    <ActionDialog open={dialog === 'link'} title={`Add link to ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>URL<input autoFocus type="url" placeholder="https://..." value={linkUrl} onChange={event => setLinkUrl(event.target.value)}/></label>
      <label>Title <small>(optional)</small><input value={linkTitle} onChange={event => setLinkTitle(event.target.value)}/></label>
      <DialogFooter busy={busy} disabled={!linkUrl.trim()} action="Add link" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.addLink({ url: linkUrl.trim(), title: linkTitle.trim() || undefined }), 'Link added')}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'customer'} title={`Add customer request to ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      {data?.customers.length ? <label>Customer<select value={customerId} onChange={event => setCustomerId(event.target.value)}>{data.customers.map(customer => <option value={customer.id} key={customer.id}>{customer.name}</option>)}<option value="">Create new customer...</option></select></label> : null}
      {!customerId && <label>Customer name<input autoFocus value={customerName} onChange={event => setCustomerName(event.target.value)}/></label>}
      <label>Request<textarea value={customerBody} onChange={event => setCustomerBody(event.target.value)}/></label>
      <DialogFooter busy={busy} disabled={!customerBody.trim() || (!customerId && !customerName.trim())} action="Add request" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.addCustomerRequest({ customerId: customerId || undefined, customerName: customerName.trim() || undefined, body: customerBody.trim() }), 'Customer request added')}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'related'} title={`Create ${relatedLabel(relatedKind)} for ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>Issue title<input autoFocus value={relatedTitle} onChange={event => setRelatedTitle(event.target.value)}/></label>
      <DialogFooter busy={busy} disabled={!relatedTitle.trim()} action="Create issue" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.createRelated(relatedKind, relatedTitle.trim()), 'Related issue created')}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'reminder'} title={`Remind me about ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>Date and time<input autoFocus type="datetime-local" value={customReminder} onChange={event => setCustomReminder(event.target.value)}/></label>
      <DialogFooter busy={busy} disabled={!customReminder || new Date(customReminder).getTime() <= Date.now()} action="Set reminder" onCancel={() => setDialog(null)} onSubmit={() => remindAt(new Date(customReminder))}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'loop'} title={`Run loop on ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>Instructions<textarea autoFocus placeholder="What should the loop do?" value={loopPrompt} onChange={event => setLoopPrompt(event.target.value)}/></label>
      <DialogFooter busy={busy} disabled={!loopPrompt.trim()} action="Run loop" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.runLoop(loopPrompt.trim()), 'Loop run created')}/>
    </ActionDialog>
    <Dialog.Root open={dialog === 'history'} onOpenChange={value => !value && setDialog(null)}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay"/>
        <Dialog.Content className="issue-description-history" aria-label={`Description history for ${issue.identifier}`}>
          <Dialog.Title>Restore version for {issue.identifier} {issue.title}</Dialog.Title>
          <button className="issue-description-history__close" aria-label="Close modal dialog" onClick={() => setDialog(null)}><X size={15}/></button>
          <div className="issue-description-history__body">
            <nav>{history.map(version => <button className={version.id === selectedHistory?.id ? 'selected' : ''} key={version.id} onClick={() => setSelectedHistoryId(version.id)}><strong>{version.id === 'current' ? 'Current' : new Date(version.createdAt).toLocaleString()}</strong><small>{version.actor}</small></button>)}</nav>
            <section><time>{new Date(selectedHistory.createdAt).toLocaleString()}</time><pre>{selectedHistory.description || 'No description'}</pre><button disabled={selectedHistory.id === 'current' || busy} onClick={() => actions && void perform(() => actions.restoreDescription(selectedHistory.description, selectedHistory.descriptionState), 'Description restored')}>Restore version</button></section>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </>

  function beginRelated(kind: RelatedIssueCreationKind) {
    setRelatedKind(kind)
    setRelatedTitle('')
    openDialog('related')
  }
  function remindAt(date: Date) {
    if (!actions) return
    void perform(() => actions.remind(date.toISOString()), 'Reminder set')
  }
  function setRecurrence(value: 'daily' | 'weekly' | 'monthly') {
    if (!actions) return
    void perform(() => actions.setRecurring(value), 'Recurring issue enabled')
  }
}

const SubmenuSurface = ({ label, top, children, innerRef, searchable = false }: { label: string; top: number; children: ReactNode; innerRef: React.Ref<HTMLDivElement>; searchable?: boolean }) => <div className="issue-options-submenu" ref={innerRef} role="dialog" aria-label={label} style={{ top }}><Command loop><Command.Input className={searchable ? 'issue-options-submenu-search' : 'issue-options-filter'} placeholder={searchable ? `Search ${label.toLowerCase()}...` : undefined} aria-label={`Filter ${label.toLowerCase()}`}/><Command.List>{children}</Command.List></Command></div>

function ActionDialog({ open, title, onOpenChange, children }: { open: boolean; title: string; onOpenChange: (open: boolean) => void; children: ReactNode }) {
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay"/><Dialog.Content className="issue-action-dialog" aria-label={title}><Dialog.Title>{title}</Dialog.Title>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>
}

function DialogFooter({ busy, disabled, action, onCancel, onSubmit }: { busy: boolean; disabled: boolean; action: string; onCancel: () => void; onSubmit: () => void }) {
  return <footer><button type="button" onClick={onCancel}>Cancel</button><button type="button" className="primary" disabled={disabled || busy} onClick={onSubmit}>{busy ? 'Working...' : action}</button></footer>
}

function Option({ icon, label, detail, shortcut, nested, danger, onSelect }: { icon: ReactNode; label: string; detail?: string; shortcut?: string; nested?: boolean; danger?: boolean; onSelect?: () => void }) {
  return <Command.Item className={`issue-options-item${danger ? ' danger' : ''}`} value={`${label} ${detail ?? ''}`} onSelect={onSelect}>
    <span className="issue-options-icon" aria-hidden="true">{icon}</span>
    <span className="issue-options-label">{label}</span>
    {detail && <small className="issue-options-detail">{detail}</small>}
    {shortcut && <Shortcut value={shortcut}/>}
    {nested && <ChevronRight className="issue-options-chevron" size={12}/>}
  </Command.Item>
}

function Shortcut({ value }: { value: string }) {
  const keys = value === 'Shift D' ? ['⇧', 'D'] : value === 'Option R' ? ['⌥', 'R'] : value === 'Ctrl L' ? ['Ctrl', 'L'] : value === 'Ctrl R' ? ['Ctrl', 'R'] : value === 'Option F' ? ['⌥', 'F'] : value === 'Shift H' ? ['⇧', 'H'] : value === 'Command Shift O' ? ['⌘', '⇧', 'O'] : value === 'Command Shift P' ? ['⌘', '⇧', 'P'] : value === 'M, then R' ? ['M', 'then', 'R'] : value === 'M, then B' ? ['M', 'then', 'B'] : value === 'M, then X' ? ['M', 'then', 'X'] : value === 'M, then M' ? ['M', 'then', 'M'] : ['⌘', '⌫']
  return <span className="issue-options-shortcut"><span className="sr-only">{value}</span>{keys.map((key,index) => <kbd key={`${key}-${index}`}>{key}</kbd>)}</span>
}

function Separator() {
  return <div className="issue-options-separator" role="separator"/>
}

function descriptionHistory(issue: Issue, activities: ActivityEvent[]) {
  const previous = activities
    .filter(activity => activity.metadata.descriptionBefore !== undefined)
    .sort((left, right) => new Date(right.createdAt).getTime() - new Date(left.createdAt).getTime())
    .map(activity => ({ id: activity.id, createdAt: activity.createdAt, actor: activity.actor.displayName, description: activity.metadata.descriptionBefore, descriptionState: activity.metadata.descriptionStateBefore }))
  return [{ id: 'current', createdAt: issue.updatedAt, actor: issue.creator.displayName, description: issue.description, descriptionState: issue.descriptionState }, ...previous]
}

function reminderDate(kind: 'hour' | 'tomorrow' | 'week' | 'month') {
  const date = new Date()
  if (kind === 'hour') return new Date(date.getTime() + 60 * 60 * 1000)
  date.setHours(9, 0, 0, 0)
  if (kind === 'tomorrow') date.setDate(date.getDate() + 1)
  if (kind === 'week') date.setDate(date.getDate() + 7)
  if (kind === 'month') date.setMonth(date.getMonth() + 1)
  return date
}

function formatDate(date: Date) {
  return date.toLocaleString(undefined, { weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })
}

function toLocalDateTime(date: Date) {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

function relatedLabel(kind: RelatedIssueCreationKind) {
  return kind === 'sub-issue' ? 'sub-issue' : kind === 'parent' ? 'parent issue' : kind === 'blocked' ? 'blocked issue' : kind === 'blocking' ? 'blocking issue' : 'related issue'
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || (target instanceof HTMLElement && target.isContentEditable)
}
