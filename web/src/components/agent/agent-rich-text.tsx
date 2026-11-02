import { useEffect } from 'react'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import { TableKit } from '@tiptap/extension-table'
import { Link } from '@tiptap/extension-link'
import { FlowStarterKit } from '@/components/editor/flow-starter-kit'
import { LinkBehavior } from '@/components/editor/link-behavior'
import { FlowCodeBlock } from '@/components/editor/code-block/code-block-extension'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { MathBlock } from '@/components/issue/editor/math-extension'
import { AgentEntityNode } from './agent-entity-node'
import styles from './agent-rich-text.module.css'
import './agent-chat-document.css'

const UserMessageLink = Link.extend({
  parseMarkdown(token, helpers) {
    const children = helpers.parseInline(token.tokens || [])
    // The Markdown lexer also emits link tokens for bare addresses.
    if (/^mailto:/i.test(String(token.href)) && !token.raw?.startsWith('[') && !token.raw?.startsWith('<')) return children
    return helpers.applyMark('link', children, { href: token.href, title: token.title || null })
  },
})

export function AgentRichText({ ariaLabel = 'AI message', className = 'agent-rich-text', content: text, autoLinkEmails = true }: { ariaLabel?: string; className?: string; content: string | undefined; /** User prompts keep plain email addresses as text, like the reference app. */ autoLinkEmails?: boolean }) {
  // An activity can arrive without a body (an action or an empty reply): show nothing rather than fail.
  const content = text ?? ''
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    // Code blocks use the editor's node view: line numbers, hover header (language, settings, copy) and syntax highlighting.
    extensions: [FlowStarterKit.configure({ codeBlock: false, link: false }), (autoLinkEmails ? Link : UserMessageLink).configure({ openOnClick: false, ...(autoLinkEmails ? {} : { shouldAutoLink: (url: string) => !/^mailto:/i.test(url) }) }), LinkBehavior, FlowCodeBlock, Markdown, MathBlock, ...structuredBlocks, TableKit.configure({ table: { resizable: false } }), AgentEntityNode],
    content: content || ' ',
    contentType: 'markdown',
    editorProps: { attributes: { class: `${className} ${styles.richText}`, role: 'document', 'aria-label': ariaLabel } },
  })

  useEffect(() => {
    if (!editor || editor.isDestroyed || editor.getMarkdown() === content) return
    editor.commands.setContent(content || ' ', { contentType: 'markdown' })
  }, [content, editor])

  // Before the editor mounts, show entity shortcodes as their plain labels.
  if (!editor) return <div aria-label={ariaLabel} className={className} role="document"><p>{content.replace(/\[agentEntity [^\]]*?label="([^"]*)"[^\]]*\]/g, '$1')}</p></div>
  return <EditorContent editor={editor}/>
}
