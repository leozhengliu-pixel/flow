/**
 * The "Issue created" notification for an issue made in place from a selection ("Create issue from selection" in a
 * description, document or comment composer), measured in the reference app: no leading icon, the issue as a row (identifier,
 * status, title) that opens it, then a footer with "Ask Flow" (the reference app's "Ask the reference app": the floating chat with the issue
 * mentioned) and icon buttons to copy the issue URL, favorite it, copy it as a prompt and copy its ID. Shown by
 * toastIssueCreatedInPlace (lib/created-toasts) for 6s, shorter than the New issue dialog's "Issue created".
 */
import { useState, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'
import { toast as sonnerToast } from 'sonner'
import { requestFloatingAgent } from '@/components/agent/agent-floating-request'
import { FlowFavoriteFilledIcon, FlowFavoriteIcon, FlowIssueIdIcon, FlowUrlIcon, FlowWorkIcon } from '@/components/issue/flow-header-icons'
import { StatusIcon } from '@/components/issue/issue-icons'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { ToastCloseIcon, ToastCopyIcon } from '@/components/ui/toast-icons'
import { useOptionalTranslate } from '@/i18n/i18n'
import { toggleFavoriteFor } from '@/lib/favorites'
import { copyIssueForWork, issueWorkPrompt } from '@/lib/issue-work-actions'
import { toast } from '@/lib/toast'
import { openToastHref } from '@/lib/toast-registry'
import type { BootstrapData, Issue, IssueUpdateInput } from '@/types/flow'
import './issue-created-toast.css'

type Translate = (source: string) => string

// The reference app confirms a copy with its clipboard notice (the copy icon, in every language), not a success check.
function copied(message: string, t: Translate) {
  toast(t(message), { icon: <ToastCopyIcon/> })
}

async function copy(text: string, message: string, t: Translate) {
  try {
    await navigator.clipboard.writeText(text)
    copied(message, t)
  } catch {
    toast.error(t('Could not write to clipboard'))
  }
}

export function IssueCreatedToast({ id, issue, href, data, latestData, onUpdate }: {
  id: string | number
  issue: Issue
  href: string
  data: BootstrapData
  /** The workspace as it is now (the parent's description gains the new mention after the toast appears). */
  latestData?: () => BootstrapData | undefined
  /** Saves a change to the issue ("Copy as prompt" may move it to started, as the issue header's does). */
  onUpdate?: (input: IssueUpdateInput) => Promise<unknown>
}) {
  const t = useOptionalTranslate()
  const [favorite, setFavorite] = useState(() => (data.favorites ?? []).some(item => item.resourceType === 'issue' && item.resourceId === issue.id && item.userId === data.viewer.id && !item.tab))
  // The row follows the status "Copy as prompt" moves the issue to, as the reference app's does.
  const [state, setState] = useState(issue.state)
  const dismiss = () => { sonnerToast.dismiss(id) }
  const url = new URL(href, window.location.origin).href
  const openIssue = (event: MouseEvent<HTMLAnchorElement>) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return
    event.preventDefault()
    openToastHref(href)
    dismiss()
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); dismiss() }
  }
  const toggleFavorite = () => {
    const next = !favorite
    setFavorite(next)
    void Promise.resolve(toggleFavoriteFor(latestData?.() ?? data, 'issue', issue.id, next, favorite)).catch(() => setFavorite(!next))
  }
  // Exactly the issue header's "Copy as prompt": the viewer's template, and (with "On open in coding tool, move issue
  // to started status" on) the issue moves to the team's first started status; holding Option skips the move.
  const copyPrompt = async (skipMove: boolean) => {
    const current = latestData?.() ?? data
    const now = current.issues.find(item => item.id === issue.id) ?? { ...issue, state }
    try {
      const moved = await copyIssueForWork(issueWorkPrompt(now, current, url), 'prompt', now, current, onUpdate ?? (() => Promise.resolve()), { skipMove: skipMove || !onUpdate })
      if (moved) setState(moved)
      copied('Prompt copied to clipboard', t)
    } catch (error) {
      toast.error(error instanceof Error ? t(error.message) : t('Could not write to clipboard'))
    }
  }
  return (
    <div className="flow-toast flow-issue-toast" data-flow-toast="" data-kind="success" onKeyDown={onKeyDown} role="status">
      <div className="flow-issue-toast__body">
        <div className="flow-issue-toast__title">{t('Issue created')}</div>
        <a className="flow-issue-toast__issue" href={href} onClick={openIssue}>
          <span className="flow-issue-toast__identifier" data-i18n-ignore="">{issue.identifier}</span>
          <StatusIcon size={14} state={state}/>
          <span className="flow-issue-toast__issue-title" data-i18n-ignore="">{issue.title}</span>
        </a>
        <div className="flow-issue-toast__actions">
          <button className="flow-issue-toast__ask" onClick={() => { requestFloatingAgent({ mentions: [{ type: 'issue', id: issue.id, label: issue.identifier }] }); dismiss() }} type="button">
            <AgentCursorGlyph size={14}/>
            <span>{t('Ask Flow')}</span>
          </button>
          <div className="flow-issue-toast__icons">
            <IconAction label={t('Copy issue URL')} onClick={() => void copy(url, 'Issue URL copied to clipboard', t)}><FlowUrlIcon/></IconAction>
            {/* The reference app: the tooltip says Favorite / Unfavorite (⌥ F); the button keeps its name and shows a filled star. */}
            <IconAction ariaLabel={t('Favorite issue')} label={t(favorite ? 'Unfavorite issue' : 'Favorite issue')} onClick={toggleFavorite} pressed={favorite} shortcut="⌥ F">{favorite ? <FlowFavoriteFilledIcon/> : <FlowFavoriteIcon/>}</IconAction>
            <IconAction label={t('Copy as prompt')} onClick={event => void copyPrompt(event.altKey)}><FlowWorkIcon/></IconAction>
            <IconAction label={t('Copy issue ID')} onClick={() => void copy(issue.identifier, 'Issue ID copied to clipboard', t)}><FlowIssueIdIcon/></IconAction>
          </div>
        </div>
      </div>
      <button aria-label={t('Dismiss')} className="flow-toast__close" onClick={dismiss} type="button"><ToastCloseIcon/></button>
    </div>
  )
}

function IconAction({ label, ariaLabel = label, shortcut, pressed, onClick, children }: { label: string; ariaLabel?: string; shortcut?: string; pressed?: boolean; onClick: (event: MouseEvent<HTMLButtonElement>) => void; children: ReactNode }) {
  return (
    <ScopedFlowTooltip contentClassName="flow-toast-tooltip" label={label} shortcut={shortcut} side="top">
      <button aria-label={ariaLabel} aria-pressed={pressed} className="flow-issue-toast__icon" onClick={onClick} type="button">{children}</button>
    </ScopedFlowTooltip>
  )
}
