import { useEffect } from 'react'
import { GitPullRequest, Menu } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import { FlowBranchIcon, FlowUrlIcon } from '@/components/issue/flow-header-icons'
import { ScopedFlowTooltip } from '@/components/ui/tooltip'
import { StatusIcon } from '@/components/issue/issue-icons'
import { AgentCursorFilledGlyph, AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { agentPath, codingSessionPath, issuePath, reviewPath } from '@/lib/app-routes'
import { toast } from '@/lib/toast'
import type { BootstrapData } from '@/types/flow'
import { CodingDiffGlyph } from './coding-diff-glyph'
import { pullRequestNumberLabel, reviewForPullRequest } from './coding-session-model'
import { codingSessionTitle, codingSessionURL } from './coding-session-page-model'
import { CodingSessionPanel } from './coding-session-panel'
import { useCodingSession, useDismissedCodingSessions } from './use-coding-sessions'
import './coding-sessions.css'

type Navigate = (path: string, options?: { replace?: boolean }) => void

/**
 * A coding session's own page (`/<workspace>/diff/<sessionId>`, like the reference app's session page): a 44px bar with
 * the issue crumb, the session title and the "Hide agent" toggle over the code changes and the 506px agent panel.
 * The agent panel's visibility rides in the URL (`?showAgent=false`), so back and forward restore it.
 */
export function CodingSessionPage({ data, sessionId, onNavigate, onOpenSidebar }: { data: BootstrapData; sessionId: string; onNavigate: Navigate; onOpenSidebar: () => void }) {
  const { t } = useI18n()
  const location = useLocation()
  const { session, error, apply } = useCodingSession(sessionId)
  const { dismiss } = useDismissedCodingSessions(data.workspace.urlKey, data.viewer.id)
  const workspace = data.workspace.urlKey
  const showAgent = new URLSearchParams(location.search).get('showAgent') !== 'false'
  const issue = session?.issueId ? data.issues.find(item => item.id === session.issueId) : undefined
  const leave = () => onNavigate(issue ? issuePath(workspace, issue) : agentPath(workspace))
  // The header's change count, the PR reference (Flow's review when synced, else the code host) and "Copy URL" (with a PR).
  const additions = (session?.diffs ?? []).reduce((sum, diff) => sum + diff.additions, 0)
  const pr = session?.latestPullRequest ?? session?.pullRequests?.at(-1)
  const review = pr ? reviewForPullRequest(pr, data.reviews ?? []) : undefined
  const prHref = review ? reviewPath(workspace, review) : pr && /^https?:\/\//i.test(pr.url) ? pr.url : undefined
  const copyURL = async () => {
    try { await navigator.clipboard.writeText(codingSessionURL(workspace, sessionId)); toast.success(t('Link copied to clipboard')) }
    catch { toast.error(t('Could not copy')) }
  }
  const toggleAgent = () => onNavigate(codingSessionPath(workspace, sessionId, { showAgent: !showAgent }), { replace: true })
  // "]" shows or hides the agent panel, as the toggle's tooltip says (not while typing).
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== ']' || event.metaKey || event.ctrlKey || event.altKey || event.defaultPrevented) return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable=""], [contenteditable="true"], [role="dialog"], [role="menu"]')) return
      event.preventDefault()
      toggleAgent()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })
  return <main className="main-panel coding-session-page" aria-label={t('Coding session')}>
    <header className="coding-session-page-bar">
      <button type="button" className="coding-session-page-menu" aria-label={t('Open sidebar')} data-sidebar-trigger onClick={onOpenSidebar}><Menu size={16}/></button>
      {issue && <>
        <AppLink className="coding-session-dialog-crumb" href={issuePath(workspace, issue)}><StatusIcon state={issue.state} size={14}/><span data-i18n-ignore>{issue.identifier}</span></AppLink>
        <span className="coding-session-dialog-separator" aria-hidden="true">›</span>
      </>}
      <span className="coding-session-page-title">
        {pr ? <GitPullRequest size={14} aria-hidden="true" className="coding-diff-glyph"/> : <CodingDiffGlyph/>}
        <h2 data-i18n-ignore={Boolean(session) || undefined}>{session ? codingSessionTitle(session, data) : t('Coding session')}</h2>
        {additions > 0 && <span className="coding-session-page-additions" aria-label={t('{count} lines added').replace('{count}', String(additions))}>+{additions}</span>}
      </span>
      {pr && <ScopedFlowTooltip label={`${pr.repository}${pullRequestNumberLabel(pr)}`}><a className="coding-session-page-pr" href={prHref} target={review ? undefined : '_blank'} rel={review ? undefined : 'noreferrer'} aria-label={`${pr.repository}${pullRequestNumberLabel(pr)}`} onClick={event => {
        if (!review || !prHref || event.metaKey || event.ctrlKey || event.shiftKey || event.button !== 0) return
        event.preventDefault(); onNavigate(prHref)
      }}><FlowBranchIcon/><span data-i18n-ignore>{`${pr.repository.split('/').at(-1)}${pullRequestNumberLabel(pr)}`}</span></a></ScopedFlowTooltip>}
      {/* Like the reference app, a session without a pull request offers only the agent toggle here. */}
      {pr && <ScopedFlowTooltip label={t('Copy session URL')} align="end"><button type="button" className="coding-session-page-icon" aria-label={t('Copy URL')} onClick={() => void copyURL()}><FlowUrlIcon/></button></ScopedFlowTooltip>}
      <ScopedFlowTooltip label={t(showAgent ? 'Hide agent' : 'Show agent')} shortcut="]" align="end">
        <button type="button" className="coding-session-agent-toggle" aria-pressed={showAgent} aria-label={t(showAgent ? 'Hide agent' : 'Show agent')} onClick={toggleAgent}>{showAgent ? <AgentCursorFilledGlyph size={14}/> : <AgentCursorGlyph size={14}/>}</button>
      </ScopedFlowTooltip>
    </header>
    {session
      ? <CodingSessionPanel session={session} data={data} onChange={apply} agentHidden={!showAgent} className="coding-session-page-body"
        onOpenSession={next => onNavigate(codingSessionPath(workspace, next.id))}
        onDismiss={() => { dismiss(session.id); leave() }}/>
      : error
        ? <p className="coding-session-error-text coding-session-page-message" role="alert">{error}</p>
        : <p className="coding-session-muted coding-session-page-message" role="status">{t('Loading coding session…')}</p>}
  </main>
}
