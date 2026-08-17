import * as Popover from '@radix-ui/react-popover'
import { Command } from 'cmdk'
import {
  CalendarDays,
  ChevronRight,
  Copy,
  FilePlus2,
  History,
  Link,
  Link2,
  RefreshCw,
  Repeat2,
  Star,
  Trash2,
  UserRoundPlus,
} from 'lucide-react'
import * as Dialog from '@radix-ui/react-dialog'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { toast } from 'sonner'
import type { Issue, IssueRelationType, IssueUpdateInput } from '@/types/flow'
import { FlowOptionsIcon } from '@/components/issue/flow-header-icons'
import { DueDateCommand } from '@/components/issue/due-date-picker'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'

interface IssueOptionsMenuProps {
  issue: Issue
  onUpdate: (input: IssueUpdateInput) => Promise<void>
  onDelete: () => Promise<void>
  onRelation: (type: IssueRelationType) => void
  /** Lets Inbox reuse the measured 28px header control instead of a second icon implementation. */
  trigger?: ReactNode
  issueUrl?: string
  onFavoriteChange?: () => void
  onRemind?: () => void
  onShowDescriptionHistory?: () => void
}

export function IssueOptionsMenu({ issue, onUpdate, onDelete, onRelation, trigger, issueUrl, onFavoriteChange, onRemind, onShowDescriptionHistory }: IssueOptionsMenuProps) {
  const [open, setOpen] = useState(false)
  const [submenu, setSubmenu] = useState<'mark' | 'copy' | null>(null)
  const [datePickerOpen, setDatePickerOpen] = useState(false)
  const [linkOpen, setLinkOpen] = useState(false)
  const [linkUrl, setLinkUrl] = useState('')
  const [linkTitle, setLinkTitle] = useState('')
  const [savingLink, setSavingLink] = useState(false)
  const nestedSurfaceRef = useRef<HTMLDivElement>(null)
  const parentFilterRef = useRef<HTMLInputElement>(null)
  const confirmDelete = useCallback(() => {
    if (window.confirm(`Delete ${issue.identifier}? This cannot be undone.`)) void onDelete()
  }, [issue.identifier, onDelete])

  const chooseRelation = (type: IssueRelationType) => {
    setOpen(false)
    setSubmenu(null)
    window.requestAnimationFrame(() => onRelation(type))
  }

  const run = (action?: () => void) => {
    setOpen(false)
    setSubmenu(null)
    action?.()
  }

  const beginAddLink = useCallback(() => {
    setOpen(false)
    setSubmenu(null)
    setLinkUrl('')
    setLinkTitle('')
    setLinkOpen(true)
  }, [])

  const addLink = async () => {
    const url = linkUrl.trim()
    if (!url || savingLink) return
    setSavingLink(true)
    try {
      const label = linkTitle.trim() || url
      const current = issue.description.trim()
      await onUpdate({ description: `${current}${current ? '\n\n' : ''}[${label}](${url})` })
      setLinkOpen(false)
      toast.success('Link added to issue description')
    } catch {
      toast.error('Could not add link')
    } finally {
      setSavingLink(false)
    }
  }

  const copy = async (value: string, message: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(message)
      setOpen(false)
      setSubmenu(null)
    } catch {
      toast.error('Could not write to clipboard')
    }
  }

  const unsupported = (label: string) => {
    setOpen(false)
    setSubmenu(null)
    toast(label, { description: 'This action requires an external Flow integration.' })
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
      } else if (event.altKey && !event.metaKey && !event.ctrlKey && key === 'f' && onFavoriteChange) {
        event.preventDefault()
        onFavoriteChange()
      } else if (event.shiftKey && !event.metaKey && !event.ctrlKey && !event.altKey && key === 'h' && onRemind) {
        event.preventDefault()
        onRemind()
      } else if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key === 'Backspace') {
        event.preventDefault()
        confirmDelete()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [beginAddLink, confirmDelete, onFavoriteChange, onRemind])

  const url = issueUrl ?? `${window.location.origin}/issue/${issue.identifier}`
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
              <Option icon={<Link/>} label="Add link..." shortcut="Ctrl L" onSelect={beginAddLink}/>
              <Option icon={<UserRoundPlus/>} label="Add customer request..." shortcut="Ctrl R" onSelect={() => unsupported('Customer requests are not connected')}/>
              <Option icon={<FilePlus2/>} label="Add document..." onSelect={() => unsupported('Documents are not connected')}/>
              <Separator/>
              <Option icon={<Repeat2/>} label="Create related" nested onSelect={() => chooseRelation('related')}/>
              <Option icon={<Link2/>} label="Mark as" nested onSelect={() => setSubmenu('mark')}/>
              <Separator/>
              <Option icon={<Copy/>} label="Copy" nested onSelect={() => setSubmenu('copy')}/>
              <Option icon={<RefreshCw/>} label="Convert to" nested onSelect={() => unsupported('Issue conversion is not connected')}/>
              <Separator/>
              <Option icon={<Star/>} label="Favorite" shortcut="Option F" onSelect={() => run(onFavoriteChange)}/>
              <Option icon={<CalendarDays/>} label="Remind me" shortcut="Shift H" nested onSelect={() => run(onRemind)}/>
              <Separator/>
              <Option icon={<Repeat2/>} label={`Run loop on ${issue.identifier}...`} onSelect={() => unsupported('Loops are not connected')}/>
              <Separator/>
              <Option icon={<History/>} label="Show description history" onSelect={() => run(onShowDescriptionHistory)}/>
              <Option danger icon={<Trash2/>} label="Delete" shortcut="Command Backspace" onSelect={() => run(confirmDelete)}/>
            </Command.List>
          </Command>
          {submenu === 'mark' && <div className="issue-options-submenu" ref={nestedSurfaceRef} role="dialog" aria-label="Mark as">
            <Command loop>
              <Command.Input className="issue-options-filter" aria-label="Filter relations"/>
              <Command.List>
                <Option icon={<Link2/>} label="Parent of..." onSelect={() => chooseRelation('parent_of')}/>
                <Option icon={<Link2/>} label="Sub-issue of..." shortcut="Command Shift P" onSelect={() => chooseRelation('sub_issue_of')}/>
                <Option icon={<Link2/>} label="Related to..." shortcut="M, then R" onSelect={() => chooseRelation('related')}/>
                <Option icon={<Link2/>} label="Blocked..." shortcut="M, then B" onSelect={() => chooseRelation('blocked_by')}/>
                <Option icon={<Copy/>} label="Duplicate..." shortcut="M, then M" onSelect={() => chooseRelation('duplicate')}/>
              </Command.List>
            </Command>
          </div>}
          {submenu === 'copy' && <div className="issue-options-submenu issue-options-copy-submenu" ref={nestedSurfaceRef} role="dialog" aria-label="Copy">
            <Command loop>
              <Command.Input className="issue-options-filter" aria-label="Filter copy actions"/>
              <Command.List>
                <Option icon={<Link/>} label="Copy issue URL" onSelect={() => void copy(url, 'Issue URL copied to clipboard')}/>
                <Option icon={<Copy/>} label="Copy issue ID" onSelect={() => void copy(issue.identifier, 'Issue ID copied to clipboard')}/>
                <Option icon={<Copy/>} label="Copy issue title" onSelect={() => void copy(issue.title, 'Issue title copied to clipboard')}/>
                <Option icon={<Copy/>} label="Copy title as link" onSelect={() => void copy(titleLink, 'Title link copied to clipboard')}/>
                <Option icon={<Copy/>} label="Copy description as Markdown" onSelect={() => void copy(issue.description, 'Description copied to clipboard')}/>
                <Option icon={<Copy/>} label="Copy as prompt" onSelect={() => void copy(issuePrompt, 'Prompt copied to clipboard')}/>
              </Command.List>
            </Command>
          </div>}
          {datePickerOpen && <div className="issue-options-due-date" ref={nestedSurfaceRef} role="dialog" aria-label="Due date">
            <DueDateCommand value={issue.dueDate} onSelect={async dueDate => {
              await onUpdate({ dueDate })
              setDatePickerOpen(false)
              setOpen(false)
            }}/>
          </div>}
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
    <Dialog.Root open={linkOpen} onOpenChange={setLinkOpen}>
      <Dialog.Portal>
        <Dialog.Overlay className="dialog-overlay"/>
        <Dialog.Content className="issue-link-dialog" aria-label={`Add link to ${issue.identifier}`}>
          <Dialog.Title>Add link to {issue.identifier}</Dialog.Title>
          <label>URL<input autoFocus type="url" placeholder="https://" value={linkUrl} onChange={event => setLinkUrl(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); void addLink() } }}/></label>
          <label>Title<input placeholder="Optional title" value={linkTitle} onChange={event => setLinkTitle(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); void addLink() } }}/></label>
          <footer><button type="button" onClick={() => setLinkOpen(false)}>Cancel</button><button type="button" className="primary" disabled={!linkUrl.trim() || savingLink} onClick={() => void addLink()}>{savingLink ? 'Adding...' : 'Add link'}</button></footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </>
}

