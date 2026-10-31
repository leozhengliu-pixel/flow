import { useEffect } from 'react'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import { TableKit } from '@tiptap/extension-table'
import { FlowStarterKit } from '@/components/editor/flow-starter-kit'
import { LinkBehavior } from '@/components/editor/link-behavior'
import { FlowCodeBlock } from '@/components/editor/code-block/code-block-extension'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { MathBlock } from '@/components/issue/editor/math-extension'
import { AgentEntityNode } from './agent-entity-node'
import styles from './agent-rich-text.module.css'
import './agent-chat-document.css'

export function AgentRichText({ ariaLabel = 'AI message', className = 'agent-rich-text', content: text, autoLinkEmails = true }: { ariaLabel?: string; className?: string; content: string | undefined; /** User prompts keep plain email addresses as text, like the reference app. */ autoLinkEmails?: boolean }) {
  // An activity can arrive without a body (an action or an empty reply): show nothing rather than fail.
  const content = text ?? ''
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    // Code blocks use the editor's node view: line numbers, hover header (language, settings, copy) and syntax highlighting.
    extensions: [FlowStarterKit.configure({ codeBlock: false, link: { openOnClick: false, ...(autoLinkEmails ? {} : { shouldAutoLink: (url: string) => !/^mailto:/i.test(url) }) } }), LinkBehavior, FlowCodeBlock, Markdown, MathBlock, ...structuredBlocks, TableKit.configure({ table: { resizable: false } }), AgentEntityNode],
    content: content || ' ',
    contentType: 'markdown',
    editorProps: { attributes: { class: `${className} ${styles.richText}`, role: 'document', 'aria-label': ariaLabel } },
  })

  useEffect(() => {
    if (!editor || editor.isDestroyed || editor.getMarkdown() === content) return
    editor.commands.setContent(content || ' ', { contentType: 'markdown' })
  }, [content, editor])

  // Marked auto-links bare email addresses while parsing Markdown; user prompts keep them as plain text.
  useEffect(() => {
    if (!editor || editor.isDestroyed || autoLinkEmails) return
    const linkType = editor.schema.marks.link
    if (!linkType) return
    let tr = editor.state.tr
    let changed = false
    editor.state.doc.descendants((node, pos) => {
      if (!node.isText || !node.marks.some(mark => mark.type === linkType && /^mailto:/i.test(String(mark.attrs.href)))) return
      tr = tr.removeMark(pos, pos + node.nodeSize, linkType)
      changed = true
    })
    if (changed) editor.view.dispatch(tr)
  }, [autoLinkEmails, content, editor])

  // Before the editor mounts, show entity shortcodes as their plain labels.
  if (!editor) return <div aria-label={ariaLabel} className={className} role="document"><p>{content.replace(/\[agentEntity [^\]]*?label="([^"]*)"[^\]]*\]/g, '$1')}</p></div>
  return <EditorContent editor={editor}/>
}
