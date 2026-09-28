/**
 * LS-0221 DocumentsMultiSelectActions — bulk archive / delete / move on documents index.
 */
import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Command } from 'cmdk'
import { Archive, ChevronLeft, ChevronRight, FolderInput, Trash2, X } from 'lucide-react'
import type { BootstrapData, FlowDocument } from '@/types/flow'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import styles from './documents-multi-select-actions.module.css'

export type DocumentBulkAction = 'archive' | 'unarchive' | 'delete' | 'move'

export interface DocumentBulkOption {
  id: string
  label: string
  color?: string
}

const ACTIONS: { id: DocumentBulkAction; label: string; icon: typeof Archive }[] = [
  { id: 'move', label: 'Move to project…', icon: FolderInput },
  { id: 'archive', label: 'Archive', icon: Archive },
  { id: 'unarchive', label: 'Unarchive', icon: Archive },
  { id: 'delete', label: 'Delete…', icon: Trash2 },
]

export function DocumentsMultiSelectActions({
  documents,
  data,
  loading = false,
  error,
  onAction,
  onClear,
}: {
  documents: FlowDocument[]
  data: BootstrapData
  loading?: boolean
  error?: string
  onAction: (action: DocumentBulkAction, documents: FlowDocument[], value?: string) => void
  onClear: () => void
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState<DocumentBulkAction>()
  useEffect(() => { if (!documents.length) setOpen(false) }, [documents.length])
  useEffect(() => { if (!open) setPending(undefined) }, [open])
  if (!documents.length) return null
  const count = documents.length
  const allArchived = documents.every(document => Boolean(document.archivedAt))
  const visibleActions = ACTIONS.filter(action => {
    if (action.id === 'archive') return !allArchived
    if (action.id === 'unarchive') return allArchived
    return true
  })
  const moveOptions: DocumentBulkOption[] = [
    { id: '', label: 'No project' },
    ...data.projects.map(project => ({ id: project.id, label: project.name, color: project.color })),
  ]
  return <>
    <div aria-label={`${count} selected documents`} className={styles.bar} data-error={Boolean(error)} role="toolbar">
      <span className={styles.count}>{loading ? 'Updating…' : `${count}\u00a0selected`}</span>
      <button aria-expanded={open} aria-label="Open document actions" className={styles.actionsButton} disabled={loading} onClick={() => setOpen(true)} type="button">
        Actions<ChevronRight size={12} />
      </button>
      <button aria-label="Clear selected documents" className={styles.clearButton} disabled={loading} onClick={onClear} type="button"><X size={15} /></button>
      {error && <span className={styles.error} role="alert">{error}</span>}
    </div>
    <Dialog.Root onOpenChange={setOpen} open={open}>
      <Dialog.Portal>
        <Dialog.Overlay className={styles.overlay} data-flow-motion="backdrop" />
        <Dialog.Content aria-describedby={undefined} className={styles.commandDialog} data-flow-motion="dialog">
          <Dialog.Title className={styles.commandTitle}>
            {pending ? ACTIONS.find(action => action.id === pending)?.label : count === 1 ? `Document · ${documents[0].title || 'Untitled document'}` : `${count} selected documents`}
          </Dialog.Title>
          {pending && <button aria-label="Back to document actions" className={styles.commandBack} onClick={() => setPending(undefined)} type="button"><ChevronLeft size={13} />Back</button>}
          <Dialog.Close aria-label="Close document command menu" className={styles.commandClose}><span>Backspace</span><kbd>⌫</kbd></Dialog.Close>
          <Command className={styles.command} loop>
            <div className={styles.commandInput}>
              <Command.Input aria-label={pending ? `Search ${pending}` : 'Document command menu'} autoFocus key={pending ?? 'actions'} placeholder="Type a command…" />
            </div>
            <Command.List className={styles.commandList}>
              <Command.Empty className={styles.commandEmpty}>No commands found</Command.Empty>
              {pending === 'move'
                ? moveOptions.map(option => (
                  <Command.Item
                    className={styles.commandItem}
                    key={option.id || 'none'}
                    onSelect={() => { onAction('move', documents, option.id); setOpen(false) }}
                    value={option.label}
                  >
                    <span className={styles.optionLabel}>{option.color && <i style={{ background: option.color }} />}<span>{option.label}</span></span>
                  </Command.Item>
                ))
                : visibleActions.map(action => {
                  const Icon = action.icon
                  return (
                  <Command.Item
                    className={styles.commandItem}
                    key={action.id}
                    onSelect={() => {
                      if (action.id === 'move') setPending('move')
                      else { onAction(action.id, documents); setOpen(false) }
                    }}
                    value={action.label}
                  >
                    <span className={styles.optionLabel}><Icon size={15} /><span>{action.label}</span></span>
                  </Command.Item>
                  )
                })}
            </Command.List>
          </Command>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  </>
}

export function DocumentListCheckbox({
  checked,
  indeterminate = false,
  onChange,
  label = 'Select document',
}: {
  checked: boolean
  indeterminate?: boolean
  onChange: (checked: boolean) => void
  label?: string
}) {
  return (
    <span className={styles.checkboxCell}>
      <button
        aria-checked={indeterminate ? 'mixed' : checked}
        aria-label={label}
        className={styles.checkbox}
        data-checked={checked || indeterminate || undefined}
        onClick={event => { event.stopPropagation(); onChange(!checked) }}
        role="checkbox"
        type="button"
      >
        {(checked || indeterminate) && <CheckboxMark />}
      </button>
    </span>
  )
}
