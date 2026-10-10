import { Extension } from '@tiptap/core'
import { Plugin, PluginKey, type EditorState } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'

export interface SlashCommandState {
  active: boolean
  query: string
  range: { from: number; to: number } | null
}

const closedState: SlashCommandState = { active: false, query: '', range: null }
export const slashCommandKey = new PluginKey<SlashCommandState>('flowSlashCommand')

export const SlashCommandExtension = Extension.create({
  name: 'flowSlashCommand',
  addProseMirrorPlugins() {
    return [new Plugin<SlashCommandState>({
      key: slashCommandKey,
      state: {
        init: (_, state) => detectSlashCommand(state),
        apply: (_, __, ___, state) => detectSlashCommand(state),
      },
      props: {
        // The typed "/command" shows as a chip, as in Linear.
        decorations: state => {
          const current = slashCommandKey.getState(state)
          return current?.active && current.range ? DecorationSet.create(state.doc, [Decoration.inline(current.range.from, current.range.to, { class: 'description-slash-trigger' })]) : null
        },
      },
    })]
  },
})

export function getSlashCommandState(state: EditorState): SlashCommandState {
  return slashCommandKey.getState(state) ?? closedState
}

/**
 * "/" opens the command menu at the start of a line or after a space, in any paragraph (list items, table cells,
 * callouts...), like Linear. A slash glued to a word ("and/or", "https://") stays text.
 */
export function detectSlashCommand(state: EditorState): SlashCommandState {
  const { $from, empty } = state.selection
  if (!empty || !$from.parent.isTextblock || $from.parent.type.name !== 'paragraph') return closedState
  const text = $from.parent.textBetween(0, $from.parentOffset, undefined, '\ufffc')
  const match = text.match(/(?:^|\s)\/([^\s/]*)$/)
  if (!match) return closedState
  return {
    active: true,
    query: match[1],
    range: { from: $from.pos - match[1].length - 1, to: $from.pos },
  }
}
