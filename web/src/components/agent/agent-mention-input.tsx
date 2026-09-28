import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { FileText } from 'lucide-react'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { useIssueCandidates } from '@/components/issue/use-issue-candidates'
import { avatarColor } from '@/components/issue/core-property-pickers'
import { UserAvatar } from '@/components/ui/user-avatar'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, WorkflowState } from '@/types/flow'
import styles from './agent-mention-input.module.css'

export type AgentMention = { type: 'issue' | 'project' | 'document' | 'user'; id: string; label: string }

/** Icon for a mention chip or option: status, project, document or avatar. */
export function mentionIcon(mention: Pick<AgentMention, 'type' | 'id'>, data: BootstrapData): ReactNode {
  if (mention.type === 'issue') {
    const issue = data.issues.find(item => item.id === mention.id)
    return issue ? <StatusIcon state={issue.state} size={14}/> : null
  }
  if (mention.type === 'project') {
    const project = data.projects.find(item => item.id === mention.id)
    return <ProjectIcon size={14} style={{ color: project?.color }}/>
  }
  if (mention.type === 'user') {
    const user = data.users.find(item => item.id === mention.id)
    return <UserAvatar className={styles.avatar} avatarUrl={user?.avatarUrl} color={avatarColor(mention.id)} name={user?.displayName ?? '?'}/>
  }
  return <FileText size={14}/>
}

type MentionOption = AgentMention & { key: string; detail?: string; icon: ReactNode; group: string }

type Chip = { el: HTMLElement; option: MentionOption }

type MentionIssue = { id: string; identifier: string; title: string; state: Pick<WorkflowState, 'id' | 'name' | 'color' | 'type'> }

const GROUP_LIMIT = 5

/**
 * Agent composer with @-mentions: "@" opens a grouped picker of issues, projects and documents,
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
}) {
  const { t } = useI18n()
  const ownRef = useRef<HTMLDivElement>(null)
  const editorRef = externalRef ?? ownRef
  const [query, setQuery] = useState<string | null>(null)
  const [activeIndex, setActiveIndex] = useState(0)
  const [chips, setChips] = useState<Chip[]>([])
  const [empty, setEmpty] = useState(!value)
  // The page only holds the issues it loaded, so fetch workspace issues while the menu is open.
  const candidates = useIssueCandidates(data, query !== null)

  const options = useMemo(() => {
    if (query === null) return []
    const needle = query.toLocaleLowerCase()
    const matches = (text: string) => !needle || text.toLocaleLowerCase().includes(needle)
    const pageIds = new Set(pageIssues.map(issue => issue.id))
    const issue = (item: MentionIssue, group: string): MentionOption => ({ key: `issue:${item.id}`, type: 'issue', id: item.id, label: item.identifier, detail: item.title, icon: <StatusIcon state={item.state} size={14}/>, group })
    const page = pageIssues.filter(item => matches(`${item.identifier} ${item.title}`)).map(item => issue(item, 'This page'))
    const users = data.users.filter(item => item.active && !item.app && matches(`${item.displayName} ${item.name} ${item.email}`)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `user:${item.id}`, type: 'user', id: item.id, label: item.displayName, icon: <UserAvatar className={styles.avatar} avatarUrl={item.avatarUrl} color={avatarColor(item.id)} name={item.displayName}/>, group: 'Users' }))
    const issues = candidates.filter(item => !pageIds.has(item.id) && !item.archivedAt && matches(`${item.identifier} ${item.title}`)).slice(0, GROUP_LIMIT).map(item => issue(item, 'Issues'))
    const projects = needle ? data.projects.filter(item => !item.archivedAt && matches(item.name)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `project:${item.id}`, type: 'project', id: item.id, label: item.name, icon: <ProjectIcon size={14} style={{ color: item.color }}/>, group: 'Projects' })) : []
    const documents = needle ? (data.documents ?? []).filter(item => matches(item.title)).slice(0, GROUP_LIMIT).map((item): MentionOption => ({ key: `document:${item.id}`, type: 'document', id: item.id, label: item.title, icon: <FileText size={14}/>, group: 'Documents' })) : []
    return [...page, ...users, ...issues, ...projects, ...documents]
  }, [candidates, data.documents, data.projects, data.users, pageIssues, query])

  useEffect(() => { setActiveIndex(0) }, [query])

  const read = useCallback(() => {
    const editor = editorRef.current
    if (!editor) return { text: '', mentions: [] as AgentMention[] }
    const mentions: AgentMention[] = []
    const walk = (node: Node): string => {
      if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? ''
      if (!(node instanceof HTMLElement)) return ''
      if (node.dataset.mentionId) {
        mentions.push({ type: node.dataset.mentionType as AgentMention['type'], id: node.dataset.mentionId, label: node.dataset.mentionLabel ?? '' })
        return `@${node.dataset.mentionLabel ?? ''}`
      }
      if (node.tagName === 'BR') return '\n'
      const inner = [...node.childNodes].map(walk).join('')
      return node.tagName === 'DIV' && node !== editor ? `\n${inner}` : inner
    }
    const text = walk(editor).replace(/\u00a0/g, ' ')
    const unique = mentions.filter((item, index) => mentions.findIndex(other => other.type === item.type && other.id === item.id) === index)
    return { text, mentions: unique }
  }, [])

  const sync = useCallback(() => {
    const { text, mentions } = read()
    setEmpty(!text.trim() && !mentions.length)
    setChips(current => current.filter(chip => chip.el.isConnected))
    onChange(text, mentions)
  }, [onChange, read])

  // Clear the editor when the host resets the value (after sending); restore plain drafts on mount.
  useLayoutEffect(() => {
    const editor = editorRef.current
    if (!editor) return
    if (!value && editor.textContent) { editor.textContent = ''; setChips([]); setEmpty(true); setQuery(null) }
    else if (value && !editor.textContent && !editor.querySelector('[data-mention-id]')) { editor.textContent = value; setEmpty(false) }
  }, [value])

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
      onPaste={event => { event.preventDefault(); document.execCommand('insertText', false, event.clipboardData.getData('text/plain')) }}
    />
    {chips.map((chip, index) => createPortal(<>
      <span className={styles.chipIcon}>{chip.option.icon}</span>
      {chip.option.detail ? <><span className={styles.identifier}>{chip.option.label}</span> {chip.option.detail}</> : chip.option.label}
    </>, chip.el, `${chip.option.key}:${index}`))}
  </div>
}
