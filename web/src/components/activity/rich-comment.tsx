import { useEffect } from 'react'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import { MentionExtension } from '@/components/issue/editor/mention-extension'
import { DescriptionImage } from '@/components/issue/editor/image-extension'
import { DescriptionFile, DescriptionVideo } from '@/components/issue/editor/file-extension'
import { structuredBlocks } from '@/components/issue/editor/structured-blocks'
import { useHydrateModelsInMarkdown } from '@/hooks/use-hydrate-models-in-markdown'
import '@/components/issue/issue-description-editor.css'

export function RichComment({ body, data, version }: { body: string; data?: Record<string, unknown>; version?: number }) {
  // LS-0738 — warm store models referenced in comment markdown / links.
  useHydrateModelsInMarkdown(body)

  const selection = data?.selection as { issueId?: string; text?: string; from?: number; to?: number } | undefined
  const initial = commentContent(body, data)
  const editor = useEditor({
    immediatelyRender: false,
    editable: false,
    extensions: [StarterKit.configure({ link: { openOnClick: false, autolink: true, linkOnPaste: true } }), Markdown, MentionExtension, DescriptionImage, DescriptionFile, DescriptionVideo, ...structuredBlocks],
    content: initial.content,
    contentType: initial.contentType,
    editorProps: { attributes: { role: 'document', 'aria-label': 'Comment' } },
  })

  useEffect(() => {
    if (!editor || editor.isDestroyed) return
    const next = commentContent(body, data)
    if (next.contentType === 'json') {
      if (JSON.stringify(editor.getJSON()) === JSON.stringify(next.content)) return
      editor.commands.setContent(next.content, { contentType: 'json' })
      return
    }
    if (editor.getMarkdown() === next.content) return
    editor.commands.setContent(next.content, { contentType: 'markdown' })
  }, [body, data, version, editor])

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
