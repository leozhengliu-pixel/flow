import * as Dialog from '@radix-ui/react-dialog'
import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import { parseDate } from 'chrono-node'
import { addMonths } from 'date-fns'
import {
  Bell,
  Copy,
  FilePlus2,
  GitPullRequest,
  History,
  Link,
  Link2,
  RefreshCw,
  Repeat2,
  Search,
  Star,
  Trash2,
  UserRoundPlus,
  X,
} from 'lucide-react'
import { createRef, useCallback, useEffect, useMemo, useRef, useState, type ReactNode, type RefObject } from 'react'
import { toast } from 'sonner'

import { DueDateCommand } from '@/components/issue/due-date-picker'
import { FlowOptionsIcon } from '@/components/issue/flow-header-icons'
import { CalendarIcon } from '@/components/issue/issue-icons'
import { ReleasesIcon } from '@/components/releases/release-icons'
import { confirmAction } from '@/components/ui/action-dialog-service'
import { SelectControl } from '@/components/ui/select-control'
import { DateTimeControl } from '@/components/ui/date-time-control'
import './issue-options-select.css'
import './issue-options-menu.css'
import { IssueReleasePicker } from './issue-release-picker'
import { useI18n } from '@/i18n/i18n'
import { IssueActionGlyph } from './issue-action-glyphs'
import type { ActivityEvent, BootstrapData, Issue, IssueRelationType, IssueUpdateInput } from '@/types/flow'

export type RelatedIssueCreationKind = 'issue' | 'sub-issue' | 'parent' | 'blocked' | 'blocking' | 'copy'
export type IssueConversionKind = 'project' | 'template'

export interface IssueOptionsActions {
  makeCopy?: () => Promise<void>
  configureRecurring?: () => void
  addLink: (input: { url: string; title?: string }) => Promise<void>
  addCustomerRequest: (input: { customerId?: string; customerName?: string; body: string }) => Promise<void>
  addDocument: () => Promise<void>
  linkReview: (reviewId: string) => Promise<void>
  unlinkReview: (reviewId: string) => Promise<void>
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

type Submenu = 'release' | 'create' | 'mark' | 'copy' | 'convert' | 'remind'
type DialogName = 'link' | 'customer' | 'review' | 'related' | 'reminder' | 'loop' | 'history' | 'recurrence' | null

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
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [submenu, setSubmenu] = useState<Submenu | null>(null)
  const [datePickerOpen, setDatePickerOpen] = useState(false)
  const [dialog, setDialog] = useState<DialogName>(null)
  const [linkUrl, setLinkUrl] = useState('')
  const [linkTitle, setLinkTitle] = useState('')
  const [customerId, setCustomerId] = useState('')
  const [customerName, setCustomerName] = useState('')
  const [customerBody, setCustomerBody] = useState(issue.title)
  const [reviewQuery, setReviewQuery] = useState('')
  const [relatedKind, setRelatedKind] = useState<RelatedIssueCreationKind>('issue')
  const [relatedTitle, setRelatedTitle] = useState('')
  const [customReminder, setCustomReminder] = useState('')
  const [loopPrompt, setLoopPrompt] = useState(issue.description)
  const [selectedHistoryId, setSelectedHistoryId] = useState('current')
  const [busy, setBusy] = useState(false)
  const [recurrenceDraft, setRecurrenceDraft] = useState<'daily' | 'weekly' | 'monthly'>('weekly')
  const parentFilterRef = useRef<HTMLInputElement>(null)
  const parentMenuRef = useRef<HTMLDivElement>(null)
  const anchors = useMemo(() => Object.fromEntries(['due','release','create','mark','copy','convert','remind'].map(key => [key, createRef<HTMLDivElement>()])) as Record<Submenu | 'due', RefObject<HTMLDivElement | null>>, [])
  const [focusNested, setFocusNested] = useState(false)
  const [highlight, setHighlight] = useState(false)
  const [rootQuery, setRootQuery] = useState('')
  const openNested = (name: Submenu | 'due', focus = false) => {
    setFocusNested(focus)
    setDatePickerOpen(name === 'due')
    setSubmenu(name === 'due' ? null : name)
  }
  const returnToParent = () => {
    setDatePickerOpen(false)
    setSubmenu(null)
    requestAnimationFrame(() => parentFilterRef.current?.focus())
  }

