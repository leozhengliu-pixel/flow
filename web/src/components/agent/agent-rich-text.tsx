import { useEffect } from 'react'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import { TableKit } from '@tiptap/extension-table'
import StarterKit from '@tiptap/starter-kit'
import { FlowCodeBlock } from '@/components/editor/code-block/code-block-extension'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { AgentEntityNode } from './agent-entity-node'
import styles from './agent-rich-text.module.css'
import './agent-chat-document.css'

export function AgentRichText({ ariaLabel = 'AI message', className = 'agent-rich-text', content: text }: { ariaLabel?: string; className?: string; content: string | undefined }) {
  // An activity can arrive without a body (an action or an empty reply): show nothing rather than fail.
  const content = text ?? ''
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    // Code blocks use the editor's node view: line numbers, hover header (language, settings, copy) and syntax highlighting.
    extensions: [StarterKit.configure({ codeBlock: false }), FlowCodeBlock, Markdown, ...structuredBlocks, TableKit.configure({ table: { resizable: false } }), AgentEntityNode],
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
