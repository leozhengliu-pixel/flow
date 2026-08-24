import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { ChevronLeft, ChevronRight, X } from 'lucide-react'
import type { MyIssuesRowData } from './my-issues-list'
import { AgentChatPanel } from '@/components/agent/agent-chat-panel'
import { AgentPointerIcon } from '@/components/agent/agent-icons'
import styles from './my-issues-bulk-action-bar.module.css'

export type MyIssuesBulkAction = 'assign' | 'unassignMe' | 'status' | 'priority' | 'project' | 'labels' | 'dueDate' | 'copyId' | 'copyUrl' | 'copyTitle' | 'copyTitleLink' | 'copyDescriptionMarkdown' | 'copyContentMarkdown' | 'copyBranch' | 'copyPrompt' | 'subscribers' | 'removeSubscribers' | 'markAs'
export interface MyIssuesBulkActionOption { id: string; label: string; color?: string }

export interface MyIssuesBulkActionBarProps {
  selectedIssues: MyIssuesRowData[]
  loading?: boolean
  error?: string
  actionOptions?: (action: MyIssuesBulkAction) => MyIssuesBulkActionOption[] | undefined
  onAction: (action: MyIssuesBulkAction, issues: MyIssuesRowData[], value?: string) => void
  onAskFlow?: (issues: MyIssuesRowData[]) => void
  onClear: () => void
}

const actions: { id: MyIssuesBulkAction; label: string; shortcut?: string }[] = [
  { id: 'assign', label: 'Assign to...', shortcut: 'A' }, { id: 'unassignMe', label: 'Un-assign from me', shortcut: 'I' },
  { id: 'status', label: 'Change status...', shortcut: 'S' }, { id: 'priority', label: 'Change priority...', shortcut: 'P' },
  { id: 'project', label: 'Add to project...', shortcut: '⇧ P' }, { id: 'labels', label: 'Change or add labels...', shortcut: 'L' },
  { id: 'dueDate', label: 'Set due date...', shortcut: '⇧ D' }, { id: 'copyId', label: 'Copy issue ID', shortcut: '⌘ .' },
  { id: 'copyUrl', label: 'Copy issue URL', shortcut: '⌘ ⇧ ,' }, { id: 'copyTitle', label: 'Copy issue title', shortcut: "⌘ ⇧ '" },
  { id: 'copyTitleLink', label: 'Copy title as link', shortcut: '⌘ C' }, { id: 'copyDescriptionMarkdown', label: 'Copy issue description as Markdown' },
  { id: 'copyContentMarkdown', label: 'Copy issue content as Markdown', shortcut: '⌘ ⌥ C' }, { id: 'copyBranch', label: 'Copy git branch name', shortcut: '⌘ ⇧ .' },
  { id: 'copyPrompt', label: 'Copy as prompt', shortcut: '⌘ ⌥ P' }, { id: 'subscribers', label: 'Change subscribers...', shortcut: '⌘ ⇧ S' },
  { id: 'removeSubscribers', label: 'Remove all subscribers' }, { id: 'markAs', label: 'Mark issue as...' },
]

export function MyIssuesBulkActionBar({ selectedIssues, loading = false, error, actionOptions, onAction, onAskFlow, onClear }: MyIssuesBulkActionBarProps) {
  const [open, setOpen] = useState(false)
  const [pendingAction, setPendingAction] = useState<MyIssuesBulkAction>()
  const [agentIssues, setAgentIssues] = useState<MyIssuesRowData[]>([])
  const [agentOpen, setAgentOpen] = useState(false)
  useEffect(() => { if (!selectedIssues.length) setOpen(false) }, [selectedIssues.length])
  useEffect(() => { if (!open) setPendingAction(undefined) }, [open])
  if (!selectedIssues.length) return null
  const count = selectedIssues.length
  return <>
    <div className={styles.bar} role="toolbar" aria-label={`${count} selected issues`} data-error={Boolean(error)}>
      <span className={styles.count}>{loading ? 'Updating...' : `${count}\u00a0selected`}</span>
      <button className={styles.actionsButton} aria-label="Open command menu" aria-expanded={open} disabled={loading} onClick={() => setOpen(true)}>Actions<ChevronRight size={12}/></button>
      <button className={styles.iconButton} aria-label="Ask Flow" tabIndex={-1} disabled={loading} onClick={() => { setAgentIssues([...selectedIssues]); setAgentOpen(true); onAskFlow?.(selectedIssues) }}><AgentPointerIcon/></button>
      <button className={styles.clearButton} aria-label="Clear selected" disabled={loading} onClick={onClear}><X size={15}/></button>
      {error && <span className={styles.error} role="alert">{error}</span>}
    </div>
    <Dialog.Root open={open} onOpenChange={setOpen}><Dialog.Portal><Dialog.Overlay className={styles.overlay}/><Dialog.Content className={styles.commandDialog} aria-describedby={undefined}>
      <Dialog.Title className={styles.commandTitle}>{pendingAction ? actions.find(action => action.id === pendingAction)?.label : count === 1 ? `${selectedIssues[0].identifier} · ${selectedIssues[0].title}` : `${count} selected issues`}</Dialog.Title>
      {pendingAction && <button type="button" className={styles.commandBack} aria-label="Back to actions" onClick={() => setPendingAction(undefined)}><ChevronLeft size={13}/>Back</button>}
      <Dialog.Close className={styles.commandClose} aria-label="Close command menu"><span>Backspace</span><kbd>⌫</kbd></Dialog.Close>
      <Command className={styles.command} loop>
        <div className={styles.commandInput}><Command.Input key={pendingAction ?? 'actions'} aria-label={pendingAction ? `Search ${actions.find(action => action.id === pendingAction)?.label}` : 'Command menu'} placeholder={pendingAction ? 'Search...' : 'Type a command...'} autoFocus/>{!pendingAction && <><span>Ask Flow</span><kbd>Tab</kbd></>}</div>
        <Command.List className={styles.commandList}><Command.Empty className={styles.commandEmpty}>No commands found</Command.Empty>{pendingAction
          ? actionOptions?.(pendingAction)?.map(option => <Command.Item key={option.id || 'none'} value={option.label} className={styles.commandItem} onSelect={() => { onAction(pendingAction, selectedIssues, option.id); setOpen(false) }}><span className={styles.optionLabel}>{option.color && <i style={{ backgroundColor: option.color }}/>}<span>{option.label}</span></span></Command.Item>)
          : actions.map(action => <Command.Item key={action.id} value={action.label} className={styles.commandItem} onSelect={() => { const options = actionOptions?.(action.id); if (options?.length) setPendingAction(action.id); else { onAction(action.id, selectedIssues); setOpen(false) } }}><span>{action.label}</span>{action.shortcut && <kbd>{action.shortcut}</kbd>}</Command.Item>)}</Command.List>
      </Command>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
    <AgentChatPanel issues={agentIssues} onClose={()=>{setAgentOpen(false);setAgentIssues([])}} open={agentOpen}/>
  </>
}
