import CodeBlock from '@tiptap/extension-code-block'
import { ReactNodeViewRenderer } from '@tiptap/react'
import { codeBlockHighlightPlugin } from './code-block-highlight'
import { CodeBlockView } from './code-block-view'

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
  addNodeView() {
    return ReactNodeViewRenderer(CodeBlockView)
  },
  addProseMirrorPlugins() {
    return [...(this.parent?.() ?? []), codeBlockHighlightPlugin(this.name)]
  },
})
