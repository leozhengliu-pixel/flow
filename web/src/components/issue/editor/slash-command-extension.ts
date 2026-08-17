import { Extension } from '@tiptap/core'
import { Plugin, PluginKey, type EditorState } from '@tiptap/pm/state'

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
    })]
  },
})

export function getSlashCommandState(state: EditorState): SlashCommandState {
  return slashCommandKey.getState(state) ?? closedState
}

export function detectSlashCommand(state: EditorState): SlashCommandState {
  const { $from, empty } = state.selection
  if (!empty || !$from.parent.isTextblock || $from.parent.type.name !== 'paragraph') return closedState
  // Flow only opens the root block command menu; a slash inside list items remains text.
  if ($from.depth !== 1) return closedState
  const text = $from.parent.textBetween(0, $from.parentOffset, undefined, '\ufffc')
  const match = text.match(/^\s*\/([^\s/]*)$/)
  if (!match) return closedState
  return {
    active: true,
    query: match[1],
    range: { from: $from.pos - match[1].length - 1, to: $from.pos },
  }
}