  const history = useMemo(() => descriptionHistory(issue, activities), [activities, issue])
  const selectedHistory = history.find(item => item.id === selectedHistoryId) ?? history[0]
  const confirmDelete = useCallback(async () => {
    if (await confirmAction(`Delete ${issue.identifier}?`,{description:'This cannot be undone.',confirmLabel:'Delete'})) void onDelete()
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
  const subscribed = Boolean(data?.viewer.id && issue.subscriberIds.includes(data.viewer.id))
  const cycles = data?.cycles.filter(cycle => cycle.teamId === issue.team.id).sort((a,b) => a.startsAt.localeCompare(b.startsAt)) ?? []
  const currentCycle = cycles.find(cycle => new Date(cycle.startsAt).getTime() <= Date.now() && new Date(cycle.endsAt).getTime() > Date.now())
  const nextCycle = cycles.find(cycle => new Date(cycle.startsAt).getTime() > Date.now())
  const cycleDates = [currentCycle && {label:t('End of this cycle'),value:currentCycle.endsAt.slice(0,10)},nextCycle && {label:t('End of next cycle'),value:nextCycle.endsAt.slice(0,10)}].filter((item): item is {label:string;value:string} => Boolean(item))
  const toggleSubscription = useCallback(() => {
    if (!data?.viewer.id) return
    const next = subscribed ? issue.subscriberIds.filter(id => id !== data.viewer.id) : [...issue.subscriberIds, data.viewer.id]
    void perform(() => onUpdate({ subscriberIds: next }), subscribed ? 'Unsubscribed from issue' : 'Subscribed to issue')
  }, [data?.viewer.id, issue.subscriberIds, onUpdate, perform, subscribed])

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
      if (!event.metaKey && !event.ctrlKey && !event.altKey && event.shiftKey && key === 'd') {
        event.preventDefault()
        setSubmenu(null)
        setOpen(true)
        setDatePickerOpen(true)
        setFocusNested(true)
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
          setFocusNested(true)
        } else onRemind?.()
      } else if (event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey && key === 's') {
        event.preventDefault()
        toggleSubscription()
      } else if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key === 'Backspace') {
        event.preventDefault()
        confirmDelete()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [actions, beginAddLink, confirmDelete, onFavoriteChange, onRemind, perform, toggleSubscription])

  const url = issueUrl ?? `${window.location.origin}/${data?.workspace.urlKey ?? ''}/issue/${issue.identifier}`
  const titleLink = `[${issue.title}](${url})`
  const issuePrompt = `# ${issue.identifier}: ${issue.title}\n\n${issue.description || 'No description provided.'}\n\nIssue URL: ${url}`

