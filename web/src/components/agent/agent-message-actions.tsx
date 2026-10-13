import { useEffect, useRef, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { createAgentSessionFeedback, updateAgentMessageFeedback } from '@/lib/api'
import { toast } from '@/lib/toast'
import { copyAgentMessage } from './agent-copy'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { AttachmentRemoveButton } from '@/components/ui/attachment-remove-button'
import { useI18n } from '@/i18n/i18n'
import type { AgentFeedback, AgentMessage, AgentSession } from '@/types/flow'
import { AgentAttachIcon } from './agent-icons'
import { AGENT_ATTACHMENT_ACCEPT, addAgentAttachments, agentFileContext } from './agent-attachments'
import styles from './agent-message-actions.module.css'

type Rating = NonNullable<AgentFeedback['rating']>

function ActionButton({ label, pressed, onClick, children }: { label: string; pressed?: boolean; onClick: () => void; children: React.ReactNode }) {
  return <FlowTooltip label={label} sideOffset={8}>
    <button aria-label={label} aria-pressed={pressed} className={styles.action} data-pressed={pressed || undefined} onClick={onClick} type="button">
      <span aria-hidden="true">{children}</span>
    </button>
  </FlowTooltip>
}

/**
 * The reference app's actions under an agent reply: "This was helpful" (thumbs up), "Leave feedback…" (thumbs down, opens the
 * feedback dialog) and "Copy message". A given rating turns its thumb solid and its label into "Remove feedback".
 * With a `sessionId` the rating is saved on the server; otherwise it is kept for this view only.
 */
export function AgentReplyActions({ content, message, sessionId, onSessionChange, className }: {
  content: string
  message: Pick<AgentMessage, 'id' | 'feedback'>
  sessionId?: string
  onSessionChange?: (session: AgentSession) => void
  className?: string
}) {
  const { t } = useI18n()
  const [rating, setRating] = useState<Rating | undefined>(message.feedback?.rating)
  const [dialogOpen, setDialogOpen] = useState(false)
  useEffect(() => setRating(message.feedback?.rating), [message.feedback?.rating])
  const persisted = Boolean(sessionId && !message.id.startsWith('pending-') && !message.id.startsWith('awaiting-'))
  const save = async (next: Rating | '', comment?: string) => {
    const previous = rating
    setRating(next || undefined)
    if (!persisted) return
    try {
      const session = await updateAgentMessageFeedback(sessionId!, message.id, comment === undefined ? { rating: next } : { rating: next, comment })
      onSessionChange?.(session)
    } catch (error) {
      setRating(previous)
      toast.error(error instanceof Error ? error.message : t('Could not save feedback'))
    }
  }
  return <div className={`${styles.replyActions}${className ? ` ${className}` : ''}`} data-agent-reply-actions="">
    <div className={styles.group}>
      <div className={styles.group}>
        <ActionButton label={t(rating === 'positive' ? 'Remove feedback' : 'This was helpful')} pressed={rating === 'positive'} onClick={() => void save(rating === 'positive' ? '' : 'positive')}>
          <FlowGlyph name={rating === 'positive' ? 'thumbsUpFilled' : 'thumbsUp'} size={14}/>
        </ActionButton>
        <ActionButton label={t(rating === 'negative' ? 'Remove feedback' : 'Leave feedback…')} pressed={rating === 'negative'} onClick={() => rating === 'negative' ? void save('') : setDialogOpen(true)}>
          <FlowGlyph name={rating === 'negative' ? 'thumbsDownFilled' : 'thumbsDown'} size={14}/>
        </ActionButton>
      </div>
      <ActionButton label={t('Copy message')} onClick={() => copyAgentMessage(content, t)}>
        <FlowGlyph name="copyMessage" size={14}/>
      </ActionButton>
    </div>
    <AgentFeedbackDialog open={dialogOpen} onOpenChange={setDialogOpen} onSubmit={async comment => { await save('negative', comment) }}/>
  </div>
}

/** The reference app's hover actions under a message you sent: "Copy message" and "Edit message". */
export function AgentUserMessageActions({ content, onEdit, className }: { content: string; onEdit?: () => void; className?: string }) {
  const { t } = useI18n()
  return <div className={`${styles.userActions}${className ? ` ${className}` : ''}`} data-agent-user-actions="">
    <ActionButton label={t('Copy message')} onClick={() => copyAgentMessage(content, t)}><FlowGlyph name="copyMessage" size={14}/></ActionButton>
    {onEdit && <ActionButton label={t('Edit message')} onClick={onEdit}><FlowGlyph name="edit" size={14}/></ActionButton>}
  </div>
}

/** The chat menu's "Leave feedback…": a note about the whole conversation. */
export function AgentSessionFeedbackDialog({ sessionId, open, onOpenChange, onSessionChange }: { sessionId: string; open: boolean; onOpenChange: (open: boolean) => void; onSessionChange?: (session: AgentSession) => void }) {
  return <AgentFeedbackDialog open={open} onOpenChange={onOpenChange} onSubmit={async comment => {
    const session = await createAgentSessionFeedback(sessionId, comment)
    onSessionChange?.(session)
  }}/>
}

/**
 * The reference app's "Leave feedback" dialog: a title, "Send our team your feedback for the AI generated content", a note box,
 * an attach button (attached files are added to the note) and Cancel / Send (Send waits for some text).
 */
export function AgentFeedbackDialog({ open, onOpenChange, onSubmit }: { open: boolean; onOpenChange: (open: boolean) => void; onSubmit: (comment: string) => Promise<void> }) {
  const { t } = useI18n()
  const [text, setText] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [busy, setBusy] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (open) return
    setText('')
    setFiles([])
  }, [open])
  const ready = Boolean(text.trim() || files.length) && !busy
  const send = async () => {
    if (!ready) return
    setBusy(true)
    try {
      const attached = await Promise.all(files.map(agentFileContext))
      await onSubmit([text.trim(), ...attached].filter(Boolean).join('\n\n'))
      toast.success(t('Feedback sent'), { description: t('Thanks for helping us improve the agent') })
      onOpenChange(false)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not send feedback'))
    } finally {
      setBusy(false)
    }
  }
  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay className={styles.overlay} data-flow-motion="backdrop"/>
      <Dialog.Content aria-describedby={undefined} className={styles.dialog} data-flow-motion="dialog" onOpenAutoFocus={event => { event.preventDefault(); requestAnimationFrame(() => document.getElementById('agent-feedback-note')?.focus()) }}>
        <form onSubmit={event => { event.preventDefault(); void send() }}>
          <div className={styles.dialogBody}>
            <div className={styles.dialogHeading}>
              <Dialog.Title className={styles.dialogTitle}>{t('Leave feedback')}</Dialog.Title>
              <span className={styles.dialogDescription}>{t('Send our team your feedback for the AI generated content')}</span>
            </div>
            <div className={styles.dialogField}>
              <textarea
                aria-label={t('Tell us more')}
                id="agent-feedback-note"
                onChange={event => setText(event.target.value)}
                onKeyDown={event => {
                  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void send() }
                }}
                placeholder={t('Tell us more...')}
                value={text}
              />
              {files.length > 0 && <div className={styles.dialogFiles}>
                {files.map((file, index) => <span key={`${file.name}-${file.lastModified}`}>
                  <b data-i18n-ignore>{file.name}</b>
                  <AttachmentRemoveButton label={`${t('Remove attachment')} ${file.name}`} onClick={() => setFiles(items => items.filter((_, itemIndex) => itemIndex !== index))}/>
                </span>)}
              </div>}
            </div>
          </div>
          <footer className={styles.dialogFooter}>
            <FlowTooltip label={t('Attach images, files, or videos')}>
              <button aria-label={t('Attach images, files, or videos')} className={styles.dialogAttach} onClick={() => fileInput.current?.click()} type="button">
                <span aria-hidden="true"><AgentAttachIcon size={14}/></span>
              </button>
            </FlowTooltip>
            <input accept={AGENT_ATTACHMENT_ACCEPT} hidden multiple onChange={event => { const picked = event.target.files; setFiles(items => addAgentAttachments(items, picked)); event.target.value = '' }} ref={fileInput} type="file"/>
            <div className={styles.dialogButtons}>
              <button className={styles.dialogCancel} onClick={() => onOpenChange(false)} type="button">{t('Cancel')}</button>
              <button className={styles.dialogSend} disabled={!ready} type="submit">{t('Send')}</button>
            </div>
          </footer>
        </form>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
