import { useEffect } from 'react'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import { TableKit } from '@tiptap/extension-table'
import StarterKit from '@tiptap/starter-kit'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { AgentEntityNode } from './agent-entity-node'
import styles from './agent-rich-text.module.css'

export function AgentRichText({ ariaLabel = 'AI message', className, content }: { ariaLabel?: string; className: string; content: string }) {
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    extensions: [StarterKit, Markdown, ...structuredBlocks, TableKit.configure({ table: { resizable: false } }), AgentEntityNode],
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
