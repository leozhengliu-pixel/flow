import type { PropertyOption } from '@/components/property/property-menu'
import type { FlowDocument } from '@/types/flow'
import type { DocumentActionContext } from './document-actions'

export function documentOwnerOptions(ctx: DocumentActionContext, document: FlowDocument): PropertyOption[] {
  const { t } = ctx
  const members = ctx.data.users.filter(user => !user.app && user.id !== document.creator.id)
  return [
    { id: '', label: t('No owner'), searchOnly: false },
    { id: document.creator.id, label: document.creator.displayName || document.creator.name, groupLabel: t('Creator'), person: document.creator },
    ...members.map(user => ({ id: user.id, label: user.displayName || user.name, groupLabel: t('Members'), person: user })),
  ]
}
