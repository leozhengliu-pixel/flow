import { ReadOnlyEmbed } from '@/components/editor/embeds/embed-node-view'
import { embedSourceForUrl } from '@/components/editor/embeds/embed-providers'
import { useI18n } from '@/i18n/i18n'
import styles from './agent-inline-embed.module.css'

/** An embedded video or preview card in the composer (`editable`, with a Delete button) or a sent message. */
export function AgentInlineEmbed({ src, editable = false, onRemove }: { src: string; editable?: boolean; onRemove?: () => void }) {
  const { t } = useI18n()
  const source = embedSourceForUrl(src)
  if (!source) return <a href={src} rel="noopener noreferrer" target="_blank">{src}</a>
  return <div className={styles.embed}>
    <ReadOnlyEmbed embedUrl={source.embedUrl} provider={source.provider} src={src}/>
    {editable && <button aria-label={t('Delete')} className={styles.remove} onClick={event => { event.preventDefault(); onRemove?.() }} onMouseDown={event => event.preventDefault()} type="button">{t('Delete')}</button>}
  </div>
}
