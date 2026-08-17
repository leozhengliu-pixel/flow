import StarterKit from '@tiptap/starter-kit'
import { EditorContent, useEditor } from '@tiptap/react'

export function RichComment({ body, data }: { body: string; data?: Record<string, unknown> }) {
  const editor = useEditor({ immediatelyRender: false, editable: false, extensions: [StarterKit], content: validDocument(data) ? data : body, editorProps: { attributes: { role: 'document', 'aria-label': 'Comment' } } })
  return <EditorContent editor={editor}/>
}

function validDocument(value?: Record<string, unknown>): value is Record<string, unknown> { return value?.type === 'doc' && Array.isArray(value.content) }
