/**
 * "Edited <date>" in the document header: Show author names, Owned by (owner
 * picker), Last edit by and Show document history — Linear's metadata popover.
 */
import * as Popover from '@radix-ui/react-popover'
import { History, SlidersHorizontal } from 'lucide-react'

import { Toggle } from '@/components/ui/toggle'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument, User } from '@/types/flow'
import { documentOwner, type DocumentActionContext } from './document-actions'
import { DocumentOwnerPicker } from './document-owner-picker'

export function DocumentEditedPopover({ ctx, document, lastEditor, lastEditedAt, showAuthorNames, onShowAuthorNames, onShowHistory, open, onOpenChange, ownerOpen, onOwnerOpenChange, readOnly }: {
  ctx: DocumentActionContext
  document: FlowDocument
  lastEditor: User
  lastEditedAt: string
  showAuthorNames: boolean
  onShowAuthorNames: (value: boolean) => void
  onShowHistory: () => void
  open: boolean
  onOpenChange: (open: boolean) => void
  ownerOpen: boolean
  onOwnerOpenChange: (open: boolean) => void
  /** A deleted document: the owner is shown, not editable. */
  readOnly?: boolean
}) {
  const { t, formatDate } = useI18n()
  const owner = documentOwner(document, ctx.data.users)
  const editedLabel = t('Edited {date}').replace('{date}', formatDate(lastEditedAt, { month: 'short', day: 'numeric' }))
  const ownerName = owner ? owner.displayName || owner.name : t('No owner')
  const ownerValue = <>
    {owner ? <UserAvatar avatarUrl={owner.avatarUrl} className="document-meta-avatar" name={ownerName}/> : null}
    <strong data-i18n-ignore>{ownerName}</strong>
  </>
  const editorName = lastEditor.displayName || lastEditor.name
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild>
      <button aria-label={editedLabel} className="document-edited" title={formatDate(lastEditedAt, { dateStyle: 'medium', timeStyle: 'short' })} type="button">
        <SlidersHorizontal size={14}/><span className="document-edited__label">{editedLabel}</span>
      </button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content align="end" className="document-edited-popover" collisionPadding={10} data-flow-motion="floating" onCloseAutoFocus={event => event.preventDefault()} sideOffset={4}>
        <label className="document-view-options">
          <span>{t('Show author names')}</span>
          <Toggle checked={showAuthorNames} label={t(showAuthorNames ? 'Hide author names' : 'Show author names')} onChange={onShowAuthorNames}/>
        </label>
        <div className="document-owner-row">
          <span>{t('Owned by')}</span>
          {readOnly
            ? <span className="document-person">{ownerValue}</span>
            : <DocumentOwnerPicker ctx={ctx} document={document} trigger={ownerValue} triggerClassName="document-person document-person--button" open={ownerOpen} onOpenChange={onOwnerOpenChange}/>}
        </div>
        <div className="document-last-edit-row">
          <span>{t('Last edit by')}</span>
          <span className="document-person"><UserAvatar avatarUrl={lastEditor.avatarUrl} className="document-meta-avatar" name={editorName}/><strong data-i18n-ignore>{editorName}</strong></span>
          <time dateTime={lastEditedAt}>{formatDate(lastEditedAt, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })}</time>
        </div>
        <div className="document-history-action">
          <Popover.Close asChild><button onClick={onShowHistory} type="button"><History size={14}/>{t('Show document history')}</button></Popover.Close>
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
