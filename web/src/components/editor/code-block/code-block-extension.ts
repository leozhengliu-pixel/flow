import CodeBlock from '@tiptap/extension-code-block'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { codeBlockHighlightPlugin } from './code-block-highlight'
import { CodeBlockView } from './code-block-view'
import type { DiagramComment } from './diagram-block-view'

declare module '@tiptap/core' {
  interface Storage {
    codeBlock: { onComment?: DiagramComment }
  }
}

export { CODE_LANGUAGES, detectLanguage, languageLabel, resolveLanguage, type CodeLanguage } from './languages'

/**
 * The schema of a code block (a fenced ```lang block with a `language` attribute, markdown included): saved with the
 * document, so every editor that can show one needs it, including the headless schema editor.
 */
export const FlowCodeBlockSchema = CodeBlock

/**
 * The code block with its header (language picker, settings, copy), line numbers, collapse and syntax highlighting, for
 * the React editors. Replaces StarterKit's `codeBlock` (set `codeBlock: false` there).
 */
export const FlowCodeBlock = CodeBlock.extend({
  /** `onComment`: a Mermaid diagram's "Add comment", wired to the editor's inline comments by its host. */
  addStorage() {
    return { onComment: undefined as DiagramComment | undefined }
  },
  addNodeView() {
    return ReactNodeViewRenderer(CodeBlockView)
  },
  addProseMirrorPlugins() {
    return [...(this.parent?.() ?? []), codeBlockHighlightPlugin(this.name)]
  },
})
