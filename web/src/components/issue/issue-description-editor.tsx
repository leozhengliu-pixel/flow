import { Code2, Heading2, Heading3, List, ListOrdered, Minus, Pilcrow, Quote } from 'lucide-react'
import Placeholder from '@tiptap/extension-placeholder'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { EditorContent, useEditor, type Editor } from '@tiptap/react'
import { BubbleMenu } from '@tiptap/react/menus'
import { useEffect, useMemo, useRef, useState } from 'react'
import { parseDescriptionContent, sameDocument, serializeDescription, type DescriptionSnapshot } from './editor/editor-content'
import { getSlashCommandState, SlashCommandExtension, type SlashCommandState } from './editor/slash-command-extension'
import { SlashCommandMenu, type EditorCommand } from './editor/slash-command-menu'
import { filterEditorCommands } from './editor/editor-commands'
import { SelectionToolbar } from './editor/selection-toolbar'
import { useI18n } from '@/i18n/i18n'

interface DescriptionEditorProps {
  value: string
  state?: string
  onChange?: (snapshot: DescriptionSnapshot) => void
  onBlur?: () => void
  onSubmit?: () => void
  editorRef?: (editor: Editor | null) => void
  className?: string
}

const closedSlash: SlashCommandState = { active: false, query: '', range: null }

