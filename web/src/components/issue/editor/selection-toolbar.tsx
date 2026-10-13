import type { Editor } from '@tiptap/react'
import { useEditorState } from '@tiptap/react'
import { useEffect, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { useI18n } from '@/i18n/i18n'
import { FlowTooltip, TooltipProvider } from '@/components/ui/tooltip'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import type { DescriptionSelectionActions } from './structured-blocks'
import { LinkEditor } from './link-editor'
import { ToolbarGlyph, type ToolbarGlyphName } from './selection-toolbar-glyphs'
import { useDismissibleLayer } from '@/hooks/use-dismissible-layer'
import './selection-toolbar.css'

type BlockId = 'paragraph' | 'h1' | 'h2' | 'h3' | 'h4'
type ListId = 'bulletList' | 'orderedList' | 'taskList'

/** The text-style menu: the toolbar's first button shows the selected block's glyph and names it. */
const TEXT_STYLES: readonly { id: BlockId; label: string; shortcut: string; glyph: ToolbarGlyphName; level?: 1 | 2 | 3 | 4 }[] = [
  { id: 'paragraph', label: 'Regular text', shortcut: '⌘⌥0', glyph: 'textStyle' },
  { id: 'h1', label: 'Heading 1', shortcut: '⌘⌥1', glyph: 'heading1', level: 1 },
  { id: 'h2', label: 'Heading 2', shortcut: '⌘⌥2', glyph: 'heading2', level: 2 },
  { id: 'h3', label: 'Heading 3', shortcut: '⌘⌥3', glyph: 'heading3', level: 3 },
  { id: 'h4', label: 'Heading 4', shortcut: '⌘⌥4', glyph: 'heading4', level: 4 },
]

const LIST_STYLES: readonly { id: ListId; label: string; glyph: ToolbarGlyphName }[] = [
  { id: 'bulletList', label: 'List', glyph: 'bulletList' },
  { id: 'orderedList', label: 'Numbered list', glyph: 'numberedList' },
  { id: 'taskList', label: 'Checklist', glyph: 'checklist' },
]

/** The tooltip shortcut of each toolbar button (the editor binds them; ⌘K opens the link field from the toolbar). */
const TOOLBAR_SHORTCUTS = {
  Bold: '⌘B', Italic: '⌘I', Strikethrough: '⌘⇧X', Underline: '⌘U', Link: '⌘K', Quote: '⌥⇧.', Collapse: '⌘⇧6',
  'Inline code': '⌘E', 'Code block': '⌘⇧\\', 'Create issue from selection': '⌘⇧O', 'Ask agent': '⌘J', Comment: '⌘⌥M',
} as const

/**
 * The floating toolbar over a text selection, shared by every rich-text editor (issue, project and initiative
 * descriptions, documents, comments, updates). Laid out like the reference: a 42px text-style button, 26px buttons
 * 6px apart, one hairline before the selection actions (create issue, ask agent, comment), tooltips above.
 */
export function SelectionToolbar({ editor, actions }: { editor: Editor; actions?: DescriptionSelectionActions }) {
  const { t } = useI18n()
  const [open, setOpen] = useState<'block' | 'list' | 'link' | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      block: TEXT_STYLES.find(style => style.level ? current.isActive('heading', { level: style.level }) : false)?.id ?? 'paragraph',
      bold: current.isActive('bold'), italic: current.isActive('italic'), strike: current.isActive('strike'), underline: current.isActive('underline'),
      link: current.isActive('link'), blockquote: current.isActive('blockquote'), details: current.isActive('details'), code: current.isActive('code'), codeBlock: current.isActive('codeBlock'),
      bulletList: current.isActive('bulletList'), orderedList: current.isActive('orderedList'), taskList: current.isActive('taskList'),
      editable: current.isEditable,
    }),
  })
  const selection = () => { const { from, to } = editor.state.selection; return { from, to, text: editor.state.doc.textBetween(from, to, '\n') } }
  const toggle = (menu: 'block' | 'list' | 'link') => setOpen(current => current === menu ? null : menu)
  const closeLink = () => { setOpen(null); editor.commands.focus() }
  useDismissibleLayer({
    open: open !== null,
    refs: [rootRef],
    onDismiss: reason => {
      setOpen(null)
      if (reason === 'escape') editor.commands.focus()
    },
  })
  // ⌘K with text selected opens the link field (and keeps the app-wide ⌘K menu closed), as the Link tooltip says.
  useEffect(() => {
    const dom = editor.view.dom
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.shiftKey || event.altKey || event.key.toLowerCase() !== 'k') return
      if (!editor.isEditable || editor.state.selection.empty) return
      event.preventDefault()
      setOpen('link')
    }
    dom.addEventListener('keydown', onKeyDown)
    return () => dom.removeEventListener('keydown', onKeyDown)
  }, [editor])
  const heading = state.block !== 'paragraph'
  const style = TEXT_STYLES.find(item => item.id === state.block) ?? TEXT_STYLES[0]
  const hasActions = Boolean(actions?.onCreateIssue || actions?.onAskAgent || actions?.onComment)
  return <TooltipProvider><div className="description-bubble-menu selection-toolbar" role="toolbar" aria-label={t('Text formatting')} ref={rootRef}>
    {state.editable && <>
      <span className="selection-toolbar-item">
        <ToolbarButton label={t(style.label)} shortcut={style.shortcut} wide expanded={open === 'block'} onPress={() => toggle('block')}>
          <ToolbarGlyph name={style.glyph}/><ToolbarGlyph name="chevron" size={12}/>
        </ToolbarButton>
        {open === 'block' && <ToolbarMenu anchorRef={rootRef} label={t('Text style')}>
          {TEXT_STYLES.map(item => <button key={item.id} type="button" role="menuitemradio" aria-checked={state.block === item.id} data-block={item.id} onMouseDown={event => event.preventDefault()} onClick={() => {
            setOpen(null)
            const chain = editor.chain().focus()
            if (item.level) chain.setHeading({ level: item.level }).run()
            else chain.setParagraph().run()
          }}>
            <span className="selection-menu-label">{t(item.label)}</span>
            <span className="selection-menu-check">{state.block === item.id && <FlowGlyph name="check" size={12}/>}</span>
            <span className="selection-menu-keys" aria-hidden="true">{[...item.shortcut].map((key, index) => <kbd key={index}>{key}</kbd>)}</span>
          </button>)}
        </ToolbarMenu>}
      </span>
      <ToolbarItem label={t('Bold')} shortcut={TOOLBAR_SHORTCUTS.Bold} active={state.bold} disabled={heading} onPress={() => editor.chain().focus().toggleBold().run()}><ToolbarGlyph name="bold"/></ToolbarItem>
      <ToolbarItem label={t('Italic')} shortcut={TOOLBAR_SHORTCUTS.Italic} active={state.italic} onPress={() => editor.chain().focus().toggleItalic().run()}><ToolbarGlyph name="italic"/></ToolbarItem>
      <ToolbarItem label={t('Strikethrough')} shortcut={TOOLBAR_SHORTCUTS.Strikethrough} active={state.strike} onPress={() => editor.chain().focus().toggleStrike().run()}><ToolbarGlyph name="strikethrough"/></ToolbarItem>
      <ToolbarItem label={t('Underline')} shortcut={TOOLBAR_SHORTCUTS.Underline} active={state.underline} onPress={() => editor.chain().focus().toggleUnderline().run()}><ToolbarGlyph name="underline"/></ToolbarItem>
      <span className="selection-toolbar-item description-link-control">
        <ToolbarButton label={t('Link')} shortcut={TOOLBAR_SHORTCUTS.Link} active={state.link || open === 'link'} tooltipDisabled={open === 'link'} onPress={() => toggle('link')}><ToolbarGlyph name="link"/></ToolbarButton>
        {open === 'link' && <LinkEditor initialValue={(editor.getAttributes('link').href as string | undefined) ?? ''} onApply={href => { editor.chain().focus().extendMarkRange('link').setLink({ href }).run(); closeLink() }} onRemove={() => { editor.chain().focus().extendMarkRange('link').unsetLink().run(); closeLink() }} onClose={closeLink}/>}
      </span>
      <ToolbarItem label={t('Quote')} shortcut={TOOLBAR_SHORTCUTS.Quote} active={state.blockquote} onPress={() => editor.chain().focus().toggleBlockquote().run()}><ToolbarGlyph name="quote"/></ToolbarItem>
      <ToolbarItem label={t('Collapse')} shortcut={TOOLBAR_SHORTCUTS.Collapse} active={state.details} onPress={() => state.details ? editor.chain().focus().unsetDetails().run() : editor.chain().focus().setDetails().run()}><ToolbarGlyph name="collapse"/></ToolbarItem>
      <ToolbarItem label={t('Inline code')} shortcut={TOOLBAR_SHORTCUTS['Inline code']} active={state.code} onPress={() => editor.chain().focus().toggleCode().run()}><ToolbarGlyph name="inlineCode"/></ToolbarItem>
      <ToolbarItem label={t('Code block')} shortcut={TOOLBAR_SHORTCUTS['Code block']} active={state.codeBlock} onPress={() => editor.chain().focus().toggleCodeBlock().run()}><ToolbarGlyph name="codeBlock"/></ToolbarItem>
      <span className="selection-toolbar-item">
        <ToolbarButton label={t('List')} wide disabled={heading} expanded={open === 'list'} onPress={() => toggle('list')}>
          <ToolbarGlyph name="bulletList"/><ToolbarGlyph name="chevron" size={12}/>
        </ToolbarButton>
        {open === 'list' && <ToolbarMenu anchorRef={rootRef} label={t('List')}>
          {LIST_STYLES.map(item => <button key={item.id} type="button" role="menuitemradio" aria-checked={state[item.id]} data-list={item.id} onMouseDown={event => event.preventDefault()} onClick={() => {
            setOpen(null)
            const chain = editor.chain().focus()
            if (item.id === 'bulletList') chain.toggleBulletList().run()
            else if (item.id === 'orderedList') chain.toggleOrderedList().run()
            else chain.toggleTaskList().run()
          }}>
            <ToolbarGlyph name={item.glyph}/>
            <span className="selection-menu-label">{t(item.label)}</span>
            <span className="selection-menu-check">{state[item.id] && <FlowGlyph name="check" size={12}/>}</span>
          </button>)}
        </ToolbarMenu>}
      </span>
    </>}
    {state.editable && hasActions && <span className="selection-toolbar-separator" role="separator" aria-orientation="vertical"/>}
    {actions?.onCreateIssue && <ToolbarItem label={t('Create issue from selection')} shortcut={TOOLBAR_SHORTCUTS['Create issue from selection']} onPress={() => actions.onCreateIssue?.(selection())}><ToolbarGlyph name="createIssue"/></ToolbarItem>}
    {actions?.onAskAgent && <ToolbarItem label={t('Ask agent')} shortcut={TOOLBAR_SHORTCUTS['Ask agent']} onPress={() => actions.onAskAgent?.(selection())}><AgentCursorGlyph size={16}/></ToolbarItem>}
    {actions?.onComment && <ToolbarItem label={t('Comment')} shortcut={TOOLBAR_SHORTCUTS.Comment} onPress={() => actions.onComment?.(selection())}><ToolbarGlyph name="comment"/></ToolbarItem>}
  </div></TooltipProvider>
}

