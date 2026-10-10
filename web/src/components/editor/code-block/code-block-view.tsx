import * as Popover from '@radix-ui/react-popover'
import { Check, ChevronDown, ChevronUp, Copy, SlidersHorizontal } from 'lucide-react'
import { Fragment, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { toast } from 'sonner'
import { NodeViewContent, NodeViewWrapper, useEditorState, type ReactNodeViewProps } from '@tiptap/react'
import { TextSelection } from '@tiptap/pm/state'
import { Toggle } from '@/components/ui/toggle'
import { useI18n } from '@/i18n/i18n'
import { updateCodeBlockSettings, useCodeBlockSettings } from './code-block-settings'
import { CODE_LANGUAGES, detectLanguage, languageLabel, resolveLanguage } from './languages'
import './code-block.css'

/** A block with more lines than this is collapsed to its first {@link COLLAPSED_LINES} lines. */
export const COLLAPSE_THRESHOLD = 30
export const COLLAPSED_LINES = 15

const AUTO = 'auto'

function LanguagePicker({ detected, editable, language, onSelect }: { detected: string | null; editable: boolean; language: string | null; onSelect: (language: string | null) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const current = resolveLanguage(language) ?? (language && language !== AUTO ? language : AUTO)
  // An unset block shows what was detected, or Plaintext when nothing convincing was found.
  const shown = current === AUTO ? detected ?? 'plaintext' : current
  const label = shown === 'plaintext' ? t('Plaintext') : languageLabel(shown)
  const options = useMemo(() => {
    const all = [{ id: AUTO, label: t('Auto detect'), keywords: 'auto automatic detect' }, ...CODE_LANGUAGES.map(item => ({ ...item, label: item.id === 'plaintext' ? t('Plaintext') : item.label }))]
    const needle = query.trim().toLowerCase()
    return needle ? all.filter(item => `${item.label} ${item.id} ${item.keywords ?? ''}`.toLowerCase().includes(needle)) : all
  }, [query, t])
  const list = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (open) list.current?.querySelector('[data-active="true"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [active, open])
  const choose = (id: string) => { onSelect(id === AUTO ? null : id); setOpen(false) }
  const keyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      setActive(index => options.length ? (index + (event.key === 'ArrowDown' ? 1 : options.length - 1)) % options.length : 0)
    } else if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
      event.preventDefault()
      const option = options[Math.min(active, options.length - 1)]
      if (option) choose(option.id)
    }
  }
  if (!editable) return <span className="flow-code-block__language" data-readonly="">{label}</span>
  return (
    <Popover.Root open={open} onOpenChange={next => { setOpen(next); if (next) { setQuery(''); setActive(Math.max(0, [AUTO, ...CODE_LANGUAGES.map(item => item.id)].indexOf(current))) } }}>
      <Popover.Trigger asChild>
        <button aria-label={t('Code language')} className="flow-code-block__language" type="button">{label}<ChevronDown size={12}/></button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="end" className="flow-code-block__popover flow-code-block__languages" collisionPadding={8} data-flow-motion="floating" data-i18n-ignore sideOffset={4} onOpenAutoFocus={event => { event.preventDefault(); (event.currentTarget as HTMLElement).querySelector('input')?.focus() }}>
          <input aria-label={t('Search languages…')} autoComplete="off" onChange={event => { setQuery(event.target.value); setActive(0) }} onKeyDown={keyDown} placeholder={t('Search languages…')} spellCheck={false} value={query}/>
          <div className="flow-code-block__options" ref={list} role="menu">
            {options.map((option, index) => (
              <Fragment key={option.id}>
              <button aria-checked={option.id === current} className="flow-code-block__option" data-active={index === active} onClick={() => choose(option.id)} onPointerMove={() => setActive(index)} role="menuitemradio" type="button">
                <span>{option.label}</span>
                {option.id === current && <Check size={14}/>}
              </button>
              {option.id === AUTO && options.length > 1 && <div className="flow-code-block__divider" role="separator"/>}
              </Fragment>
            ))}
            {!options.length && <div className="flow-code-block__empty">{t('No languages found')}</div>}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function SettingsMenu({ lineNumbers, wrap }: { lineNumbers: boolean; wrap: boolean }) {
  const { t } = useI18n()
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button aria-label={t('Code block settings')} className="flow-code-block__icon" title={t('Code block settings')} type="button"><SlidersHorizontal size={14}/></button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="end" className="flow-code-block__popover flow-code-block__settings" collisionPadding={8} data-flow-motion="floating" data-i18n-ignore sideOffset={4}>
          <label><span>{t('Wrap lines')}</span><Toggle checked={wrap} label={t('Wrap lines')} onChange={next => updateCodeBlockSettings({ wrap: next })}/></label>
          <label><span>{t('Show line numbers')}</span><Toggle checked={lineNumbers} label={t('Show line numbers')} onChange={next => updateCodeBlockSettings({ lineNumbers: next })}/></label>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}

function CopyButton({ text }: { text: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
    } catch {
      toast.error(t('Could not copy'))
      return
    }
    setCopied(true)
    toast.success(t('Copied to clipboard'))
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setCopied(false), 1500)
  }
  const label = copied ? t('Copied') : t('Copy code')
  return <button aria-label={label} className="flow-code-block__icon" data-copied={copied || undefined} onClick={() => void copy()} title={label} type="button">{copied ? <Check size={14}/> : <Copy size={14}/>}</button>
}

