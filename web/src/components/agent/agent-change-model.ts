import type { AgentChange, AgentMessage } from '@/types/flow'
import type { AgentEntityKind } from './agent-entity-refs'

/**
 * The reference app's result cards under an agent reply ("Created FLO-24", "Updated project", "Created custom view"…), built from
 * the change each successful Flow write recorded on its tool call. Writes to the same entity fold into one card: a
 * created entity's later edits are part of its "Created" card, repeated edits make one "Updated" card. Undo and Restore
 * act on every write behind a card.
 */
export type AgentChangeCard = {
  key: string
  action: AgentChange['action']
  entityType: string
  entityId?: string
  parentId?: string
  label: string
  url?: string
  /** The entity kind used to resolve the live record for the card (status, icon, owner…), when Flow can resolve it. */
  kind?: AgentEntityKind
  /** Tool-call parts behind the card (sent with Undo / Restore). */
  partIds: string[]
  fields: string[]
  /** Text fields (description / content) before the first and after the last write, for Highlight changes. */
  text?: { field: string; before: string; after: string }
  undoable: boolean
  undone: boolean
  /** Fields the last Undo / Restore left alone (they changed after the agent's edit). */
  skipped?: string[]
  /** Why an undone change has no Restore (e.g. a customer the Undo deleted permanently). */
  finalNote?: string
  /** Extra preview data the write returned (status update body and health, skill instructions). */
  preview?: { body?: string; health?: string; parent?: string; instructions?: string; key?: string }
}

type Labels = { created: string; updated: string; deleted: string }

/** Header copy and entity kind per entity type. Unknown types (new write tools) fall back to a generic card. */
export const AGENT_CHANGE_ENTITIES: Record<string, Labels & { kind?: AgentEntityKind }> = {
  issue: { created: 'Created {label}', updated: 'Updated {label}', deleted: 'Deleted {label}', kind: 'issue' },
  project: { created: 'Created project', updated: 'Updated project', deleted: 'Deleted project', kind: 'project' },
  initiative: { created: 'Created initiative', updated: 'Updated initiative', deleted: 'Deleted initiative', kind: 'initiative' },
  document: { created: 'Created document', updated: 'Updated document', deleted: 'Deleted document', kind: 'document' },
  view: { created: 'Created custom view', updated: 'Updated custom view', deleted: 'Deleted custom view', kind: 'view' },
  skill: { created: 'Created skill', updated: 'Updated skill', deleted: 'Deleted skill' },
  milestone: { created: 'Created milestone', updated: 'Updated milestone', deleted: 'Deleted milestone', kind: 'milestone' },
  project_update: { created: 'Created project update', updated: 'Updated project update', deleted: 'Deleted project update' },
  initiative_update: { created: 'Created initiative update', updated: 'Updated initiative update', deleted: 'Deleted initiative update' },
  customer: { created: 'Created customer', updated: 'Updated customer', deleted: 'Deleted customer', kind: 'customer' },
  customer_request: { created: 'Created customer request', updated: 'Updated customer request', deleted: 'Deleted customer request' },
  cycle: { created: 'Created cycle', updated: 'Updated cycle', deleted: 'Deleted cycle', kind: 'cycle' },
  label: { created: 'Created label', updated: 'Updated label', deleted: 'Deleted label' },
  release: { created: 'Created release', updated: 'Updated release', deleted: 'Deleted release' },
  team: { created: 'Created team', updated: 'Updated team', deleted: 'Deleted team' },
  template: { created: 'Created template', updated: 'Updated template', deleted: 'Deleted template' },
}

const GENERIC: Labels = { created: 'Created item', updated: 'Updated item', deleted: 'Deleted item' }

/** Entity types that have a card of their own elsewhere (the loop builder shows its draft) get none here. */
const WITHOUT_CARD = new Set(['loop'])

const TEXT_FIELDS = ['description', 'content']

/** Field names as the update summary reads them ("Changed the description and status"). */
export const AGENT_CHANGE_FIELD_LABELS: Record<string, string> = {
  title: 'title', name: 'name', description: 'description', content: 'content', summary: 'summary', state: 'status',
  status: 'status', priority: 'priority', assignee: 'assignee', lead: 'lead', owner: 'owner', project: 'project',
  cycle: 'cycle', labels: 'labels', parentId: 'parent issue', dueDate: 'due date', targetDate: 'target date',
  startDate: 'start date', estimate: 'estimate', icon: 'icon', color: 'color', setTeams: 'teams',
  setInitiatives: 'initiatives', leadTeam: 'lead team', instructions: 'instructions',
}

