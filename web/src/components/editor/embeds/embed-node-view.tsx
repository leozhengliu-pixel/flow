import { Link2 } from 'lucide-react'
import { NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import { useI18n } from '@/i18n/i18n'
import styles from './embed.module.css'

/** The inline player for a pasted YouTube / Loom / Descript link, with Linear's "Keep as link" while editing. */
export function EmbedView({ editor, getPos, node }: ReactNodeViewProps) {
  const { t } = useI18n()
  const { embedUrl, provider, src } = node.attrs as { embedUrl: string; provider: string; src: string }
  const keepAsLink = () => {
    const pos = getPos()
    if (typeof pos !== 'number') return
    const { schema } = editor.state
    const text = schema.text(src, [schema.marks.link.create({ href: src })])
    editor.view.dispatch(editor.state.tr.replaceWith(pos, pos + node.nodeSize, schema.nodes.paragraph.create(null, text)))
  }
  return (
    <NodeViewWrapper className={styles.embed} contentEditable={false} data-embed-provider={provider} data-i18n-ignore>
      <iframe allowFullScreen className={styles.frame} loading="lazy" referrerPolicy="strict-origin-when-cross-origin" sandbox="allow-scripts allow-same-origin allow-presentation allow-popups allow-popups-to-escape-sandbox" src={embedUrl} title={provider}/>
      {editor.isEditable && <button className={styles.keep} onClick={keepAsLink} type="button"><Link2 size={14}/>{t('Keep as link')}</button>}
    </NodeViewWrapper>
  )
}
