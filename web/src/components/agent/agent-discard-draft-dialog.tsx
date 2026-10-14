/**
 * The floating chat's "Discard draft?", measured in the reference app: closing a chat whose composer holds something (text, a
 * quote or mention chip, an attachment) asks first — a 480px card 26% down the window, "Discard draft?" over "Your
 * draft will be lost if you close this chat.", Cancel and a red Discard that has focus. Escape or Cancel keeps the
 * chat; Escape, the Agent button, minimizing and switching chats never ask (they keep the draft).
 */
import * as Dialog from '@radix-ui/react-dialog'
import { useRef } from 'react'
import { useI18n } from '@/i18n/i18n'
import './agent-discard-draft-dialog.css'

export function AgentDiscardDraftDialog({ open, onCancel, onDiscard }: { open: boolean; onCancel: () => void; onDiscard: () => void }) {
  const { t } = useI18n()
  const discardRef = useRef<HTMLButtonElement>(null)
  return (
    <Dialog.Root open={open} onOpenChange={next => { if (!next) onCancel() }}>
      <Dialog.Portal>
        <Dialog.Overlay className="agent-discard-overlay" data-flow-motion="backdrop"/>
        <Dialog.Content
          aria-describedby="agent-discard-description"
          className="agent-discard-dialog"
          data-flow-motion="dialog"
          onOpenAutoFocus={event => { event.preventDefault(); discardRef.current?.focus() }}
          // Escape only dismisses the question; the chat's own Escape (hide) must not also fire.
          onEscapeKeyDown={event => { event.preventDefault(); onCancel() }}
        >
          <Dialog.Title className="agent-discard-title">{t('Discard draft?')}</Dialog.Title>
          <Dialog.Description className="agent-discard-description" id="agent-discard-description">{t('Your draft will be lost if you close this chat.')}</Dialog.Description>
          <footer>
            <button className="agent-discard-cancel" onClick={onCancel} type="button">{t('Cancel')}</button>
            <button className="agent-discard-confirm" onClick={onDiscard} ref={discardRef} type="button">{t('Discard')}</button>
          </footer>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
