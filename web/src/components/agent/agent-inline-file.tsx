import type { ReactNode } from 'react'
import { Printer } from 'lucide-react'
import { copyText, downloadImage } from '@/components/editor/lightbox-actions'
import { deleteIcon, downloadIcon, linkIcon, viewIcon } from '@/components/issue/editor/image-extension'
import { openPdfViewer } from '@/components/editor/pdf/pdf-bridge'
import { printPdf } from '@/components/editor/pdf/pdf-file-actions'
import { useI18n } from '@/i18n/i18n'
import { formatFileSize } from '@/lib/format-file-size'
import { ToolbarIcon } from './agent-inline-image'
import styles from './agent-inline-file.module.css'

const isPdf = (name: string, contentType: string) => contentType === 'application/pdf' || /\.pdf$/i.test(name)

function printFile(src: string) {
  const win = window.open(src, '_blank')
  win?.addEventListener('load', () => win.print())
}

/**
 * A file in the Agent composer or a sent message, as the reference app draws it: a 54px card with the file-type icon,
 * name and size, and a pill of actions. Only the composer can delete it. The reference app's "Open menu" button has
 * no counterpart here, so it is left out.
 */
export function AgentInlineFile({ name, src, size, contentType, editable = false, uploading = false, onRemove }: { name: string; src: string; size: number; contentType: string; editable?: boolean; uploading?: boolean; onRemove?: () => void }) {
  const { t } = useI18n()
  const pdf = isPdf(name, contentType)
  const printable = pdf || contentType.startsWith('image/')
  const button = (label: string, icon: ReactNode, run: () => void) => (
    <button aria-label={t(label)} onClick={event => { event.preventDefault(); event.stopPropagation(); run() }} onMouseDown={event => event.preventDefault()} title={t(label)} type="button">{icon}</button>
  )
  return (
    <div aria-label={name} className={styles.card} data-uploading={uploading || undefined} role="group">
      <div className={styles.row}>
        <span className={styles.main}>
          <span aria-hidden className={styles.icon}>
            <svg fill="currentColor" height="16" viewBox="0 0 16 16" width="16"><path d="M3.75 1A1.75 1.75 0 0 0 2 2.75v10.5C2 14.216 2.784 15 3.75 15h8.5A1.75 1.75 0 0 0 14 13.25V6.586a1.75 1.75 0 0 0-.513-1.238L9.652 1.513A1.75 1.75 0 0 0 8.414 1H3.75ZM8 2v3.25c0 .966.784 1.75 1.75 1.75H13v6.25a.25.25 0 0 1-.25.25h-8.5a.25.25 0 0 1-.25-.25V2.75a.25.25 0 0 1 .25-.25H8Zm1.5.56 2.94 2.94H9.75a.25.25 0 0 1-.25-.25V2.56Z"/></svg>
          </span>
          <span className={styles.text}>
            <span className={styles.name} data-i18n-ignore>{name}</span>
            {!uploading && <span className={styles.size} data-i18n-ignore>{formatFileSize(size)}</span>}
          </span>
        </span>
        {!uploading && <span className={styles.actions}>
          {pdf && button('View PDF', <ToolbarIcon make={viewIcon}/>, () => openPdfViewer({ src, title: name }))}
          {button('Download', <ToolbarIcon make={downloadIcon}/>, () => downloadImage({ src, alt: name }))}
          {button('Copy link', <ToolbarIcon make={linkIcon}/>, () => void copyText(new URL(src, window.location.origin).href))}
          {editable
            ? <><span aria-hidden className={styles.separator}/>{button('Delete', <ToolbarIcon make={deleteIcon}/>, () => onRemove?.())}</>
            : printable && button('Print', <Printer aria-hidden size={14} strokeWidth={1.75}/>, () => { if (pdf) void printPdf(src); else printFile(src) })}
        </span>}
      </div>
    </div>
  )
}