export function agentChangeHeader(card: Pick<AgentChangeCard, 'action' | 'entityType' | 'label'>, t: (source: string) => string) {
  const labels = AGENT_CHANGE_ENTITIES[card.entityType] ?? GENERIC
  return t(labels[card.action]).replace('{label}', card.label)
}

function fieldList(fields: string[], t: (source: string) => string, locale: string) {
  const names = [...new Set(fields.map(field => t(AGENT_CHANGE_FIELD_LABELS[field] ?? field)))]
  if (locale.startsWith('zh')) return names.join('、')
  return names.length < 2 ? names.join('') : `${names.slice(0, -1).join(', ')} ${t('and')} ${names.at(-1)}`
}

/** "Changed the description and summary." — the one-line summary on an "Updated" card. */
export function agentChangeSummary(card: Pick<AgentChangeCard, 'fields'>, t: (source: string) => string, locale = 'en') {
  if (!card.fields.length) return ''
  return t('Changed the {fields}.').replace('{fields}', fieldList(card.fields, t, locale))
}

/** Why an Undo / Restore left fields alone ("Left the description as is: it changed after the agent’s edit."). */
export function agentChangeSkippedNote(skipped: string[] | undefined, t: (source: string) => string, locale = 'en') {
  if (!skipped?.length) return ''
  return t('Left the {fields} as is: it changed after the agent’s edit.').replace('{fields}', fieldList(skipped, t, locale))
}

/** The server's Undo / Restore refusals in the viewer's language. */
export function agentChangeErrorMessage(message: string, t: (source: string) => string) {
  if (message.includes('changed after the agent')) return t('This changed after the agent’s edit, so nothing was undone.')
  if (message.includes('no longer in the trash')) return t('This item is no longer in the trash.')
  if (message.includes('no longer exists')) return t('This item no longer exists.')
  if (message.includes('nothing to undo')) return t('There is nothing to undo in this reply.')
  if (message.includes('nothing to restore')) return t('There is nothing to restore in this reply.')
  return message || t('Could not undo the change')
}

/** Entity keys ("document:doc_1") that have a result card, so the reference cards under the answer skip them. */
export function agentChangeEntityKeys(message: Pick<AgentMessage, 'parts'>) {
  return agentChangeCards(message).filter(card => card.kind && card.entityId).flatMap(card => {
    const keys = [`${card.kind}:${card.entityId}`]
    // An issue the page has not loaded yet is referenced by its identifier ("DEV-12"), which is the card's label.
    if (card.kind === 'issue' && /^[A-Z][A-Z0-9]*-\d+$/i.test(card.label)) keys.push(`issue:${card.label.toUpperCase()}`)
    return keys
  })
}

function textValue(value: unknown) {
  return typeof value === 'string' ? value : ''
}

function previewOf(change: AgentChange, args: Record<string, unknown> | undefined, result: unknown): AgentChangeCard['preview'] {
  if (change.entityType.endsWith('_update') && change.after) {
    return { body: textValue(change.after.body), health: textValue(change.after.health), parent: textValue(change.after.parent) }
  }
  if (change.entityType === 'skill') {
    const instructions = textValue((result as Record<string, unknown> | undefined)?.instructions) || textValue(args?.instructions)
    return instructions ? { instructions } : undefined
  }
  if (change.entityType === 'team') {
    // The team's key ("INV") for the card's right edge, while the new team is not in the client's data yet.
    const key = textValue((result as Record<string, unknown> | undefined)?.key) || textValue(args?.key)
    return key ? { key } : undefined
  }
  return undefined
}

