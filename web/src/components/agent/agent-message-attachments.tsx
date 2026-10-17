import { useI18n } from '@/i18n/i18n'
import type { AgentMessageAttachment } from '@/types/flow'
import { AgentAttachIcon } from './agent-icons'
import styles from './agent-message-attachments.module.css'

/** The files sent with a user message: saved images as thumbnails that open full size, anything else by name. */
export function AgentMessageAttachments({ attachments }: { attachments?: AgentMessageAttachment[] }) {
  const { t } = useI18n()
  if (!attachments?.length) return null
  return (
    <div aria-label={t('Attachments')} className={styles.attachments} role="list">
      {attachments.map((attachment, index) => attachment.url && attachment.contentType.startsWith('image/') ? (
        <a className={styles.image} href={attachment.url} key={`${attachment.url}-${index}`} rel="noreferrer" role="listitem" target="_blank" title={attachment.name}>
          <img alt={attachment.name} loading="lazy" src={attachment.url}/>
        </a>
      ) : (
        <span className={styles.file} key={`${attachment.name}-${index}`} role="listitem" title={attachment.name}>
          <AgentAttachIcon aria-hidden size={12}/>
          <b data-i18n-ignore>{attachment.name}</b>
        </span>
      ))}
    </div>
  )
}
