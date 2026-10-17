import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MutableRefObject, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { FileText } from 'lucide-react'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { useIssueCandidates } from '@/components/issue/use-issue-candidates'
import { avatarColor } from '@/components/issue/avatar-color'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { AGENT_UPLOAD_MAX_ITEMS, agentFileKind, agentUploadLimit, pastedAgentFiles, shrinkAgentImage, type AgentFileKind } from './agent-attachments'
import { agentBlockMarkdown, splitAgentBlocks } from './agent-content-blocks'
import { AgentInlineFile } from './agent-inline-file'
import { AgentInlineImage } from './agent-inline-image'
import { AgentInlineVideo } from './agent-inline-video'
import { toast } from '@/lib/toast'
import type { BootstrapData, WorkflowState } from '@/types/flow'
import styles from './agent-mention-input.module.css'
import { AgentSelectedTextIcon } from './agent-icons'

export type AgentMention = { type: 'issue' | 'project' | 'document' | 'initiative' | 'user'; id: string; label: string }

type MentionOption = AgentMention & { key: string; detail?: string; icon: ReactNode; group: string }

type Chip = { el: HTMLElement; option: MentionOption }

/** What the upload endpoint returns for a file. */
export type AgentUpload = { name: string; contentType: string; size: number; url: string }

type BlockKind = Exclude<AgentFileKind, 'audio'>

/** An image, video or file block in the composer: shown from `preview` while it uploads, then from its uploaded `src`. */
type InlineBlock = { el: HTMLElement; id: string; kind: BlockKind; name: string; src: string; size: number; type: string; preview?: string }

let inlineBlockId = 0

function inlineBlock(kind: BlockKind, name: string, src: string, size = 0, type = '') {
  const block = document.createElement('div')
  block.setAttribute('contenteditable', 'false')
  block.className = styles.block
  block.dataset.agentBlock = `block-${++inlineBlockId}`
  block.dataset.kind = kind
  block.dataset.name = name
  block.dataset.src = src
  block.dataset.size = String(size)
  block.dataset.type = type
  return block
}

const isBlock = (node: Node | null | undefined) => node instanceof HTMLElement && node.dataset.agentBlock !== undefined

/** A line with nothing on it: the one to type on after a block. */
const isEmptyLine = (node: Node | null | undefined) => node instanceof HTMLElement && node.tagName === 'DIV' && !isBlock(node) && !node.textContent && !node.querySelector('[data-agent-block], [data-mention-id]')

function emptyLine() {
  const line = document.createElement('div')
  line.append(document.createElement('br'))
  return line
}

/**
 * Writes a message into the composer: text as typed, its Markdown images, videos and files as blocks (editing a sent
 * message, restoring a draft). The composer picks the blocks up on the input event this fires.
 */
export function fillAgentComposer(editor: HTMLElement, value: string) {
  editor.textContent = ''
  const parts = splitAgentBlocks(value)
  parts.forEach((part, index) => {
    if (part.type === 'text') {
      if (!part.text) return
      // Text after a block sits on its own line, so the block's spacing applies to it.
      if (isBlock(editor.lastChild)) {
        const line = document.createElement('div')
        line.append(document.createTextNode(part.text))
        editor.append(line)
      } else editor.append(document.createTextNode(part.text))
      return
    }
    editor.append(part.type === 'image' ? inlineBlock('image', part.alt, part.src)
      : part.type === 'video' ? inlineBlock('video', part.name, part.src)
      : inlineBlock('file', part.name, part.src, part.size, part.contentType))
    if (!parts[index + 1]) editor.append(emptyLine())
  })
  editor.dispatchEvent(new Event('input', { bubbles: true }))
}

type MentionIssue = { id: string; identifier: string; title: string; state: Pick<WorkflowState, 'id' | 'name' | 'color' | 'type'> }

const GROUP_LIMIT = 5

/**
 * Agent composer with @-mentions: "@" opens a grouped picker of issues, projects, initiatives and documents,
 * and a choice becomes an inline chip. The plain-text value keeps "@identifier" / "@name" so the
 * message reads naturally, and the chosen resources are reported separately.
 */
