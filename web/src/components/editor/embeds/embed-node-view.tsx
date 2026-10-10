import { TextSelection } from '@tiptap/pm/state'
import { NodeViewWrapper, type ReactNodeViewProps } from '@tiptap/react'
import type { ReactNode } from 'react'
import { embedLayoutFor } from './embed-providers'
import { EmbedToolbar } from './embed-toolbar'
import { FilePreviewCard } from './file-preview-card'
import { LinkPreviewCard } from './link-preview-card'
import styles from './embed.module.css'

const PLAYER_SANDBOX = 'allow-scripts allow-same-origin allow-presentation allow-popups allow-popups-to-escape-sandbox'

type EmbedAttrs = { embedUrl: string; provider: string; src: string }

/**
 * What an embed shows (the player iframe, a link card or the file preview card), with the shared hover toolbar. The same body is used by
 * the editors' node view and by read-only renderings such as the document history, so they cannot drift apart.
 * `shield` lays a transparent layer over a player so a first click selects the block instead of landing in the iframe.
 */
export function EmbedBody({ embedUrl, provider, src, toolbar, shield }: EmbedAttrs & { toolbar: ReactNode; shield?: ReactNode }) {
  const layout = embedLayoutFor(provider)
  const frameClass = layout.height ? styles.frame : layout.aspect === 'tall' ? `${styles.frame} ${styles.tall}` : `${styles.frame} ${styles.video}`
  return (
    <>
      {layout.kind === 'card'
        ? <FilePreviewCard compactHeader provider={provider} src={src}/>
        : layout.kind === 'link'
          ? <LinkPreviewCard provider={provider} src={src}/>
          : <iframe allowFullScreen className={frameClass} loading="lazy" referrerPolicy="strict-origin-when-cross-origin" sandbox={PLAYER_SANDBOX} src={embedUrl} style={layout.height ? { height: layout.height } : undefined} title={provider}/>}
      {layout.kind === 'iframe' && shield}
      {toolbar}
    </>
  )
}

/** The block's class names: every embed spans the content column; a selected block gets the selection ring. */
function embedBlockClass(provider: string, selected = false) {
  const kind = embedLayoutFor(provider).kind
  return [styles.embed, kind === 'card' ? styles.cardWrap : kind === 'link' ? styles.linkWrap : '', selected ? styles.selected : ''].filter(Boolean).join(' ')
}

/** A saved embed in a read-only rendering (document history): the player or file card with only Open link and Copy link. */
export function ReadOnlyEmbed({ embedUrl, provider, src }: EmbedAttrs) {
  return (
    <div className={embedBlockClass(provider)} data-embed-provider={provider} data-i18n-ignore>
      <EmbedBody embedUrl={embedUrl} provider={provider} src={src} toolbar={<EmbedToolbar src={src}/>}/>
    </div>
  )
}

/** The inline player / card for a pasted media link (layout by provider), with Linear's hover toolbar while editing. */
export function EmbedView({ editor, getPos, node, selected }: ReactNodeViewProps) {
  const { embedUrl, provider, src } = node.attrs as EmbedAttrs
  const editable = editor.isEditable
  const convertToLink = () => {
    const pos = getPos()
    if (typeof pos !== 'number') return
    const { schema } = editor.state
    const paragraph = schema.nodes.paragraph.create(null, schema.text(src, [schema.marks.link.create({ href: src })]))
    const tr = editor.state.tr.replaceWith(pos, pos + node.nodeSize, paragraph)
    editor.view.dispatch(tr.setSelection(TextSelection.create(tr.doc, pos + 1 + src.length)).scrollIntoView())
    editor.view.focus()
  }
  const remove = () => {
    const pos = getPos()
    if (typeof pos !== 'number') return
    // An embed that was the whole document leaves an empty paragraph behind (a document needs at least one block).
    const tr = editor.state.tr.delete(pos, pos + node.nodeSize)
    if (!tr.doc.childCount) tr.insert(0, editor.state.schema.nodes.paragraph.create())
    editor.view.dispatch(tr.setSelection(TextSelection.near(tr.doc.resolve(Math.min(pos, tr.doc.content.size)), -1)).scrollIntoView())
    editor.view.focus()
  }
  return (
    <NodeViewWrapper className={embedBlockClass(provider, selected)} contentEditable={false} data-embed-provider={provider} data-i18n-ignore>
      <EmbedBody
        embedUrl={embedUrl}
        provider={provider}
        shield={editable && !selected ? <span aria-hidden="true" className={styles.shield} data-embed-shield/> : null}
        src={src}
        toolbar={<EmbedToolbar onConvert={editable ? convertToLink : undefined} onDelete={editable ? remove : undefined} src={src}/>}
      />
    </NodeViewWrapper>
  )
}