export function IssueDescriptionEditor({ value, state, onChange, onBlur, onSubmit, editorRef, className }: DescriptionEditorProps) {
  const { t } = useI18n()
  const descriptionLabel = t('Issue description')
  const initial = useMemo(() => parseDescriptionContent(value, state), []) // eslint-disable-line react-hooks/exhaustive-deps
  const rootRef = useRef<HTMLDivElement>(null)
  const commandsRef = useRef<EditorCommand[]>([])
  const slashRef = useRef<SlashCommandState>(closedSlash)
  const selectedRef = useRef(0)
  const dismissedRef = useRef<number | null>(null)
  const submitRef = useRef(onSubmit)
  submitRef.current = onSubmit
  const [slash, setSlash] = useState<SlashCommandState>(closedSlash)
  const [selectedIndex, setSelectedIndex] = useState(0)
  const [menuPosition, setMenuPosition] = useState({ left: 14, top: 44 })

  const editor = useEditor({
    immediatelyRender: false,
    extensions: [
      StarterKit.configure({ heading: { levels: [2, 3] }, link: { openOnClick: false, autolink: true, linkOnPaste: true } }),
      Placeholder.configure({ placeholder: 'Add description...' }),
      Markdown,
      SlashCommandExtension,
    ],
    content: initial.content,
    contentType: initial.contentType,
    editorProps: {
      attributes: {
        class: 'flow-prosemirror description-editor',
        'aria-label': descriptionLabel,
        'aria-multiline': 'true',
        'aria-readonly': 'false',
        spellcheck: 'true',
        translate: 'no',
      },
      handleKeyDown: (view, event) => {
        if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
          event.preventDefault()
          submitRef.current?.()
          return true
        }
        const current = getSlashCommandState(view.state)
        if (!current.active || current.range?.from === dismissedRef.current) return false
        const filtered = filterEditorCommands(commandsRef.current, current.query)
        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
          event.preventDefault()
          if (!filtered.length) return true
          const movement = event.key === 'ArrowDown' ? 1 : -1
          const next = (selectedRef.current + movement + filtered.length) % filtered.length
          selectedRef.current = next
          setSelectedIndex(next)
          return true
        }
        if ((event.key === 'Enter' || event.key === 'Tab') && filtered.length) {
          event.preventDefault()
          filtered[Math.min(selectedRef.current, filtered.length - 1)].run()
          return true
        }
        if (event.key === 'Escape') {
          event.preventDefault()
          dismissedRef.current = current.range?.from ?? null
          slashRef.current = closedSlash
          setSlash(closedSlash)
          return true
        }
        return false
      },
    },
    onUpdate: ({ editor: current }) => onChange?.(serializeDescription(current)),
    onTransaction: ({ editor: current }) => syncSlashState(current),
    onSelectionUpdate: ({ editor: current }) => syncSlashState(current),
    onBlur: () => { setSlash(closedSlash); onBlur?.() },
  })

  const commands = useMemo<EditorCommand[]>(() => {
    if (!editor) return []
    const execute = (operation: () => void) => () => {
      const range = getSlashCommandState(editor.state).range ?? slashRef.current.range
      if (!range) return
      editor.chain().focus().deleteRange(range).run()
      operation()
      dismissedRef.current = null
      setSlash(closedSlash)
    }
    return [
      { id: 'text', group: 'Basic blocks', label: 'Text', description: 'Start writing with plain text', keywords: 'paragraph regular', shortcut: '⌘⌥0', icon: Pilcrow, run: execute(() => editor.chain().focus().setParagraph().run()) },
      { id: 'heading-2', group: 'Basic blocks', label: 'Heading 2', description: 'Medium section heading', keywords: 'h2 title', shortcut: '##', icon: Heading2, run: execute(() => editor.chain().focus().toggleHeading({ level: 2 }).run()) },
      { id: 'heading-3', group: 'Basic blocks', label: 'Heading 3', description: 'Small section heading', keywords: 'h3 subtitle', shortcut: '###', icon: Heading3, run: execute(() => editor.chain().focus().toggleHeading({ level: 3 }).run()) },
      { id: 'bullet-list', group: 'Basic blocks', label: 'Bulleted list', description: 'Create a simple bulleted list', keywords: 'unordered bullets', shortcut: '-', icon: List, run: execute(() => editor.chain().focus().toggleBulletList().run()) },
      { id: 'number-list', group: 'Basic blocks', label: 'Numbered list', description: 'Create a list with numbering', keywords: 'ordered numbers', shortcut: '1.', icon: ListOrdered, run: execute(() => editor.chain().focus().toggleOrderedList().run()) },
      { id: 'quote', group: 'Basic blocks', label: 'Quote', description: 'Capture a quote or callout', keywords: 'blockquote citation', shortcut: '>', icon: Quote, run: execute(() => editor.chain().focus().toggleBlockquote().run()) },
      { id: 'code-block', group: 'Basic blocks', label: 'Code block', description: 'Add a formatted code block', keywords: 'source snippet', shortcut: '```', icon: Code2, run: execute(() => editor.chain().focus().toggleCodeBlock().run()) },
      { id: 'divider', group: 'Basic blocks', label: 'Divider', description: 'Separate sections visually', keywords: 'horizontal rule separator', shortcut: '---', icon: Minus, run: execute(() => editor.chain().focus().setHorizontalRule().run()) },
    ]
  }, [editor])
  commandsRef.current = commands
  const filteredCommands = filterEditorCommands(commands, slash.query)

  useEffect(() => {
    if (!editor) return
    // A server echo must not replace the active document and collapse its selection.
    if (sameDocument(editor, state) || (!state && editor.getMarkdown() === value)) return
    if (editor.isFocused) return
    const next = parseDescriptionContent(value, state)
    editor.commands.setContent(next.content, { emitUpdate: false, contentType: next.contentType })
  }, [editor, state, value])
  useEffect(() => {
    editorRef?.(editor)
    return () => editorRef?.(null)
  }, [editor, editorRef])
  useEffect(() => {
    if (!editor) return
    editor.view.dom.setAttribute('aria-label', descriptionLabel)
  }, [descriptionLabel, editor])
  useEffect(() => {
    selectedRef.current = 0
    setSelectedIndex(0)
  }, [slash.query])

  function syncSlashState(current: NonNullable<typeof editor>) {
    const next = getSlashCommandState(current.state)
    if (!next.active || next.range?.from === dismissedRef.current) {
      slashRef.current = closedSlash
      setSlash(closedSlash)
      return
    }
    if (dismissedRef.current !== null && next.range?.from !== dismissedRef.current) dismissedRef.current = null
    slashRef.current = next
    setSlash(next)
    requestAnimationFrame(() => {
      const root = rootRef.current
      const live = getSlashCommandState(current.state)
      if (!root || current.isDestroyed || !next.range || !live.active || live.range?.to !== next.range.to || next.range.to > current.state.doc.content.size) return
      const caret = current.view.coordsAtPos(next.range.to)
      const bounds = root.getBoundingClientRect()
      const width = 292
      setMenuPosition({
        left: Math.max(0, Math.min(caret.left - bounds.left, bounds.width - width)),
        top: caret.bottom - bounds.top + 6,
      })
    })
  }

  if (!editor) return <div className="issue-description-skeleton" aria-label="Loading issue description"/>
  return <div className={['issue-description-root', className].filter(Boolean).join(' ')} ref={rootRef}>
    <EditorContent editor={editor}/>
    <BubbleMenu editor={editor} shouldShow={({ from, to, editor: current }) => from !== to && !current.isActive('codeBlock')}><SelectionToolbar editor={editor}/></BubbleMenu>
    {slash.active && <SlashCommandMenu
      commands={filteredCommands}
      selectedIndex={selectedIndex}
      position={menuPosition}
      query={slash.query}
      onSelect={command => command.run()}
    />}
  </div>
}