export function agentChangeCards(message: Pick<AgentMessage, 'parts'>): AgentChangeCard[] {
  const cards: AgentChangeCard[] = []
  const byEntity = new Map<string, AgentChangeCard>()
  for (const part of message.parts ?? []) {
    const call = part.toolCall
    const change = call?.change
    if (!call || !change || call.status !== 'completed' || WITHOUT_CARD.has(change.entityType)) continue
    const entityKey = change.entityId ? `${change.entityType}:${change.entityId}` : `${change.entityType}:${part.id}`
    const existing = byEntity.get(entityKey)
    const before = change.before ?? {}
    const after = change.after ?? {}
    const textField = (change.fields ?? []).find(field => TEXT_FIELDS.includes(field))
    if (existing) {
      // Edits right after creating are part of the "Created" card: undoing the create removes them too.
      if (existing.action === 'created' && change.action === 'updated') {
        existing.label = change.label || existing.label
        continue
      }
      existing.partIds.push(part.id)
      existing.fields = [...new Set([...existing.fields, ...(change.fields ?? [])])]
      existing.label = change.label || existing.label
      existing.undoable = existing.undoable && change.undoable
      existing.undone = existing.undone && change.state === 'undone'
      if (change.skipped?.length) existing.skipped = [...new Set([...(existing.skipped ?? []), ...change.skipped])]
      if (change.action === 'deleted') {
        existing.undoable = existing.undoable && existing.action !== 'created'
        existing.action = 'deleted'
      }
      if (textField) {
        existing.text = existing.text?.field === textField
          ? { ...existing.text, after: textValue(after[textField]) }
          : { field: textField, before: textValue(before[textField]), after: textValue(after[textField]) }
      }
      continue
    }
    const labels = AGENT_CHANGE_ENTITIES[change.entityType]
    const card: AgentChangeCard = {
      key: part.id,
      action: change.action,
      entityType: change.entityType,
      entityId: change.entityId,
      parentId: change.parentId,
      label: change.label || '',
      url: change.url || undefined,
      kind: labels?.kind,
      partIds: [part.id],
      fields: change.fields ?? [],
      text: textField ? { field: textField, before: textValue(before[textField]), after: textValue(after[textField]) } : undefined,
      undoable: change.undoable,
      undone: change.state === 'undone',
      skipped: change.skipped?.length ? change.skipped : undefined,
      finalNote: change.state === 'undone' && !change.undoable && change.note ? change.note : undefined,
      preview: previewOf(change, call.arguments, call.result),
    }
    byEntity.set(entityKey, card)
    cards.push(card)
  }
  return cards
}

/** The blocks (paragraphs, list items, headings) of Markdown, trimmed, as Highlight changes compares them. */
export function markdownBlocks(markdown: string) {
  return markdown.split(/\n\s*\n|\n(?=\s*(?:[-*+]|\d+\.)\s|#)/).map(block => block.trim()).filter(Boolean)
}

/** Plain text of a Markdown block as an editor shows it (markers, emphasis and link targets removed, spaces collapsed). */
export function markdownPlainText(block: string) {
  return block
    .replace(/^\s*(?:#{1,6}\s+|[-*+]\s+(?:\[[ xX]\]\s+)?|\d+\.\s+|>\s?)/gm, '')
    .replace(/!?\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/(\*\*|__|\*|_|~~|`)/g, '')
    .replace(/\s+/g, ' ')
    .trim()
}

export type AgentTextDiffLine = { kind: 'added' | 'removed' | 'same'; text: string }

/** Block-level diff of two Markdown texts (longest common subsequence), for the in-card diff and the editor highlight. */
export function diffMarkdownBlocks(before: string, after: string): AgentTextDiffLine[] {
  const left = markdownBlocks(before)
  const right = markdownBlocks(after)
  const table = Array.from({ length: left.length + 1 }, () => new Array<number>(right.length + 1).fill(0))
  for (let i = left.length - 1; i >= 0; i--) {
    for (let j = right.length - 1; j >= 0; j--) {
      table[i][j] = left[i] === right[j] ? table[i + 1][j + 1] + 1 : Math.max(table[i + 1][j], table[i][j + 1])
    }
  }
  const lines: AgentTextDiffLine[] = []
  let i = 0
  let j = 0
  while (i < left.length || j < right.length) {
    if (i < left.length && j < right.length && left[i] === right[j]) {
      lines.push({ kind: 'same', text: left[i] })
      i++
      j++
    } else if (j < right.length && (i >= left.length || table[i][j + 1] >= table[i + 1][j])) {
      lines.push({ kind: 'added', text: right[j++] })
    } else {
      lines.push({ kind: 'removed', text: left[i++] })
    }
  }
  return lines
}

/** Plain text of every block the agent added or rewrote: what Highlight changes marks in the live editor. */
export function changedBlockTexts(before: string, after: string) {
  return diffMarkdownBlocks(before, after).filter(line => line.kind === 'added').map(line => markdownPlainText(line.text)).filter(Boolean)
}
