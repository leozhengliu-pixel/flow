import { Extension, type Editor, type JSONContent } from '@tiptap/core'
import { Fragment, Slice } from '@tiptap/pm/model'
import { Plugin, PluginKey, type Selection } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'
import { normalizeGithubHtml, isGithubHtml } from './github-html'
import { isPlainTextWrapperHtml, isSingleUrl, looksLikeMarkdown, normalizeNewlines } from './markdown-detect'
import { plainTextToBlocks, tryMarkdownToDocJSON } from './markdown-to-doc'

export const markdownPasteKey = new PluginKey('flowMarkdownPaste')

/** Pastes above this size go through the default paste: converting megabytes of text would freeze the editor. */
const MAX_MARKDOWN_PASTE = 500_000

/** VS Code reports the language of the file the text came from; these are the ones whose text is prose / Markdown. */
const PROSE_MODES = new Set(['markdown', 'plaintext', 'mdx', 'text', ''])

function insideCode(selection: Selection) {
  const { $from, $to } = selection
  for (const $pos of [$from, $to]) {
    for (let depth = $pos.depth; depth >= 0; depth--) {
      const node = $pos.node(depth)
      if (node.type.spec.code || node.type.name === 'codeBlock') return true
    }
  }
  return false
}

/** Builds a closed slice from block JSON, lets the other `transformPasted` props (mention conversion) see it, and inserts it. */
function insertBlocks(view: EditorView, blocks: JSONContent[], pos?: number) {
  const { schema } = view.state
  let slice = new Slice(Fragment.fromJSON(schema, blocks), 0, 0)
  view.someProp('transformPasted', transform => {
    slice = transform(slice, view, false)
    return false
  })
  const tr = view.state.tr
  if (pos == null) tr.replaceSelection(slice)
  else tr.replaceRange(pos, pos, slice)
  view.dispatch(tr.scrollIntoView().setMeta('paste', true).setMeta('uiEvent', 'paste'))
}

function vscodeMode(data: DataTransfer) {
  try {
    const raw = data.getData('vscode-editor-data')
    return raw ? String((JSON.parse(raw) as { mode?: string }).mode ?? '') : undefined
  } catch {
    return undefined
  }
}

/**
 * Pasting Markdown source (README text, chat answers, notes) inserts real blocks instead of one wall of text; pasting
 * the html GitHub copies from rendered Markdown gets normalised first so task lists, code languages, alerts and tables survive.
 *
 * Priority 150 puts this above the stock handlers that would otherwise win (the code-block VS Code handler and the
 * plain-text ordered-list handler, both 100), so Markdown copied from an editor is converted rather than becoming one code block.
 * That is safe for the handlers that must keep their cases, because this one returns false (lets them run) for: clipboards
 * with files (image / file upload in `DescriptionImage`), a lone URL (`EmbedPasteNode` player, link-over-selection, mention
 * conversion), any single line of plain text, Shift-paste (paste as plain text), code blocks, and non-prose VS Code code.
 */
export const MarkdownPaste = Extension.create({
  name: 'markdownPaste',
  priority: 150,
  addProseMirrorPlugins() {
    const editor = this.editor
    return [new Plugin({
      key: markdownPasteKey,
      props: {
        transformPastedHTML: html => {
          if (!isGithubHtml(html)) return html
          try { return normalizeGithubHtml(html) } catch { return html }
        },
        handlePaste: (view, event) => {
          try {
            return handleMarkdownPaste(editor, view, event)
          } catch {
            return false
          }
        },
      },
    })]
  },
})

function handleMarkdownPaste(editor: Editor, view: EditorView, event: ClipboardEvent) {
  const data = event.clipboardData
  if (!data || !view.editable || !editor.markdown) return false
  if (data.files?.length) return false
  if ((view as unknown as { input?: { shiftKey?: boolean } }).input?.shiftKey) return false
  if (insideCode(view.state.selection)) return false
  const raw = data.getData('text/plain')
  if (!raw.trim() || raw.length > MAX_MARKDOWN_PASTE) return false
  const mode = vscodeMode(data)
  if (mode !== undefined && !PROSE_MODES.has(mode.toLowerCase())) return false
  const html = data.getData('text/html')
  if (html.trim() && !isPlainTextWrapperHtml(html)) return false
  const text = normalizeNewlines(raw)
  if (isSingleUrl(text) || !looksLikeMarkdown(text)) return false
  const doc = tryMarkdownToDocJSON(editor, text)
  if (!doc?.content?.length) return false
  insertBlocks(view, doc.content)
  event.preventDefault()
  return true
}

const TEXT_FILE = /\.(md|markdown|mdown|mkd|txt|text)$/i
const MARKDOWN_FILE = /\.(md|markdown|mdown|mkd)$/i

export function isTextImportFile(file: File) {
  return TEXT_FILE.test(file.name) || file.type === 'text/markdown' || file.type === 'text/x-markdown' || file.type === 'text/plain'
}

function readText(file: File): Promise<string> {
  if (typeof file.text === 'function') return file.text()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsText(file)
  })
}

/**
 * Inserts a .md / .markdown / .txt file's content at the selection (or at `pos`): Markdown files are converted into
 * blocks, text files only when they look like Markdown, otherwise they become plain paragraphs. Resolves false when the
 * file is not a text file, is empty, or the editor is not editable.
 */
export async function importTextFile(editor: Editor, file: File, pos?: number): Promise<boolean> {
  if (!isTextImportFile(file) || !editor.isEditable) return false
  try {
    const text = normalizeNewlines(await readText(file)).replace(/^﻿/, '')
    if (!text.trim()) return false
    const asMarkdown = MARKDOWN_FILE.test(file.name) || file.type === 'text/markdown' || looksLikeMarkdown(text)
    const blocks = (asMarkdown ? tryMarkdownToDocJSON(editor, text)?.content : undefined) ?? plainTextToBlocks(text)
    if (!blocks.length) return false
    insertBlocks(editor.view, blocks, pos)
    return true
  } catch {
    return false
  }
}