  return <>
    <Popover.Root open={open} onOpenChange={value => {
      setOpen(value)
      setHighlight(false)
      setRootQuery('')
      if (!value) {
        setSubmenu(null)
        setDatePickerOpen(false)
      }
    }}>
      <Popover.Trigger asChild>
        {trigger ?? <button className="issue-header-icon" type="button" aria-label="Issue options"><FlowOptionsIcon/></button>}
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content ref={parentMenuRef} data-flow-motion="floating" className="issue-options-popover" data-highlight={highlight} side="bottom" align="start" sideOffset={3.75} collisionPadding={16} role="dialog" aria-label={t('Issue options')} onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => parentFilterRef.current?.focus()) }} onEscapeKeyDown={closeMenu} onPointerMoveCapture={event => {
          if (!event.currentTarget.contains(event.target as Node)) return
          setHighlight(true)
          const row = (event.target as Element).closest('[cmdk-item]')
          if (row && !row.hasAttribute('data-submenu')) { setSubmenu(null); setDatePickerOpen(false) }
        }} onKeyDownCapture={event => {
          if (!event.currentTarget.contains(event.target as Node)) return
          if (event.key.startsWith('Arrow')) setHighlight(true)
          if (event.key === 'ArrowRight') {
            const name = parentMenuRef.current?.querySelector<HTMLElement>('[cmdk-item][data-selected=true]')?.dataset.submenu as Submenu | 'due' | undefined
            if (name) { event.preventDefault(); event.stopPropagation(); openNested(name, true) }
          }
        }}>
          <Command loop defaultValue="__no_selection__">
            <Command.Input ref={parentFilterRef} className={rootQuery ? 'issue-options-submenu-search' : 'issue-options-filter'} aria-label={t('Filter…')} placeholder={t('Filter…')} value={rootQuery} onValueChange={value => { setRootQuery(value); setHighlight(true); setSubmenu(null); setDatePickerOpen(false) }}/>
            <Command.List>
              <Command.Empty className="issue-options-empty">{t('No results')}</Command.Empty>
              <Option icon={<CalendarIcon/>} label="Due date" shortcut="Shift D" nested="due" anchor={anchors.due} expanded={datePickerOpen} onHover={() => openNested('due')} onSelect={() => openNested('due', true)}/>
              <Option icon={<ReleasesIcon/>} label="Release" shortcut="Option R" nested="release" anchor={anchors.release} expanded={submenu === 'release'} onHover={() => openNested('release')} onSelect={() => openNested('release', true)}/>
              <Option icon={<Link/>} label="Add link..." shortcut="Ctrl L" onSelect={beginAddLink}/>
              <Option icon={<UserRoundPlus/>} label="Add customer request..." shortcut="Ctrl R" onSelect={() => {
                setCustomerId(data?.customers[0]?.id ?? '')
                setCustomerName('')
                setCustomerBody(issue.title)
                openDialog('customer')
              }}/>
              {Boolean(data?.reviews.length) && <Option icon={<GitPullRequest/>} label="Add pull request..." onSelect={() => { setReviewQuery(''); openDialog('review') }}/>}
              <Option icon={<FilePlus2/>} label="Add document..." onSelect={() => actions && void perform(actions.addDocument, 'Document created')}/>
              <Separator/>
              <Option icon={<Repeat2/>} label="Create related" nested="create" anchor={anchors.create} expanded={submenu === 'create'} onHover={() => openNested('create')} onSelect={() => openNested('create', true)}/>
              <Option icon={<Link2/>} label="Mark as" nested="mark" anchor={anchors.mark} expanded={submenu === 'mark'} onHover={() => openNested('mark')} onSelect={() => openNested('mark', true)}/>
              <Separator/>
              <Option icon={<Copy/>} label="Copy" nested="copy" anchor={anchors.copy} expanded={submenu === 'copy'} onHover={() => openNested('copy')} onSelect={() => openNested('copy', true)}/>
              <Option icon={<RefreshCw/>} label="Convert to" nested="convert" anchor={anchors.convert} expanded={submenu === 'convert'} onHover={() => openNested('convert')} onSelect={() => openNested('convert', true)}/>
              {actions && <Option icon={<Copy/>} label="Make a copy…" onSelect={() => actions.makeCopy ? void perform(actions.makeCopy) : beginRelated('copy')}/>}
              <Separator/>
              <Option icon={<Star fill={favorited ? 'currentColor' : 'none'}/>} label={favorited ? 'Remove from favorites' : 'Add to favorites'} shortcut="Option F" onSelect={() => {
                if (actions) void perform(actions.toggleFavorite)
                else {
                  closeMenu()
                  onFavoriteChange?.()
                }
              }}/>
              <Option icon={<Bell/>} label="Remind me" shortcut="Shift H" nested="remind" anchor={anchors.remind} expanded={submenu === 'remind'} onHover={() => { if (actions) openNested('remind') }} onSelect={() => actions ? openNested('remind', true) : (closeMenu(), onRemind?.())}/>
              {data?.viewer.id && issue.creator.id !== data.viewer.id && <Option icon={<Bell/>} label={subscribed ? 'Unsubscribe' : 'Subscribe'} shortcut="Shift S" onSelect={toggleSubscription}/>}
              <Separator/>
              <Option icon={<Repeat2/>} label="Run loop…" onSelect={() => {
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

          {submenu === 'release' && (data ? <IssueReleasePicker data={data} issue={issue} externalAnchor={anchors.release} popoverOpen onPopoverOpenChange={next => !next && returnToParent()} onMenuEscape={closeMenu}/> : <SubmenuSurface label="Release" anchor={anchors.release} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu}><div className="issue-options-empty">{t('No releases')}</div></SubmenuSurface>)}
          {submenu === 'create' && <SubmenuSurface label="Create related" anchor={anchors.create} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu}>
            <Option icon={<FilePlus2/>} label="Issue..." onSelect={() => beginRelated('issue')}/>
            <Option icon={<FilePlus2/>} label="Sub-issue..." shortcut="Command Shift O" onSelect={() => beginRelated('sub-issue')}/>
            <Option icon={<FilePlus2/>} label="Parent issue..." onSelect={() => beginRelated('parent')}/>
            <Option icon={<Link2/>} label="Blocked issue..." onSelect={() => beginRelated('blocked')}/>
            <Option icon={<Link2/>} label="Blocking issue..." onSelect={() => beginRelated('blocking')}/>
          </SubmenuSurface>}
          {submenu === 'mark' && <SubmenuSurface label="Mark as" anchor={anchors.mark} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu}>
            <Option icon={<Link2/>} label="Parent of..." onSelect={() => chooseRelation('parent_of')}/>
            <Option icon={<Link2/>} label="Sub-issue of..." shortcut="Command Shift P" onSelect={() => chooseRelation('sub_issue_of')}/>
            <Option icon={<Link2/>} label="Related to..." shortcut="M, then R" onSelect={() => chooseRelation('related')}/>
            <Option icon={<Link2/>} label="Blocked by..." shortcut="M, then B" onSelect={() => chooseRelation('blocked_by')}/>
            <Option icon={<Link2/>} label="Blocking..." shortcut="M, then X" onSelect={() => chooseRelation('blocks')}/>
            <Option icon={<Copy/>} label="Duplicate of..." shortcut="M, then M" onSelect={() => chooseRelation('duplicate')}/>
          </SubmenuSurface>}
          {submenu === 'copy' && <SubmenuSurface label="Copy" anchor={anchors.copy} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu}>
            <Option icon={<Copy/>} label="Copy ID" shortcut="Command ." onSelect={() => void copy(issue.identifier, 'Issue ID copied to clipboard')}/>
            <Option icon={<Link/>} label="Copy URL" shortcut="Command Shift ," onSelect={() => void copy(url, 'Issue URL copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy title" shortcut="Command Shift '" onSelect={() => void copy(issue.title, 'Issue title copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy title as link" shortcut="Command C" onSelect={() => void copy(titleLink, 'Title link copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy description as Markdown" onSelect={() => void copy(issue.description, 'Description copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy content as Markdown" shortcut="Command Option C" onSelect={() => void copy(`# ${issue.title}\n\n${issue.description}`, 'Content copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy git branch name" shortcut="Command Shift ." onSelect={() => void copy(issueBranchName(issue), 'Branch name copied to clipboard')}/>
            <Option icon={<Copy/>} label="Copy as prompt" shortcut="Command Option P" onSelect={() => void copy(issuePrompt, 'Prompt copied to clipboard')}/>
          </SubmenuSurface>}
          {submenu === 'convert' && <SubmenuSurface label="Convert to" anchor={anchors.convert} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu}>
            <Option icon={<RefreshCw/>} label="Project..." onSelect={() => {
              if (actions) void confirmAction(`Convert ${issue.identifier} to a project?`,{description:'The original issue will be deleted.',confirmLabel:'Convert'}).then(confirmed=>{if(confirmed)return perform(() => actions.convert('project'), 'Converted to project')})
            }}/>
            <Option icon={<RefreshCw/>} label="Template..." onSelect={() => actions && void perform(() => actions.convert('template'), 'Issue template created')}/>
            <Option icon={<Repeat2/>} label="Recurring issue..." onSelect={() => {
              if (actions?.configureRecurring) { closeMenu(); actions.configureRecurring() }
              else { setRecurrenceDraft(issue.recurrence || 'weekly'); openDialog('recurrence') }
            }}/>
          </SubmenuSurface>}
          {submenu === 'remind' && <SubmenuSurface label="Remind me" anchor={anchors.remind} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu} searchable bare>
            <ReminderChoices data={data} teamId={issue.team.id} onChoose={remindAt} onCustom={() => {
              setCustomReminder(toLocalDateTime(reminderDate('tomorrow')))
              openDialog('reminder')
            }}/>
          </SubmenuSurface>}
          {datePickerOpen && <SubmenuSurface label="Due date" anchor={anchors.due} autoFocus={focusNested} onReturn={returnToParent} onCloseAll={closeMenu} bare searchable>
            <DueDateCommand autoFocus={false} extraOptions={cycleDates} value={issue.dueDate} onSelect={async dueDate => {
              await onUpdate({ dueDate })
              closeMenu()
            }}/>
          </SubmenuSurface>}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>

    <ActionDialog open={dialog === 'recurrence'} title={t('Recurring issue…')} onOpenChange={value => !value && setDialog(null)}>
      <SelectControl label={t('Repeat')} value={recurrenceDraft} options={['daily','weekly','monthly'].map(value => ({value,label:t(value[0].toUpperCase()+value.slice(1))}))} onChange={value => setRecurrenceDraft(value as typeof recurrenceDraft)}/>
      <IssueOptionsDialogFooter busy={busy} disabled={!actions} action="Save" onCancel={() => setDialog(null)} onSubmit={() => setRecurrence(recurrenceDraft)}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'link'} title={`Add link to ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>URL<input autoFocus type="url" placeholder="https://..." value={linkUrl} onChange={event => setLinkUrl(event.target.value)}/></label>
      <label>Title <small>(optional)</small><input value={linkTitle} onChange={event => setLinkTitle(event.target.value)}/></label>
      <IssueOptionsDialogFooter busy={busy} disabled={!linkUrl.trim()} action="Add link" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.addLink({ url: linkUrl.trim(), title: linkTitle.trim() || undefined }), 'Link added')}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'customer'} title={`Add customer request to ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      {data?.customers.length ? <label>Customer<SelectControl label="Customer" value={customerId} onChange={setCustomerId} options={[...data.customers.map(customer=>({value:customer.id,label:customer.name,entityName:true})),{value:'',label:'Create new customer...'}]}/></label> : null}
      {!customerId && <label>Customer name<input autoFocus value={customerName} onChange={event => setCustomerName(event.target.value)}/></label>}
      <label>Request<textarea value={customerBody} onChange={event => setCustomerBody(event.target.value)}/></label>
      <IssueOptionsDialogFooter busy={busy} disabled={!customerBody.trim() || (!customerId && !customerName.trim())} action="Add request" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.addCustomerRequest({ customerId: customerId || undefined, customerName: customerName.trim() || undefined, body: customerBody.trim() }), 'Customer request added')}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'review'} title={`Add pull request to ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label className="issue-review-filter"><Search/><input autoFocus value={reviewQuery} placeholder="Filter pull requests…" onChange={event => setReviewQuery(event.target.value)}/></label>
      <div className="issue-review-results" role="listbox">{data?.reviews.filter(review => !review.issueIds.includes(issue.id)).filter(review => `${review.title} ${review.repositoryOwner}/${review.repositoryName}`.toLowerCase().includes(reviewQuery.trim().toLowerCase())).map(review => <button type="button" role="option" key={review.id} disabled={busy} onClick={() => actions && void perform(() => actions.linkReview(review.id), 'Pull request linked')}><GitPullRequest/><span><strong>{review.title}</strong><small>{review.repositoryOwner}/{review.repositoryName} · #{review.number}</small></span></button>)}{!data?.reviews.some(review => !review.issueIds.includes(issue.id) && `${review.title} ${review.repositoryOwner}/${review.repositoryName}`.toLowerCase().includes(reviewQuery.trim().toLowerCase())) && <p>No pull requests found</p>}</div>
      <IssueOptionsDialogFooter busy={busy} disabled={false} action="Close" onCancel={() => setDialog(null)} onSubmit={() => setDialog(null)}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'related'} title={relatedKind === 'copy' ? `Make a copy of ${issue.identifier}` : `Create ${relatedLabel(relatedKind)} for ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>Issue title<input autoFocus value={relatedTitle} onChange={event => setRelatedTitle(event.target.value)}/></label>
      <IssueOptionsDialogFooter busy={busy} disabled={!relatedTitle.trim()} action="Create issue" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.createRelated(relatedKind, relatedTitle.trim()), 'Related issue created')}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'reminder'} title={`Remind me about ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>Date and time<DateTimeControl label="Date and time" min={new Date().toISOString()} mode="datetime" value={customReminder} onChange={setCustomReminder}/></label>
      <IssueOptionsDialogFooter busy={busy} disabled={!customReminder || new Date(customReminder).getTime() <= Date.now()} action="Set reminder" onCancel={() => setDialog(null)} onSubmit={() => remindAt(new Date(customReminder))}/>
    </ActionDialog>
    <ActionDialog open={dialog === 'loop'} title={`Run loop on ${issue.identifier}`} onOpenChange={value => !value && setDialog(null)}>
      <label>Instructions<textarea autoFocus placeholder="What should the loop do?" value={loopPrompt} onChange={event => setLoopPrompt(event.target.value)}/></label>
      <IssueOptionsDialogFooter busy={busy} disabled={!loopPrompt.trim()} action="Run loop" onCancel={() => setDialog(null)} onSubmit={() => actions && void perform(() => actions.runLoop(loopPrompt.trim()), 'Loop run created')}/>
    </ActionDialog>
    <Dialog.Root open={dialog === 'history'} onOpenChange={value => !value && setDialog(null)}>
      <Dialog.Portal>
        <Dialog.Overlay data-flow-motion="backdrop" className="dialog-overlay"/>
        <Dialog.Content data-flow-motion="dialog" className="issue-description-history" aria-label={`Description history for ${issue.identifier}`}>
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
    setRelatedTitle(kind === 'copy' ? issue.title : '')
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

function SubmenuSurface({ label, anchor, children, searchable = false, bare = false, autoFocus, onReturn, onCloseAll }: { label: string; anchor: RefObject<HTMLDivElement | null>; children: ReactNode; searchable?: boolean; bare?: boolean; autoFocus: boolean; onReturn: () => void; onCloseAll: () => void }) {
  const { t } = useI18n()
  const ref = useRef<HTMLDivElement>(null)
  const [highlight, setHighlight] = useState(autoFocus)
  const [query, setQuery] = useState('')
  const [value, setValue] = useState('__no_selection__')
  useEffect(() => { if (autoFocus) { setValue(ref.current?.querySelector('[cmdk-item]')?.getAttribute('data-value') ?? '__no_selection__'); requestAnimationFrame(() => ref.current?.querySelector('input')?.focus()) } }, [autoFocus, ref])
  return <Popover.Root open onOpenChange={next => !next && onReturn()}><Popover.Anchor virtualRef={anchor}/><Popover.Portal><Popover.Content data-flow-motion="floating" className={bare && label === 'Due date' ? 'issue-options-due-date' : 'issue-options-submenu'} ref={ref} data-menu={label} data-highlight={highlight} side="right" align="start" alignOffset={searchable ? -43 : -6.5} sideOffset={-1.5} collisionPadding={16} aria-label={t(label)} onOpenAutoFocus={event => event.preventDefault()} onCloseAutoFocus={event => event.preventDefault()} onFocusOutside={event => event.preventDefault()} onEscapeKeyDown={event => { event.preventDefault(); onCloseAll() }} onPointerMoveCapture={() => setHighlight(true)} onKeyDown={event => {
    if (event.key === 'ArrowLeft') { event.preventDefault(); event.stopPropagation(); onReturn() }
    if (event.key === 'ArrowRight') { const selected = ref.current?.querySelector<HTMLElement>('[cmdk-item][data-selected=true][data-submenu]'); if (selected) { event.preventDefault(); selected.click() } }
    if (event.key.startsWith('Arrow')) setHighlight(true)
  }}>{bare ? children : <Command loop value={value} onValueChange={setValue}><Command.Input className={searchable || query ? 'issue-options-submenu-search' : 'issue-options-filter'} placeholder={t(searchable && label === 'Remind me' ? 'Try: 4 pm, 2 days, in 5 weeks…' : 'Filter…')} aria-label={`${t('Filter')} ${t(label)}`} value={query} onValueChange={setQuery}/><Command.List><Command.Empty className="issue-options-empty">{t('No results')}</Command.Empty>{children}</Command.List></Command>}</Popover.Content></Popover.Portal></Popover.Root>
}

function ActionDialog({ open, title, onOpenChange, children }: { open: boolean; title: string; onOpenChange: (open: boolean) => void; children: ReactNode }) {
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay data-flow-motion="backdrop" className="dialog-overlay"/><Dialog.Content data-flow-motion="dialog" className="issue-action-dialog" aria-label={title}><Dialog.Title>{title}</Dialog.Title>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>
}

function IssueOptionsDialogFooter({ busy, disabled, action, onCancel, onSubmit }: { busy: boolean; disabled: boolean; action: string; onCancel: () => void; onSubmit: () => void }) {
  return <footer><button type="button" onClick={onCancel}>Cancel</button><button type="button" className="primary" disabled={disabled || busy} onClick={onSubmit}>{busy ? 'Working...' : action}</button></footer>
}

function Option({ icon, label, detail, shortcut, nested, anchor, expanded, danger, onHover, onSelect }: { icon: ReactNode; label: string; detail?: string; shortcut?: string; nested?: Submenu | 'due'; anchor?: RefObject<HTMLDivElement | null>; expanded?: boolean; danger?: boolean; onHover?: () => void; onSelect?: () => void }) {
  const { t } = useI18n()
  const text = t(label.replaceAll('...', '…'))
  return <Command.Item ref={anchor} className={`issue-options-item${danger ? ' danger' : ''}`} value={label} keywords={[text]} data-submenu={nested} data-submenu-open={expanded || undefined} aria-expanded={nested ? Boolean(expanded) : undefined} onPointerEnter={onHover} onSelect={onSelect}>
    <span className="issue-options-icon" aria-hidden="true"><IssueActionGlyph label={label} fallback={icon}/></span>
    <span className="issue-options-label">{text}</span>
    {detail && <small className="issue-options-detail">{detail}</small>}
    {shortcut && <IssueOptionsShortcut value={shortcut}/>}
    {nested && <span className="issue-options-chevron" aria-hidden="true">▶</span>}
  </Command.Item>
}

function IssueOptionsShortcut({ value }: { value: string }) {
  const glyph: Record<string,string> = {Command:'⌘',Shift:'⇧',Option:'⌥',Control:'⌃',Ctrl:'⌃',Backspace:'⌫'}
  const keys = value.split(' ').map(key => glyph[key] ?? key.replace(/^M,$/, 'M'))
  return <span className="issue-options-shortcut"><span className="sr-only">{value}</span>{keys.map((key,index) => <kbd key={`${key}-${index}`}>{key}</kbd>)}</span>
}

function Separator() {
  return <div className="issue-options-separator" role="separator"/>
}

function issueBranchName(issue: Issue) {
  const owner = (issue.assignee?.email || issue.creator.email || '').split('@')[0].toLowerCase().replace(/[^a-z0-9]/g, '') || 'user'
  const title = issue.title.normalize('NFC').toLowerCase().replace(/[()[\]{}=]/g, '').replace(/[^\p{L}\p{N}_：:-]+/gu, '-').replace(/^-|-$/g, '')
  return `${owner}/${`${issue.identifier.toLowerCase()}-${title || 'issue'}`.slice(0,100)}`
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
  if (kind === 'week') date.setDate(date.getDate() + ((8 - date.getDay()) % 7 || 7))
  if (kind === 'month') return addMonths(date, 1)
  return date
}

export function ReminderChoices({ data, teamId, onChoose, onCustom }: { data?: BootstrapData; teamId?: string; onChoose: (date: Date) => void; onCustom: () => void }) {
  const { t, locale } = useI18n()
  const [query, setQuery] = useState('')
  const nextCycle = data?.cycles.filter(cycle => cycle.teamId === teamId && new Date(cycle.startsAt).getTime() > Date.now()).sort((a,b) => a.startsAt.localeCompare(b.startsAt))[0]
  const options = [
    {label:'An hour from now',date:reminderDate('hour')}, {label:'Tomorrow',date:reminderDate('tomorrow')},
    {label:'Next week',date:reminderDate('week')}, {label:'A month from now',date:reminderDate('month')},
    ...(nextCycle ? [{label:'Next cycle',date:new Date(nextCycle.startsAt)}] : []),
  ]
  const matches = options.filter(option => `${option.label} ${t(option.label)}`.toLowerCase().includes(query.toLowerCase()))
  const parsed = query.trim() && !matches.length ? parseDate(query, new Date(), {forwardDate:true}) : null
  const format = (date: Date) => date.toLocaleString(locale, {weekday:'short',month:'short',day:'numeric',hour:'numeric',minute:'2-digit'})
  return <Command loop defaultValue="__no_selection__" shouldFilter={false}><Command.Input className="issue-options-submenu-search" aria-label={t('Remind me')} placeholder={t('Try: 4 pm, 2 days, in 5 weeks…')} value={query} onValueChange={setQuery}/><Command.List>{matches.map(option => <Option key={option.label} icon={<CalendarIcon/>} label={option.label} detail={format(option.date)} onSelect={() => onChoose(option.date)}/>)}{parsed && parsed.getTime() > Date.now() && <Option icon={<CalendarIcon/>} label={format(parsed)} onSelect={() => onChoose(parsed)}/>}<Option icon={<CalendarIcon/>} label="Custom…" onSelect={onCustom}/></Command.List></Command>
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
