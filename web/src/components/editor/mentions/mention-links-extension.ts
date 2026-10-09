import { Extension, InputRule, type JSONContent } from '@tiptap/core'
import { Fragment, Slice } from '@tiptap/pm/model'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'
import { peekAgentRecord, settleAgentRecords } from '@/components/agent/agent-entity-fetch'
import type { AgentEntityTarget } from '@/components/agent/agent-entity-refs'
import type { BootstrapData } from '@/types/flow'
import { convertMentionLinks, mentionAttrsForEntity, mentionAttrsForTarget, mentionKind, mentionTarget, resolveMentionEntity, typedIdentifierTarget, type MentionAttrs } from './mention-model'

const pluginKey = new PluginKey('mentionLinks')

/** One clipboard URL (nothing else) pasted over a selection makes the selection a link, as in Linear. */
function isSingleUrl(slice: Slice) {
  const text = slice.content.textBetween(0, slice.content.size, '\n', ' ').trim()
  return /^(?:https?:\/\/|\/)\S+$/.test(text)
}

/** Fills in the label / title / path of issue and project mentions once the records they name were fetched by id. */
export function refreshMentionAttrs(view: EditorView, data: BootstrapData) {
  if (view.isDestroyed) return
  const { tr } = view.state
  let changed = false
  view.state.doc.descendants((node, pos) => {
    if (node.type.name !== 'mention') return
    const attrs = node.attrs as MentionAttrs
    const kind = mentionKind(attrs, data)
    if (kind !== 'issue' && kind !== 'project') return
    const target = mentionTarget(attrs, data)
    const entity = target && resolveMentionEntity(data, target)
    if (!entity) return
    const next = mentionAttrsForEntity(data, entity)
    if (next.label === attrs.label && next.title === attrs.title && (attrs.href || !next.href) && next.id === attrs.id) return
    tr.setNodeMarkup(pos, undefined, { ...attrs, ...next, href: attrs.href || next.href })
    changed = true
  })
  if (changed) view.dispatch(tr.setMeta('addToHistory', false))
}

function hydrateAfterFetch(view: EditorView, data: BootstrapData, targets: AgentEntityTarget[]) {
  if (!targets.length) return
  void settleAgentRecords(data.workspace.urlKey, targets).then(() => refreshMentionAttrs(view, data))
}

/** Converts a typed identifier the client did not hold once the issue was fetched: the first plain-text match in the cursor's block. */
function convertTypedIdentifierAfterFetch(view: EditorView, data: BootstrapData, target: AgentEntityTarget) {
  void settleAgentRecords(data.workspace.urlKey, [target]).then(() => {
    if (view.isDestroyed || !peekAgentRecord(data.workspace.urlKey, 'issue', target.id)) return
    const { schema, selection } = view.state
    const mention = schema.nodes.mention
    if (!mention) return
    const block = selection.$from.parent
    const start = selection.$from.start()
    let from = -1
    block.descendants((node, offset) => {
      if (from >= 0 || !node.isText || node.marks.some(mark => mark.type.name === 'code' || mark.type.name === 'link')) return
      const index = (node.text ?? '').toUpperCase().search(new RegExp(`(?<![\\w/#.@-])${target.label}(?![\\w-])`))
      if (index >= 0) from = start + offset + index
    })
    if (from < 0) return
    const attrs = mentionAttrsForTarget(data, target)
    view.dispatch(view.state.tr.replaceWith(from, from + target.label.length, mention.create(attrs)).setMeta('addToHistory', false))
  })
}

export type MentionLinksOptions = { getData: () => BootstrapData | undefined }

/**
 * Turns pasted Flow URLs, identifiers and links into mentions, and typed team-key identifiers ("DEV-12 ") into issue
 * mentions: the paste and typing half of Linear's hydration plugin. Backspace straight after a typed conversion
 * restores the text, and a URL pasted over selected text still becomes that text's link.
 */
export const MentionLinksExtension = Extension.create<MentionLinksOptions>({
  name: 'mentionLinks',
  addOptions() {
    return { getData: () => undefined }
  },
  addProseMirrorPlugins() {
    const { getData } = this.options
    return [new Plugin({
      key: pluginKey,
      props: {
        transformPasted: (slice, view) => {
          const data = getData()
          if (!data || !view.editable || !view.state.schema.nodes.mention) return slice
          if (!view.state.selection.empty && isSingleUrl(slice)) return slice
          const blocks = (slice.content.toJSON() ?? []) as JSONContent[]
          const result = convertMentionLinks({ type: 'doc', content: blocks }, data, { text: true })
          if (!result.changed) return slice
          queueMicrotask(() => hydrateAfterFetch(view, data, result.unresolved))
          return new Slice(Fragment.fromJSON(view.state.schema, result.content.content ?? []), slice.openStart, slice.openEnd)
        },
      },
    })]
  },
  addInputRules() {
    const { getData } = this.options
    return [new InputRule({
      find: /(?<![\w/#.@-])([A-Za-z][A-Za-z0-9]*-\d+)([\s.,;:!?)])$/,
      handler: ({ state, range, match, chain }) => {
        const data = getData()
        const mention = state.schema.nodes.mention
        if (!data || !mention || !this.editor.isEditable) return null
        const target = typedIdentifierTarget(data, match[1])
        if (!target) return null
        if (state.doc.resolve(range.from).parent.type.name === 'codeBlock') return null
        const held = resolveMentionEntity(data, target)
        if (!held) {
          const view = this.editor.view
          queueMicrotask(() => convertTypedIdentifierAfterFetch(view, data, target))
          return null
        }
        const from = range.from + match[0].length - match[2].length - match[1].length
        chain().insertContentAt({ from, to: range.to }, [{ type: 'mention', attrs: mentionAttrsForTarget(data, target) }, { type: 'text', text: match[2] }]).run()
        return undefined
      },
    })]
  },
})