/** A fenced code block with Linear's chrome: language picker, settings, copy, line numbers and a collapsed long-block view. */
export function CodeBlockView({ editor, getPos, node }: ReactNodeViewProps) {
  const { t } = useI18n()
  const { lineNumbers, wrap } = useCodeBlockSettings()
  const [showAll, setShowAll] = useState(false)
  const text = node.textContent
  const lineCount = useMemo(() => text.split('\n').length, [text])
  const collapsible = lineCount > COLLAPSE_THRESHOLD
  const hasSelection = useEditorState({
    editor,
    selector: ({ editor: current }) => {
      const pos = getPos()
      if (typeof pos !== 'number' || !current.isFocused) return false
      const { from, to } = current.state.selection
      return from >= pos && to <= pos + node.nodeSize
    },
  })
  const expanded = !collapsible || showAll || hasSelection
  const language = (node.attrs.language as string | null) ?? null
  const detected = useMemo(() => !language || language === AUTO ? detectLanguage(text) : null, [language, text])
  const editable = editor.isEditable
  const setLanguage = (next: string | null) => {
    editor.chain().command(({ tr }) => {
      const pos = getPos()
      if (typeof pos !== 'number') return false
      tr.setNodeMarkup(pos, undefined, { ...node.attrs, language: next })
      return true
    }).run()
  }
  const collapse = () => {
    setShowAll(false)
    if (!hasSelection) return
    // The caret is inside the block, which would keep it open: move it just past the block.
    editor.chain().command(({ tr }) => {
      const pos = getPos()
      if (typeof pos !== 'number') return false
      tr.setSelection(TextSelection.near(tr.doc.resolve(Math.min(pos + node.nodeSize, tr.doc.content.size)), 1))
      return true
    }).run()
  }
  const showNumbers = lineNumbers && !wrap
  return (
    <NodeViewWrapper className="flow-code-block" data-collapsed={!expanded || undefined} data-i18n-ignore data-line-numbers={showNumbers || undefined} data-wrap={wrap || undefined}>
      <div className="flow-code-block__controls" contentEditable={false}>
        <LanguagePicker detected={detected} editable={editable} language={language} onSelect={setLanguage}/>
        <SettingsMenu lineNumbers={lineNumbers} wrap={wrap}/>
        <CopyButton text={text}/>
      </div>
      <div className="flow-code-block__body" style={{ '--flow-code-lines': COLLAPSED_LINES } as CSSProperties}>
        {showNumbers && <div aria-hidden className="flow-code-block__gutter" contentEditable={false}>{Array.from({ length: lineCount }, (_, index) => <span key={index}>{index + 1}</span>)}</div>}
        {/* The node view content defaults to `white-space: pre-wrap` inline; long lines scroll sideways unless "Wrap lines" is on. */}
        <NodeViewContent<'code'> as="code" className="flow-code-block__code" spellCheck={false} style={{ whiteSpace: wrap ? 'pre-wrap' : 'pre' }}/>
      </div>
      {collapsible && (
        <button className="flow-code-block__expander" contentEditable={false} onClick={() => expanded ? collapse() : setShowAll(true)} type="button">
          {expanded ? <ChevronUp size={14}/> : <ChevronDown size={14}/>}
          {expanded ? t('Collapse') : t('Show all {count} lines').replace('{count}', String(lineCount))}
        </button>
      )}
    </NodeViewWrapper>
  )
}
