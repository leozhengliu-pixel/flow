/**
 * LS-0224 EditCustomViewHeader — thin wrapper around SavedViewEditor with draft persist.
 */

import { useEffect, useRef } from 'react'
import type { ReactNode } from 'react'
import { SavedViewEditor, type SavedViewTarget } from './saved-view-editor'
import type { ViewVisual } from '@/components/views/view-icon-picker'
import {
  discardCustomViewDraft,
  readCustomViewDraft,
  writeCustomViewDraft,
} from './custom-view-draft'

export function EditCustomViewHeader({
  orgKey,
  viewId,
  initialName = '',
  initialDescription = '',
  initialIcon,
  initialColor,
  initialTarget,
  saveTargets = [],
  saving = false,
  hasChanges = false,
  actions,
  onCancel,
  onSave,
  onDiscardDraft,
}: {
  orgKey: string
  viewId: string | 'new'
  initialName?: string
  initialDescription?: string
  initialIcon?: string
  initialColor?: string
  initialTarget?: SavedViewTarget
  saveTargets?: SavedViewTarget[]
  saving?: boolean
  hasChanges?: boolean
  actions?: ReactNode
  onCancel: () => void
  onSave: (name: string, description: string, target: SavedViewTarget | undefined, visual: ViewVisual) => void
  onDiscardDraft?: () => void
}) {
  const draft = readCustomViewDraft(orgKey, viewId)
  const name = draft?.name ?? initialName
  const description = draft?.description ?? initialDescription
  const icon = draft?.icon ?? initialIcon
  const color = draft?.color ?? initialColor
  const dirtyRef = useRef(hasChanges)
  dirtyRef.current = hasChanges

  useEffect(() => {
    return () => {
      if (!dirtyRef.current) return
      // Persist last known editor fields via draft key; SavedViewEditor owns live state,
      // so callers should also call writeCustomViewDraft on change when available.
      writeCustomViewDraft(orgKey, viewId, {
        name,
        description,
        icon,
        color,
      })
    }
  }, [orgKey, viewId, name, description, icon, color])

  return (
    <SavedViewEditor
      ariaLabel="Edit view"
      actions={actions}
      initialName={name}
      initialDescription={description}
      initialIcon={icon}
      initialColor={color}
      initialTarget={initialTarget}
      saveTargets={saveTargets}
      saving={saving}
      onCancel={() => {
        discardCustomViewDraft(orgKey, viewId)
        onDiscardDraft?.()
        onCancel()
      }}
      onSave={(nextName, nextDescription, target, visual) => {
        discardCustomViewDraft(orgKey, viewId)
        onSave(nextName, nextDescription, target, visual)
      }}
    />
  )
}
