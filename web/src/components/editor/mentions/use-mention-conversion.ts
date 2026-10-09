import { useEffect, useState } from 'react'
import type { Editor } from '@tiptap/react'
import { Node as ProseNode } from '@tiptap/pm/model'
import { loadAgentRecord, useAgentRecordVersion } from '@/components/agent/agent-entity-fetch'
import { useAgentEntityData } from '@/components/agent/agent-entity-data'
import { convertMentionLinks } from './mention-model'
import type { BootstrapData } from '@/types/flow'

/**
 * Replaces the references in an editor's document (links to Flow resources, team-key identifiers, bare URLs, @names) with
 * mention nodes, as a display-only change: it is not an edit (no undo step, no update event), so opening a description never
 * saves it. Returns the issues / projects the client does not hold, which the caller fetches by id before converting again.
 */
export function convertEditorMentions(editor: Editor, data: BootstrapData, anyText = false) {
  if (editor.isDestroyed || !editor.schema.nodes.mention) return []
  const result = convertMentionLinks(editor.getJSON(), data, { text: true, anyText })
  if (!result.changed) return result.unresolved
  const { state } = editor
  const next = ProseNode.fromJSON(editor.schema, result.content)
  editor.view.dispatch(state.tr.replaceWith(0, state.doc.content.size, next.content).setMeta('addToHistory', false).setMeta('preventUpdate', true))
  return result.unresolved
}

/**
 * Keeps an editor's references as mentions while its content comes from markdown or older JSON: converts after the content
 * changes (`contentKey`) and again when records the content names finish loading (paged workspaces). Skip it for
 * collaborative documents, whose converted nodes would become shared edits.
 */
export function useMentionConversion(editor: Editor | null, contentKey: string, enabled = true, anyText = false) {
  const data = useAgentEntityData()
  // Only a surface that waits on fetched records re-renders when records arrive.
  const [waiting, setWaiting] = useState(false)
  const records = useAgentRecordVersion(waiting)
  useEffect(() => {
    if (!editor || !data || !enabled || editor.isDestroyed || editor.isFocused) return
    const unresolved = convertEditorMentions(editor, data, anyText)
    const fetchable = unresolved.filter(target => target.kind === 'issue' || target.kind === 'project')
    for (const target of fetchable) loadAgentRecord(data.workspace.urlKey, target.kind as 'issue' | 'project', target.id)
    setWaiting(fetchable.length > 0)
  }, [anyText, contentKey, data, editor, enabled, records])
}
