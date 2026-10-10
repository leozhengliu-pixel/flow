/**
 * Owner picker for a document (list Owner column, "Edited" popover, ⌘K):
 * "No owner", the creator in its own group, then every other member, with a
 * check on the current owner. Choosing writes the owner permission.
 */
import { type ReactNode } from 'react'

import { PropertyMenu } from '@/components/property/property-menu'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument } from '@/types/flow'
import { changeDocumentOwner, documentOwner, type DocumentActionContext } from './document-actions'
import { documentOwnerOptions } from './document-owner-options'

export function DocumentOwnerPicker({ ctx, document, trigger, triggerClassName, ariaLabel, align = 'start', open, onOpenChange }: {
  ctx: DocumentActionContext
  document: FlowDocument
  trigger: ReactNode
  triggerClassName?: string
  ariaLabel?: string
  align?: 'start' | 'center' | 'end'
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const { t } = useI18n()
  const owner = documentOwner(document, ctx.data.users)
  return <PropertyMenu
    label="Owner"
    ariaLabel={ariaLabel ?? t('Change owner')}
    searchPlaceholder={t('Change owner…')}
    options={documentOwnerOptions({ ...ctx, t }, document)}
    selectedId={owner?.id ?? ''}
    trigger={trigger}
    triggerClassName={triggerClassName}
    triggerRole="button"
    align={align}
    open={open}
    onOpenChange={onOpenChange}
    closeOnSelect
    onChange={id => { if ((id || null) !== (owner?.id ?? null)) return changeDocumentOwner(ctx, document, id || null) }}
  />
}
