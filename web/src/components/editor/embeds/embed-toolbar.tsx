import { Link2, SquareArrowOutUpRight, Trash2, Type } from 'lucide-react'
import type { MouseEvent, ReactElement } from 'react'
import { toast } from 'sonner'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { useI18n } from '@/i18n/i18n'
import { safeEmbedHref } from './embed-providers'
import styles from './embed.module.css'

/** Keeps the editor's focus and selection while a toolbar control is pressed (a mousedown would dismiss them). */
const keepFocus = (event: MouseEvent) => event.preventDefault()

/** Copies a link and confirms with Linear's toast; the clipboard can be unavailable (insecure context, denied permission). */
async function copyEmbedLink(src: string, labels: { copied: string; failed: string }) {
  try {
    await navigator.clipboard.writeText(src)
    toast.info(labels.copied)
  } catch {
    toast.error(labels.failed)
  }
}

/**
 * The pill that floats over the top-right corner of an embed on hover / selection, as in Linear: Open link, Convert to
 * text link, Copy link, then Delete after a divider. Read-only renderings (comments, document history) pass no
 * `onConvert` / `onDelete` and keep only Open link and Copy link, which is what Linear's history view shows.
 */
export function EmbedToolbar({ src, onConvert, onDelete }: { src: string; onConvert?: () => void; onDelete?: () => void }) {
  const { t } = useI18n()
  const href = safeEmbedHref(src)
  const item = (label: string, child: ReactElement) => <FlowTooltip label={label}>{child}</FlowTooltip>
  return (
    <TooltipProvider delayDuration={300}>
      <div aria-label={t('Embed actions')} className={styles.toolbar} contentEditable={false} data-embed-toolbar role="toolbar">
        {href && item(t('Open link'), <a aria-label={t('Open link')} className={styles.toolbarButton} href={href} onMouseDown={keepFocus} rel="noopener noreferrer" target="_blank"><SquareArrowOutUpRight size={14}/></a>)}
        {onConvert && item(t('Convert to text link'), <button aria-label={t('Convert to text link')} className={styles.toolbarButton} onClick={onConvert} onMouseDown={keepFocus} type="button"><Type size={14}/></button>)}
        {item(t('Copy link'), <button aria-label={t('Copy link')} className={styles.toolbarButton} onClick={() => void copyEmbedLink(src, { copied: t('Link copied to clipboard'), failed: t('Could not copy link') })} onMouseDown={keepFocus} type="button"><Link2 size={14}/></button>)}
        {onDelete && <><span aria-hidden="true" className={styles.toolbarSeparator}/>{item(t('Delete'), <button aria-label={t('Delete')} className={styles.toolbarButton} onClick={onDelete} onMouseDown={keepFocus} type="button"><Trash2 size={14}/></button>)}</>}
      </div>
    </TooltipProvider>
  )
}