function Option({ icon, label, shortcut, nested, danger, onSelect }: { icon: React.ReactNode; label: string; shortcut?: string; nested?: boolean; danger?: boolean; onSelect?: () => void }) {
  return <Command.Item className={`issue-options-item${danger ? ' danger' : ''}`} value={label} onSelect={onSelect}>
    <span className="issue-options-icon" aria-hidden="true">{icon}</span>
    <span className="issue-options-label">{label}</span>
    {shortcut && <Shortcut value={shortcut}/>}
    {nested && <ChevronRight className="issue-options-chevron" size={12}/>}
  </Command.Item>
}

function Shortcut({ value }: { value: string }) {
  const keys = value === 'Shift D' ? ['⇧', 'D'] : value === 'Ctrl L' ? ['Ctrl', 'L'] : value === 'Ctrl R' ? ['Ctrl', 'R'] : value === 'Option F' ? ['⌥', 'F'] : value === 'Shift H' ? ['⇧', 'H'] : value === 'Command Shift P' ? ['⌘', '⇧', 'P'] : value === 'M, then R' ? ['M', 'then', 'R'] : value === 'M, then B' ? ['M', 'then', 'B'] : value === 'M, then M' ? ['M', 'then', 'M'] : ['⌘', '⌫']
  return <span className="issue-options-shortcut"><span className="sr-only">{value}</span>{keys.map((key,index) => <kbd key={`${key}-${index}`}>{key}</kbd>)}</span>
}

function Separator() {
  return <div className="issue-options-separator" role="separator"/>
}

function isEditableTarget(target: EventTarget | null) {
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || (target instanceof HTMLElement && target.isContentEditable)
}