type ToolbarButtonProps = { label: string; shortcut?: string; active?: boolean; disabled?: boolean; expanded?: boolean; wide?: boolean; tooltipDisabled?: boolean; onPress: () => void; children: ReactNode }

/** One 26px (42px with a caret) toolbar button; its tooltip sits centred above it. Toggles report `aria-pressed`, menu buttons `aria-expanded`. */
function ToolbarButton({ label, shortcut, active, disabled = false, expanded, wide = false, tooltipDisabled, onPress, children }: ToolbarButtonProps) {
  return <FlowTooltip label={label} shortcut={shortcut} side="top" disabled={tooltipDisabled ?? expanded}>
    <button type="button" className={wide ? 'selection-toolbar-button selection-toolbar-button--menu' : 'selection-toolbar-button'} aria-label={label}
      aria-pressed={expanded === undefined ? active : undefined} aria-expanded={expanded} aria-haspopup={expanded === undefined ? undefined : 'menu'}
      aria-disabled={disabled || undefined} onMouseDown={event => event.preventDefault()} onClick={() => { if (!disabled) onPress() }}>{children}</button>
  </FlowTooltip>
}

function ToolbarItem(props: ToolbarButtonProps) {
  return <span className="selection-toolbar-item"><ToolbarButton {...props}/></span>
}

/**
 * A text-style or list menu: 200px, aligned 6px left of its button, 8px below it (4px below the toolbar), flipping
 * above the toolbar when the viewport has no room below.
 */
function ToolbarMenu({ label, anchorRef, children }: { label: string; anchorRef: RefObject<HTMLElement | null>; children: ReactNode }) {
  const menuRef = useRef<HTMLDivElement>(null)
  const [side, setSide] = useState<'bottom' | 'top'>('bottom')
  useLayoutEffect(() => {
    const anchor = anchorRef.current?.getBoundingClientRect()
    const height = menuRef.current?.offsetHeight ?? 0
    if (!anchor) return
    const fitsBelow = anchor.bottom + 4 + height <= window.innerHeight - 8
    const fitsAbove = anchor.top - 4 - height >= 8
    setSide(!fitsBelow && fitsAbove ? 'top' : 'bottom')
  }, [anchorRef])
  return <div ref={menuRef} className="selection-menu" role="menu" aria-label={label} data-side={side}>{children}</div>
}
