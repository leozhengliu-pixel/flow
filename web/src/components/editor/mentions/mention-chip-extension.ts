import { ReactNodeViewRenderer } from '@tiptap/react'
import { MentionExtension } from '@/components/issue/editor/mention-extension'
import { MentionChipView } from './mention-chip-node'

/** `mention` with its chip view, for the React editors (the headless schema editor uses MentionExtension). */
export const MentionChipNode = MentionExtension.extend({
  addNodeView() {
    return ReactNodeViewRenderer(MentionChipView, { as: 'span' })
  },
})
