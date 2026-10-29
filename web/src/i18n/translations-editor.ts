import { editorCalloutZhCN } from './translations-editor-callout'
import { editorCodeBlockZhCN } from './translations-editor-code-block'
import { editorCoreZhCN } from './translations-editor-core'
import { editorEmbedsZhCN } from './translations-editor-embeds'
import { editorOutlineZhCN } from './translations-editor-outline'
import { editorTableZhCN } from './translations-editor-table'

/** Rich-text editor chrome (slash menu, toolbar, code block, table, callout, embeds, outline). The DOM translator skips ProseMirror, so these are real t() keys. */
export const editorZhCN: Record<string, string> = {
  'LaTeX math block': 'LaTeX 数学公式块',
  'Formula actions': '公式操作',
  'Copy formula': '复制公式',
  ...editorCalloutZhCN,
  ...editorCodeBlockZhCN,
  ...editorCoreZhCN,
  ...editorEmbedsZhCN,
  ...editorOutlineZhCN,
  ...editorTableZhCN,
}
