import { Extension } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'

const hintKey = new PluginKey('flowEmptyLineHint')

/** Splits a translated "Type / for commands…" hint around its "/" so the key can be drawn as a chip. */
export function splitSlashHint(text: string): [string, string] {
  const index = text.indexOf('/')
  return index < 0 ? [text, ''] : [text.slice(0, index).trimEnd(), text.slice(index + 1).trimStart()]
}

/**
 * Linear shows "Type [/] for commands…" in the focused empty paragraph. The very first line of an empty document keeps the
 * editor's own placeholder ("Start writing…" / "Add description..."), so this hint stays out of its way.
 */
export const EmptyLineHint = Extension.create<{ getText: () => string }>({
  name: 'flowEmptyLineHint',
  addOptions() {
    return { getText: () => 'Type / for commands…' }
  },
  addProseMirrorPlugins() {
    const { editor, options } = this
    return [new Plugin({
      key: hintKey,
      props: {
        decorations: state => {
          const { selection } = state
          const { $from } = selection
          if (!editor.isEditable || !editor.isFocused || !selection.empty || editor.isEmpty) return null
          if ($from.parent.type.name !== 'paragraph' || $from.parent.content.size > 0) return null
          const [before, after] = splitSlashHint(options.getText())
          const widget = Decoration.widget($from.pos, () => {
            const hint = document.createElement('span')
            hint.className = 'description-empty-hint'
            hint.contentEditable = 'false'
            hint.setAttribute('data-i18n-ignore', '')
            if (before) hint.append(document.createTextNode(before), ' ')
            const key = document.createElement('kbd')
            key.textContent = '/'
            hint.append(key)
            if (after) hint.append(' ', document.createTextNode(after))
            return hint
          }, { side: -1, key: `hint:${before}:${after}`, ignoreSelection: true })
          return DecorationSet.create(state.doc, [widget])
        },
      },
    })]
  },
})
