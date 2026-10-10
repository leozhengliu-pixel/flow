import { useEffect, useMemo, useRef } from 'react'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { EmbedNode } from '@/components/editor/embeds/embed-extension'
import { MentionChipNode } from '@/components/editor/mentions/mention-chip-extension'
import { useMentionConversion } from '@/components/editor/mentions/use-mention-conversion'
import { DescriptionImage } from '@/components/issue/editor/image-extension'
import { DescriptionFile, DescriptionVideo } from '@/components/issue/editor/file-extension'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import '@/components/issue/issue-description-editor.css'

export function RichComment({ body, data, version }: { body: string; data?: Record<string, unknown>; version?: number }) {
  const selection = data?.selection as { issueId?: string; text?: string; from?: number; to?: number } | undefined
  const initial = commentContent(body, data)
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    extensions: [StarterKit.configure({ link: { openOnClick: false, autolink: true, linkOnPaste: true } }), Markdown, MentionChipNode, EmbedNode, DescriptionImage, DescriptionFile, DescriptionVideo, ...structuredBlocks],
    content: initial.content,
    contentType: initial.contentType,
    editorProps: { attributes: { role: 'document', 'aria-label': 'Comment' } },
  })

  const contentKey = useMemo(() => `${version ?? ''}:${body}:${data ? JSON.stringify(data) : ''}`, [body, data, version])
  const appliedKey = useRef(contentKey)
  useEffect(() => {
    if (!editor || editor.isDestroyed || appliedKey.current === contentKey) return
    appliedKey.current = contentKey
    const next = commentContent(body, data)
    // Mention node views render with flushSync, which React rejects inside an effect, so set the content right after it.
    queueMicrotask(() => {
      if (!editor.isDestroyed) editor.commands.setContent(next.content, { contentType: next.contentType })
    })
  }, [body, contentKey, data, editor])
  // After the content effect: the references in the new content become mentions (display only).
  useMentionConversion(editor, contentKey, true, !validDocument(data))

  if (!editor) return <div aria-label="Comment" role="document"><p>{body}</p></div>
  return <>{selection?.text && <button className="comment-selection-quote" type="button" onClick={() => window.dispatchEvent(new CustomEvent('flow-reveal-description-selection', { detail: selection }))}>{selection.text}</button>}<EditorContent editor={editor}/></>
}

function commentContent(body: string, data?: Record<string, unknown>) {
  if (validDocument(data)) return { content: data, contentType: 'json' as const }
  return { content: body || ' ', contentType: 'markdown' as const }
}

function validDocument(value?: Record<string, unknown>): value is Record<string, unknown> {
  return value?.type === 'doc' && Array.isArray(value.content)
}
