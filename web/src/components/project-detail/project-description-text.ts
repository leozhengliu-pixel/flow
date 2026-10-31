import { Extension } from '@tiptap/core'
import { Plugin } from '@tiptap/pm/state'
import { Decoration, DecorationSet } from '@tiptap/pm/view'

// Inline text has its own bounds; paragraphs and list items retain their full line boxes.
// Decorations leave the stored description and clipboard markup unchanged.
export const ProjectDescriptionText = Extension.create({
  name: 'projectDescriptionText',
  addProseMirrorPlugins() {
    return [new Plugin({
      props: {
        decorations(state) {
          const decorations: Decoration[] = []
          state.doc.descendants((node, pos) => {
            if (['paragraph', 'heading'].includes(node.type.name) && node.content.size) {
              decorations.push(Decoration.inline(pos + 1, pos + 1 + node.content.size, { class: 'project-description-text' }))
            }
          })
          return DecorationSet.create(state.doc, decorations)
        },
      },
    })]
  },
})

export const projectDescriptionExtensions = [ProjectDescriptionText]
