import { EditorContent, useEditor, type Editor } from '@tiptap/react'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { useEffect, useRef } from 'react'
import { useI18n } from '@/i18n/i18n'
import { handleEditorSubmit } from './editor/editor-keyboard'

interface IssueTitleEditorProps {
  value: string
  onChange: (value: string) => void
  onBlur?: () => void
  onEnter?: () => void
  onSubmit?: () => void
  autoFocus?: boolean
  className?: string
  editorRef?: (editor: Editor | null) => void
}

export function IssueTitleEditor({ value, onChange, onBlur, onEnter, onSubmit, autoFocus = false, className, editorRef }: IssueTitleEditorProps) {
  const { t } = useI18n()
  const titleLabel = t('Issue title')
  const enterRef = useRef(onEnter)
  const submitRef = useRef(onSubmit)
  enterRef.current = onEnter
  submitRef.current = onSubmit
  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({
        blockquote: false,
        bulletList: false,
        code: false,
        codeBlock: false,
        heading: false,
        horizontalRule: false,
        orderedList: false,
      }),
      Placeholder.configure({ placeholder: titleLabel }),
    ],
    content: titleDocument(value),
    editorProps: {
      attributes: {
        class: 'flow-prosemirror title-editor',
        'aria-label': titleLabel,
      },
      handleKeyDown: (view, event) => {
        if (handleEditorSubmit(event, submitRef.current)) return true
        if (event.key === 'Enter') {
          event.preventDefault()
          enterRef.current?.()
          return true
        }
        if (event.key === 'Escape') {
          view.dom.blur()
          return true
        }
        return false
      },
    },
    onUpdate: ({ editor: current }) => onChange(current.getText()),
    onBlur,
  })

  useEffect(() => {
    if (!editor || editor.getText() === value || editor.isFocused) return
    editor.commands.setContent(titleDocument(value), { emitUpdate: false })
  }, [editor, value])

  useEffect(() => {
    if (!editor || !autoFocus) return
    const frame = requestAnimationFrame(() => editor.commands.focus('end'))
    return () => cancelAnimationFrame(frame)
  }, [autoFocus, editor])
  useEffect(() => {
    if (!editor) return
    editor.view.dom.setAttribute('aria-label', titleLabel)
    if (editor.isEmpty) editor.view.dom.setAttribute('data-placeholder', titleLabel)
  }, [editor, titleLabel])
  useEffect(() => {
    editorRef?.(editor)
    return () => editorRef?.(null)
  }, [editor, editorRef])

  return <div className={['issue-title-field', className].filter(Boolean).join(' ')}><EditorContent editor={editor}/></div>
}

function titleDocument(value: string) {
  return {
    type: 'doc',
    content: [{
      type: 'paragraph',
      content: value ? [{ type: 'text', text: value }] : undefined,
    }],
  }
}