export function AgentMentionInput({
  data,
  pageIssues = [],
  value,
  onChange,
  onSubmit,
  placeholder,
  disabled = false,
  ariaLabel,
  editorRef: externalRef,
  className,
  quote,
  onQuoteRemove,
  initialMentions,
  onTab,
  onPasteFiles,
  onUploadFile,
  insertFilesRef,
  onBusyChange,
}: {
  data: BootstrapData
  /** Issues from the current page, offered first under "This page". */
  pageIssues?: MentionIssue[]
  value: string
  onChange: (value: string, mentions: AgentMention[]) => void
  onSubmit: () => void
  placeholder: string
  disabled?: boolean
  ariaLabel: string
  /** Lets a host keep writing into the editor directly (drafts, edits). */
  editorRef?: RefObject<HTMLDivElement | null>
  className?: string
  /** Selected text shown as the reference app's "selected text" chip at the start of the composer. */
  quote?: string
  /** The user deleted the quote chip (Backspace or a cut). */
  onQuoteRemove?: () => void
  /** Mention chips the composer starts with, caret after them (the reference app's "Ask the reference app" on an "Issue created" toast). */
  initialMentions?: AgentMention[]
  /** Tab with no mention menu open; return true when the host handled it (e.g. toggling page context). */
  onTab?: () => boolean
  /** Files pasted into the composer (e.g. a screenshot) when it cannot upload them; without it a paste inserts plain text only. */
  onPasteFiles?: (files: File[]) => void
  /**
   * Uploads a file. With it, every pasted or attached file goes into the message itself, as the reference app does:
   * an image, video or file block uploaded at once and sent as Markdown (audio is ignored).
   */
  onUploadFile?: (file: File) => Promise<AgentUpload>
  /** Set to a function that inserts files at the caret (the attach button's files). */
  insertFilesRef?: MutableRefObject<((files: File[]) => void) | null>
  /** Whether files are still uploading; the host holds the message until they are done. */
  onBusyChange?: (busy: boolean) => void
}) {
  const { t } = useI18n()
  const ownRef = useRef<HTMLDivElement>(null)
  const editorRef = externalRef ?? ownRef
  const [query, setQuery] = useState<string | null>(null)
  const [activeIndex, setActiveIndex] = useState(0)
  const [chips, setChips] = useState<Chip[]>([])
  const [empty, setEmpty] = useState(!value && !quote)
  const [quoteEl, setQuoteEl] = useState<HTMLElement | null>(null)
  const [blocks, setBlocks] = useState<InlineBlock[]>([])
  // The page only holds the issues it loaded, so fetch workspace issues while the menu is open.
  const candidates = useIssueCandidates(data, query !== null)

  const options = useMemo(() => {
    if (query === null) return []
    const needle = query.toLocaleLowerCase()
    const matches = (text: string) => !needle || text.toLocaleLowerCase().includes(needle)
    const pageIds = new Set(pageIssues.map(issue => issue.id))
    const issue = (item: MentionIssue, group: string): MentionOption => ({ key: `issue:${item.id}`, type: 'issue', id: item.id, label: item.identifier, detail: item.title, icon: <StatusIcon state={item.state} size={14}/>, group })
    // "DEV-2" lists DEV-2 itself first (then DEV-2…), before the slice cuts the list.
    const ranked = <T extends MentionIssue>(items: T[]) => needle ? items.map((item, index) => ({ item, index, rank: issueMentionRank(item.identifier, needle) })).sort((a, b) => a.rank - b.rank || a.index - b.index).map(entry => entry.item) : items
    const page = ranked(pageIssues.filter(item => matches(`${item.identifier} ${item.title}`))).map(item => issue(item, 'This page'))
    const users = data.users.filter(item => item.active && !item.app && matches(`${item.displayName} ${item.name} ${item.email}`)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `user:${item.id}`, type: 'user', id: item.id, label: item.displayName, icon: <UserAvatar className={styles.avatar} avatarUrl={item.avatarUrl} color={avatarColor(item.id)} name={item.displayName}/>, group: 'Users' }))
    const issues = ranked(candidates.filter(item => !pageIds.has(item.id) && !item.archivedAt && matches(`${item.identifier} ${item.title}`))).slice(0, GROUP_LIMIT).map(item => issue(item, 'Issues'))
    const projects = needle ? data.projects.filter(item => !item.archivedAt && matches(item.name)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `project:${item.id}`, type: 'project', id: item.id, label: item.name, icon: <ProjectIcon size={14} style={{ color: item.color }}/>, group: 'Projects' })) : []
    const initiatives = needle ? (data.initiatives ?? []).filter(item => matches(item.name)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `initiative:${item.id}`, type: 'initiative', id: item.id, label: item.name, icon: <ViewGlyph color={item.color} icon={item.icon || 'Initiative'}/>, group: 'Initiatives' })) : []
    const documents = needle ? (data.documents ?? []).filter(item => matches(item.title)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `document:${item.id}`, type: 'document', id: item.id, label: item.title, icon: <FileText size={14}/>, group: 'Documents' })) : []
    return [...page, ...users, ...issues, ...projects, ...initiatives, ...documents]
  }, [candidates, data.documents, data.initiatives, data.projects, data.users, pageIssues, query])

  useEffect(() => { setActiveIndex(0) }, [query])

  const read = useCallback(() => {
    const editor = editorRef.current
    if (!editor) return { text: '', mentions: [] as AgentMention[] }
    const mentions: AgentMention[] = []
    const walk = (node: Node): string => {
      if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? ''
      if (!(node instanceof HTMLElement)) return ''
      // The quote travels separately (the host puts it first, as a markdown quote).
      if (node.dataset.agentQuote !== undefined) return ''
      if (node.dataset.mentionId) {
        mentions.push({ type: node.dataset.mentionType as AgentMention['type'], id: node.dataset.mentionId, label: node.dataset.mentionLabel ?? '' })
        return `@${node.dataset.mentionLabel ?? ''}`
      }
      if (isBlock(node)) return node.dataset.src ? agentBlockMarkdown({ kind: node.dataset.kind as BlockKind, name: node.dataset.name ?? '', src: node.dataset.src, size: node.dataset.size, type: node.dataset.type }) : ''
      if (node.tagName === 'BR') return '\n'
      const inner = [...node.childNodes].map(walk).join('')
      return node.tagName === 'DIV' && node !== editor ? `\n${inner}` : inner
    }
    const text = walk(editor).replace(/\u00a0/g, ' ').replace(/^ /, editor.querySelector('[data-agent-quote]') ? '' : ' ').replace(/\n{3,}/g, '\n\n')
    const unique = mentions.filter((item, index) => mentions.findIndex(other => other.type === item.type && other.id === item.id) === index)
    return { text, mentions: unique }
  }, [editorRef])

  const sync = useCallback(() => {
    const { text, mentions } = read()
    const editor = editorRef.current
    const quoted = Boolean(editor?.querySelector('[data-agent-quote]'))
    const found = [...(editor?.querySelectorAll<HTMLElement>('[data-agent-block]') ?? [])]
    setEmpty(!text.trim() && !mentions.length && !quoted && !found.length)
    setChips(current => current.filter(chip => chip.el.isConnected))
    // Blocks written in from outside (an edited message, a draft) join the ones pasted here.
    setBlocks(current => {
      const kept = current.filter(block => block.el.isConnected)
      current.filter(block => !block.el.isConnected && block.preview).forEach(block => URL.revokeObjectURL(block.preview!))
      const added = found.filter(el => !kept.some(block => block.el === el)).map((el): InlineBlock => ({ el, id: el.dataset.agentBlock!, kind: el.dataset.kind as BlockKind, name: el.dataset.name ?? '', src: el.dataset.src ?? '', size: Number(el.dataset.size) || 0, type: el.dataset.type ?? '' }))
      return added.length || kept.length !== current.length ? [...kept, ...added] : current
    })
    if (quote && !quoted) { setQuoteEl(null); onQuoteRemove?.() }
    onChange(text, mentions)
  }, [editorRef, onChange, onQuoteRemove, quote, read])

  // The quote chip sits first in the editor with the caret after it's chip from "Ask agent".
  useLayoutEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    const existing = editor.querySelector<HTMLElement>('[data-agent-quote]')
    if (!quote) { if (existing) { existing.remove(); setQuoteEl(null) } return }
    if (existing) { existing.dataset.agentQuote = quote; setQuoteEl(existing); return }
    const chip = document.createElement('span')
    chip.setAttribute('contenteditable', 'false')
    chip.className = styles.quoteChip
    chip.dataset.agentQuote = quote
    editor.insertBefore(chip, editor.firstChild)
    const after = document.createTextNode('\u00a0')
    editor.insertBefore(after, chip.nextSibling)
    setQuoteEl(chip)
    setEmpty(false)
  }, [editorRef, quote])

  // Like the reference app, "Ask agent" on a selection moves the caret into the composer, right after the chip (once it is usable).
  const focusedQuote = useRef<string | undefined>(undefined)
  useLayoutEffect(() => {
    const editor = editorRef.current
    const chip = editor?.querySelector<HTMLElement>('[data-agent-quote]')
    if (!editor || !chip || !quote || disabled || focusedQuote.current === quote) return
    focusedQuote.current = quote
    editor.focus({ preventScroll: true })
    const range = document.createRange()
    range.setStartAfter(chip.nextSibling ?? chip)
    range.collapse(true)
    const selection = window.getSelection()
    selection?.removeAllRanges()
    selection?.addRange(range)
  }, [disabled, editorRef, quote])

  // Mentions handed over by the host (e.g. "Ask Flow" on an "Issue created" toast) become chips, caret after them.
  const seededMentions = useRef<AgentMention[] | undefined>(undefined)
  useLayoutEffect(() => {
    const editor = editorRef.current
    if (!editor || !initialMentions?.length || disabled || seededMentions.current === initialMentions) return
    seededMentions.current = initialMentions
    const added: Chip[] = []
    let after: Text | undefined
    for (const mention of initialMentions) {
      const issue = mention.type === 'issue' ? data.issues.find(item => item.id === mention.id) : undefined
      const option: MentionOption = { ...mention, key: `${mention.type}:${mention.id}`, group: '', detail: issue?.title, icon: issue ? <StatusIcon state={issue.state} size={14}/> : null }
      const chip = document.createElement('span')
      chip.contentEditable = 'false'
      chip.className = styles.chip
      chip.dataset.mentionType = mention.type
      chip.dataset.mentionId = mention.id
      chip.dataset.mentionLabel = mention.label
      after = document.createTextNode(' ')
      editor.append(chip, after)
      added.push({ el: chip, option })
    }
    setChips(current => [...current, ...added])
    editor.focus({ preventScroll: true })
    if (after) {
      const range = document.createRange()
      range.setStart(after, 1)
      range.collapse(true)
      const selection = window.getSelection()
      selection?.removeAllRanges()
      selection?.addRange(range)
    }
    sync()
  }, [data.issues, disabled, editorRef, initialMentions, sync])

  // Clear the editor when the host resets the value (after sending); restore plain drafts on mount.
  useLayoutEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    const quoteChip = editor.querySelector('[data-agent-quote]')
    const typed = quoteChip ? (editor.textContent ?? '').slice((quoteChip.textContent ?? '').length).replace(/^\u00a0/, '') : editor.textContent
    const hasBlocks = Boolean(editor.querySelector('[data-agent-block]'))
    if (!value && (typed || hasBlocks)) {
      // Keep the quote chip: the host drops it (via `quote`) when the message is sent.
      editor.textContent = ''
      if (quoteChip) { editor.append(quoteChip, document.createTextNode('\u00a0')) }
      setChips([]); setBlocks(current => { current.forEach(block => block.preview && URL.revokeObjectURL(block.preview)); return [] }); setEmpty(!quoteChip); setQuery(null)
    }
    else if (value && !typed && !hasBlocks && !editor.querySelector('[data-mention-id]')) {
      if (splitAgentBlocks(value).some(part => part.type !== 'text')) fillAgentComposer(editor, value)
      else editor.append(document.createTextNode(value))
      setEmpty(false)
    }
  }, [editorRef, value])

  const detectQuery = () => {
    const selection = window.getSelection()
    const node = selection?.anchorNode
    if (!selection || !node || node.nodeType !== Node.TEXT_NODE || !editorRef.current?.contains(node)) { setQuery(null); return }
    const before = (node.textContent ?? '').slice(0, selection.anchorOffset)
    const match = before.match(/(?:^|\s)@([^\s@]{0,40})$/)
    setQuery(match ? match[1] : null)
  }

  const choose = (option: MentionOption) => {
    const selection = window.getSelection()
    const node = selection?.anchorNode
    if (!selection || !node || node.nodeType !== Node.TEXT_NODE) return
    const text = node.textContent ?? ''
    const caret = selection.anchorOffset
    const start = text.slice(0, caret).lastIndexOf('@')
    if (start < 0) return
    const chip = document.createElement('span')
    chip.contentEditable = 'false'
    chip.className = styles.chip
    chip.dataset.mentionType = option.type
    chip.dataset.mentionId = option.id
    chip.dataset.mentionLabel = option.label
    const after = document.createTextNode(`\u00a0${text.slice(caret)}`)
    node.textContent = text.slice(0, start)
    const parent = node.parentNode!
    parent.insertBefore(after, node.nextSibling)
    parent.insertBefore(chip, after)
    const range = document.createRange()
    range.setStart(after, 1)
    range.collapse(true)
    selection.removeAllRanges()
    selection.addRange(range)
    setChips(current => [...current, { el: chip, option }])
    setQuery(null)
    sync()
  }

  // Files go in after the line holding the caret (at the end without one), followed by a line to type on.
  const insertFiles = useCallback((picked: File[]) => {
    const editor = editorRef.current
    const files = picked.filter(file => agentFileKind(file) !== 'audio')
    if (!editor || !onUploadFile || disabled || !files.length) return
    const room = AGENT_UPLOAD_MAX_ITEMS - editor.querySelectorAll('[data-agent-block]').length
    if (files.length > room) toast.error(t('A message can carry up to 8 files'), { id: 'agent-attachment-limit' })
    const selection = window.getSelection()
    const caret = selection?.rangeCount && editor.contains(selection.getRangeAt(0).startContainer) ? selection.getRangeAt(0).startContainer : null
    let line: Node | null = caret ?? editor.lastChild
    while (line && line.parentNode !== editor) line = line.parentNode
    // The empty line after a block is where the next blocks go, ahead of it, so they stack 22px apart.
    const trailing = isEmptyLine(line) && isBlock(line!.previousSibling) ? line as HTMLElement : null
    const before = trailing ?? line?.nextSibling ?? null
    let focus: HTMLElement | null = trailing
    let inserted = false
    for (const file of files.slice(0, Math.max(0, room))) {
      const kind = agentFileKind(file) as BlockKind
      const preview = kind === 'file' ? undefined : URL.createObjectURL(file)
      const block = inlineBlock(kind, file.name, '', file.size, file.type)
      editor.insertBefore(block, before)
      inserted = true
      setBlocks(current => [...current, { el: block, id: block.dataset.agentBlock!, kind, name: file.name, src: '', size: file.size, type: file.type, preview }])
      const prepared = kind === 'image' ? shrinkAgentImage(file) : Promise.resolve(file)
      void prepared.then(ready => {
        if (ready.size > agentUploadLimit(ready)) throw new Error(t(agentUploadLimit(ready) > 2 * 1024 * 1024 ? "Files over 20 MB can't be attached" : "Files over 2 MB can't be attached"))
        return onUploadFile(ready)
      }).then(upload => {
        block.dataset.src = upload.url
        block.dataset.name = upload.name || file.name
        block.dataset.size = String(upload.size)
        block.dataset.type = upload.contentType
        if (preview) URL.revokeObjectURL(preview)
        setBlocks(current => current.map(item => item.el === block ? { ...item, preview: undefined, src: upload.url, name: upload.name || file.name, size: upload.size, type: upload.contentType } : item))
        sync()
      }).catch((error: unknown) => {
        block.remove()
        sync()
        toast.error(error instanceof Error ? error.message : t('Could not upload file'))
      })
    }
    if (inserted && !trailing && !isBlock(before)) {
      focus = emptyLine()
      editor.insertBefore(focus, before)
    }
    if (focus) {
      editor.focus({ preventScroll: true })
      const range = document.createRange()
      range.setStart(focus, 0)
      range.collapse(true)
      selection?.removeAllRanges()
      selection?.addRange(range)
      focus.scrollIntoView?.({ block: 'nearest' })
    }
    sync()
  }, [disabled, editorRef, onUploadFile, sync, t])

  useEffect(() => {
    if (!insertFilesRef) return
    insertFilesRef.current = insertFiles
    return () => { insertFilesRef.current = null }
  }, [insertFiles, insertFilesRef])

  const uploading = blocks.some(block => !block.src)
  useEffect(() => { onBusyChange?.(uploading) }, [onBusyChange, uploading])

  const removeBlock = (el: HTMLElement) => {
    // The line typed after a lone block goes with it when it is empty.
    const next = el.nextSibling
    if (isEmptyLine(next) && !isBlock(el.previousSibling)) (next as HTMLElement).remove()
    el.remove()
    sync()
  }

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (query !== null && options.length) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        setActiveIndex(index => (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length)
        return
      }
      if (event.key === 'Enter' || event.key === 'Tab') { event.preventDefault(); choose(options[activeIndex]); return }
    }
    if (query !== null && event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setQuery(null); return }
    // With the menu open and nothing to pick, Enter just closes it rather than sending.
    if (query !== null && event.key === 'Enter') { event.preventDefault(); setQuery(null); return }
    if (event.key === 'Tab' && !event.shiftKey && query === null && onTab?.()) { event.preventDefault(); return }
    if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); onSubmit() }
  }

  let lastGroup = ''
  // The menu lives in a portal so scroll containers around the editor cannot clip it.
  const anchor = query !== null ? editorRef.current?.getBoundingClientRect() : undefined
  const menuStyle = anchor ? { left: Math.max(8, anchor.left - 12), bottom: window.innerHeight - anchor.top + 12 } : undefined
  return <div className={styles.root}>
    {query !== null && anchor && createPortal(
      <div className={styles.menu} style={menuStyle} role="listbox" aria-label={t('Mention')} onMouseDown={event => event.preventDefault()}>
        {options.length ? options.map((option, index) => {
          const heading = option.group !== lastGroup ? option.group : ''
          lastGroup = option.group
          return <div key={option.key}>
            {heading && <div className={styles.group}>{t(heading)}</div>}
            <button type="button" role="option" aria-selected={index === activeIndex} className={styles.option} onMouseMove={() => setActiveIndex(index)} onClick={() => choose(option)}>
              <span className={styles.optionIcon}>{option.icon}</span>
              {option.detail ? <><span className={styles.identifier} data-i18n-ignore>{option.label}</span><span className={styles.title} data-i18n-ignore>{option.detail}</span></> : <span className={styles.title} data-i18n-ignore>{option.label}</span>}
            </button>
          </div>
        }) : (
          <div className={styles.none}><span>{t('No results found')}</span><button type="button" onClick={() => setQuery(null)}>{t('Dismiss')}</button></div>
        )}
      </div>,
      document.body,
    )}
    <div
      ref={editorRef}
      aria-label={ariaLabel}
      aria-multiline="true"
      className={className ? `${styles.editor} ${className}` : styles.editor}
      contentEditable={!disabled}
      data-empty={empty || undefined}
      data-placeholder={placeholder}
      role="textbox"
      suppressContentEditableWarning
      onInput={() => { sync(); detectQuery() }}
      onKeyUp={event => { if (!['ArrowDown', 'ArrowUp', 'Enter', 'Tab', 'Escape'].includes(event.key)) detectQuery() }}
      onClick={detectQuery}
      onBlur={() => setQuery(null)}
      onKeyDown={onKeyDown}
      onPaste={event => {
        event.preventDefault()
        const files = onPasteFiles || onUploadFile ? pastedAgentFiles(event.clipboardData) : []
        if (onUploadFile) insertFiles(files)
        else if (files.length) onPasteFiles?.(files)
        if (!files.length) document.execCommand('insertText', false, event.clipboardData.getData('text/plain'))
      }}
    />
    {quoteEl && quote && createPortal(<>
      <AgentSelectedTextIcon className={styles.quoteIcon}/>
      <span className={styles.quoteText} data-i18n-ignore>{quote}</span>
    </>, quoteEl, 'agent-quote')}
    {blocks.map(block => {
      const uploadingBlock = !block.src
      const src = block.src || block.preview || ''
      return createPortal(
        block.kind === 'image' ? <AgentInlineImage alt={block.name} editable onRemove={() => removeBlock(block.el)} src={src} uploading={uploadingBlock}/>
          : block.kind === 'video' ? <AgentInlineVideo editable name={block.name} onRemove={() => removeBlock(block.el)} src={src} uploading={uploadingBlock}/>
          : <AgentInlineFile contentType={block.type} editable name={block.name} onRemove={() => removeBlock(block.el)} size={block.size} src={src} uploading={uploadingBlock}/>,
        block.el,
        block.id,
      )
    })}
    {chips.map((chip, index) => createPortal(<>
      <span className={styles.chipIcon}>{chip.option.icon}</span>
      {chip.option.detail ? <><span className={styles.identifier}>{chip.option.label}</span> {chip.option.detail}</> : chip.option.label}
    </>, chip.el, `${chip.option.key}:${index}`))}
  </div>
}

/** How well an issue identifier answers a mention query: the exact identifier, then identifiers starting with it. */
function issueMentionRank(identifier: string, needle: string) {
  const key = identifier.toLocaleLowerCase()
  if (key === needle) return 0
  if (key.startsWith(needle)) return 1
  return 2
}
