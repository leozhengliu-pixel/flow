/**
 * The selection toolbar's actions in a comment composer (a new comment, a reply, an edit):
 * - "Create issue from selection" (⌘⇧O) creates the issue right away — the selection's first line is its title and the
 *   rest its description, in the page's team and project, a sub-issue when the composer is on an issue — and the
 *   selected text becomes a mention of the new issue; an "Issue created" toast links to it.
 * - "Ask agent" (⌘J) opens the floating agent chat with the selection as a quote chip (the page rides along as context).
 * There is no "Comment" action in a composer.
 */
import type { Editor } from '@tiptap/core'
import { createContext, useContext, useMemo } from 'react'
import { createIssue } from '@/lib/api'
import { toast } from '@/lib/toast'
import { useI18n } from '@/i18n/i18n'
import { useAgentEntityData } from '@/components/agent/agent-entity-data'
import { requestFloatingAgent } from '@/components/agent/agent-floating-request'
import { mentionAttrsForEntity } from '@/components/editor/mentions/mention-model'
import type { DescriptionSelectionActions, SelectedDescription } from '@/components/issue/editor/structured-blocks'
import type { BootstrapData, Issue } from '@/types/flow'

/** Where an issue made from a composer's selection goes: the page's team and project, and its parent on an issue page. */
export type SelectionIssueContext = { teamId?: string; projectId?: string; parentId?: string }

/** Provided by the page (see SelectionIssueProvider); without it an issue goes to the workspace's first team. */
export const SelectionIssueContextValue = createContext<SelectionIssueContext>({})

/** The selection's first non-empty line is the title (at most 255 characters); the lines after it are the description. */
export function selectionIssueFields(text: string): { title: string; description: string } | undefined {
  const lines = text.replace(/\r\n?/g, '\n').replace(/[​⁠﻿]/g, '').split('\n')
  const first = lines.findIndex(line => line.trim())
  if (first < 0) return undefined
  return { title: lines[first].trim().slice(0, 255), description: lines.slice(first + 1).join('\n').trim() }
}

export type InlineIssueInput = Parameters<typeof createIssue>[0]

/** The app creates the issue (adding it to the workspace data and showing "Issue created") without leaving the page. */
export const CREATE_ISSUE_INLINE_EVENT = 'flow:create-issue-inline'
export type InlineIssueRequest = { input: InlineIssueInput; result?: Promise<Issue> }

export function createIssueInline(input: InlineIssueInput): Promise<Issue> {
  const detail: InlineIssueRequest = { input }
  window.dispatchEvent(new CustomEvent<InlineIssueRequest>(CREATE_ISSUE_INLINE_EVENT, { detail }))
  return detail.result ?? createIssue(input)
}

/**
 * Puts a mention of the issue where the selection was (the text is gone), followed by a space unless one
 * is already there, and leaves the caret after it. If the text moved meanwhile, the mention goes at the caret.
 */
export function replaceSelectionWithIssue(editor: Editor | undefined, selection: SelectedDescription, data: BootstrapData | undefined, issue: Issue) {
  if (!editor || editor.isDestroyed) return
  const { state } = editor
  const node = state.schema.nodes.mention?.create(mentionAttrsForEntity(data, { kind: 'issue', issue }))
  if (!node) return
  const size = state.doc.content.size
  const intact = selection.to <= size && state.doc.textBetween(selection.from, selection.to, '\n') === selection.text
  const from = intact ? selection.from : state.selection.from
  const tr = state.tr.replaceWith(from, intact ? selection.to : state.selection.to, node)
  let caret = from + node.nodeSize
  const next = tr.doc.textBetween(caret, Math.min(caret + 1, tr.doc.content.size), '\n', '\n')
  if (!/^\s/.test(next)) { tr.insertText(' ', caret); caret += 1 }
  editor.view.dispatch(tr)
  editor.chain().focus().setTextSelection(caret).run()
}

/** The status a new issue of the team starts in, as in the New issue dialog: the team's default, else its first unstarted one. */
export function teamDefaultStateId(data: BootstrapData | undefined, teamId: string) {
  if (!data) return undefined
  const specific = data.states.some(state => state.teamId === teamId)
  const states = data.states.filter(state => specific ? state.teamId === teamId : !state.teamId).sort((a, b) => (a.position ?? 0) - (b.position ?? 0))
  const configured = data.teamSettings?.[teamId]?.defaultStateId
  return (states.find(state => state.id === configured) ?? states.find(state => state.default) ?? states.find(state => state.type === 'unstarted') ?? states[0])?.id
}

/** Creates an issue from a composer's selection and swaps the selected text for a mention of it. */
export async function createIssueFromSelection({ editor, selection, data, context }: { editor?: Editor; selection: SelectedDescription; data?: BootstrapData; context: SelectionIssueContext }) {
  const fields = selectionIssueFields(selection.text)
  const teamId = context.teamId ?? data?.teams[0]?.id
  if (!fields || !teamId) return undefined
  // Made in the app by a member, so it skips Triage like an issue from the New issue dialog.
  const issue = await createIssueInline({ ...fields, teamId, stateId: teamDefaultStateId(data, teamId), projectId: context.projectId, parentId: context.parentId })
  replaceSelectionWithIssue(editor, selection, data, issue)
  return issue
}

/** "Create issue from selection" and "Ask agent" for a comment composer's selection toolbar. */
export function useComposerSelectionActions(): DescriptionSelectionActions {
  const data = useAgentEntityData()
  const context = useContext(SelectionIssueContextValue)
  const { t } = useI18n()
  const canCreate = Boolean(context.teamId ?? data?.teams[0]?.id)
  return useMemo(() => ({
    onCreateIssue: canCreate ? (selection, editor) => {
      void createIssueFromSelection({ editor, selection, data, context }).catch(error => toast.error(error instanceof Error ? error.message : t('Could not create issue')))
    } : undefined,
    // Like the reference app: the selection goes to the floating chat as a chip and the composer lets go of it (the toolbar closes).
    onAskAgent: (selection, editor) => {
      if (!selection.text.trim()) return
      if (editor && !editor.isDestroyed) editor.commands.setTextSelection(selection.to)
      requestFloatingAgent({ quote: selection.text })
    },
  }), [canCreate, context, data, t])
}
