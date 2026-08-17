import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { ChevronLeft, ChevronRight, Sparkles, X } from 'lucide-react'
import type { ProjectPageItem } from './projects-data-view'
import styles from '@/components/my-issues/my-issues-bulk-action-bar.module.css'

export type ProjectBulkAction = 'status' | 'priority' | 'lead' | 'targetDate' | 'copyNames' | 'delete' | 'edit' | 'initiatives' | 'labels' | 'dependencies' | 'members' | 'favorite' | 'subscribe'
export type ProjectBulkOption = { id: string; label: string; color?: string }

const ACTIONS: { id: ProjectBulkAction; label: string; shortcut?: string }[] = [
  { id: 'edit', label: 'Edit project…' },
  { id: 'status', label: 'Change project status…', shortcut: 'P, then S' },
  { id: 'priority', label: 'Change project priority…', shortcut: 'P, then P' },
  { id: 'initiatives', label: 'Change project initiatives…', shortcut: 'P, then N' },
  { id: 'labels', label: 'Add labels…', shortcut: 'P, then L' },
  { id: 'targetDate', label: 'Set project target date…', shortcut: '⌃⌥D' },
  { id: 'dependencies', label: 'Change project dependencies…' },
  { id: 'lead', label: 'Set project lead…', shortcut: 'P, then A' },
  { id: 'members', label: 'Change project members…', shortcut: 'P, then M' },
  { id: 'favorite', label: 'Favorite project', shortcut: '⌥F' },
  { id: 'subscribe', label: 'Subscribe to project notifications…' },
  { id: 'copyNames', label: 'Copy project names' },
  { id: 'delete', label: 'Delete projects…' },
]

export function ProjectsBulkActionBar({ projects, options, onAction, onAsk, onClear }: {
  projects: ProjectPageItem[]
  options: (action: ProjectBulkAction) => ProjectBulkOption[] | undefined
  onAction: (action: ProjectBulkAction, value?: string) => void
  onAsk: () => void
  onClear: () => void
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState<ProjectBulkAction>()
  useEffect(() => { if (!projects.length) setOpen(false) }, [projects.length])
  useEffect(() => { if (!open) setPending(undefined) }, [open])
  if (!projects.length) return null
  const count = projects.length
  return <>
    <div aria-label={`${count} selected projects`} className={styles.bar} role="toolbar"><span className={styles.count}>{count}&nbsp;selected</span><button aria-expanded={open} aria-label="Open project command menu" className={styles.actionsButton} onClick={() => setOpen(true)}>Actions<ChevronRight size={12} /></button><button aria-label="Ask Flow" className={styles.iconButton} onClick={onAsk}><Sparkles size={15} /></button><button aria-label="Clear selected projects" className={styles.clearButton} onClick={onClear}><X size={15} /></button></div>
    <Dialog.Root open={open} onOpenChange={setOpen}><Dialog.Portal><Dialog.Overlay className={styles.overlay} /><Dialog.Content aria-describedby={undefined} className={styles.commandDialog}>
      <Dialog.Title className={styles.commandTitle}>{pending ? ACTIONS.find(action => action.id === pending)?.label : count === 1 ? `Project · ${projects[0].name}` : `${count} selected projects`}</Dialog.Title>
      {pending && <button aria-label="Back to project actions" className={styles.commandBack} onClick={() => setPending(undefined)} type="button"><ChevronLeft size={13} />Back</button>}
      <Dialog.Close aria-label="Close project command menu" className={styles.commandClose}><span>Backspace</span><kbd>⌫</kbd></Dialog.Close>
      <Command className={styles.command} loop><div className={styles.commandInput}><Command.Input aria-label={pending ? `Search ${pending}` : 'Project command menu'} autoFocus key={pending ?? 'actions'} placeholder="Type a command…" />{!pending && <><span>Ask Flow</span><kbd>Tab</kbd></>}</div><Command.List className={styles.commandList}><Command.Empty className={styles.commandEmpty}>No commands found</Command.Empty>{pending ? options(pending)?.map(option => <Command.Item className={styles.commandItem} key={option.id || 'none'} onSelect={() => { onAction(pending, option.id); setOpen(false) }} value={option.label}><span className={styles.optionLabel}>{option.color && <i style={{ background: option.color }} />}<span>{option.label}</span></span></Command.Item>) : ACTIONS.map(action => <Command.Item className={styles.commandItem} key={action.id} onSelect={() => { const values = options(action.id); if (values?.length) setPending(action.id); else { onAction(action.id); setOpen(false) } }} value={action.label}><span>{action.label}</span>{action.shortcut && <kbd>{action.shortcut}</kbd>}</Command.Item>)}</Command.List></Command>
    </Dialog.Content></Dialog.Portal></Dialog.Root>
  </>
}
