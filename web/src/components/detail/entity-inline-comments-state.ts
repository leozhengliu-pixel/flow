import type { Editor } from '@tiptap/react'
import { useCallback, useMemo, useState } from 'react'
import type { Comment } from '@/types/flow'
import { partitionAnchoredComments } from '@/components/documents/inline-comments/inline-comments-model'
import type { CommentDraft } from '@/components/documents/inline-comments/inline-comment-gutter'
import { useInlineThreads } from '@/components/documents/inline-comments/use-inline-threads'
import type { SelectedDescription } from '@/components/issue/editor/structured-blocks'

/**
 * The inline comment state of one project or initiative description: the live editor and its positioned wrapper (both
 * set through refs), the comment being written, whether gutter cards push the column left, and the entity's inline
 * threads (its page-level comments belong to Activity).
 */
export function useEntityInlineComments(comments: Comment[] | undefined) {
  const [editor, setEditor] = useState<Editor | null>(null)
  const [shell, setShell] = useState<HTMLElement | null>(null)
  const [draft, setDraft] = useState<CommentDraft>()
  const [gutter, setGutter] = useState(false)
  const anchored = useMemo(() => partitionAnchoredComments(comments ?? []).anchored, [comments])
  // Like the reference app: the composer opens beside the selection and keystrokes go to it, never over the selected text.
  const startComment = useCallback((selection: SelectedDescription) => {
    if (!selection.text.trim()) return
    setDraft({ from: selection.from, to: selection.to, text: selection.text })
    if (editor && !editor.isDestroyed) { editor.commands.setTextSelection(selection.to); editor.view.dom.blur(); window.getSelection()?.removeAllRanges() }
  }, [editor])
  return { editor, attachEditor: setEditor, shell, attachShell: setShell, draft, setDraft, gutter, setGutter, anchored, startComment }
}

export type EntityInlineCommentsState = ReturnType<typeof useEntityInlineComments>

/** The number of resolved inline threads (the page header shows "Show resolved comments" while there are any). */
export function useResolvedInlineThreadCount(comments: Comment[] | undefined) {
  const anchored = useMemo(() => partitionAnchoredComments(comments ?? []).anchored, [comments])
  return useInlineThreads(anchored).resolved.length
}
