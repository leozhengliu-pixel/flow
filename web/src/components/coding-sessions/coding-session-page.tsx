import { Menu } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import { StatusIcon } from '@/components/issue/issue-icons'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { agentPath, codingSessionPath, issuePath } from '@/lib/app-routes'
import type { BootstrapData } from '@/types/flow'
import { CodingDiffGlyph } from './coding-diff-glyph'
import { codingSessionTitle } from './coding-session-page-model'
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
  return <main className="main-panel coding-session-page" aria-label={t('Coding session')}>
    <header className="coding-session-page-bar">
      <button type="button" className="coding-session-page-menu" aria-label={t('Open sidebar')} data-sidebar-trigger onClick={onOpenSidebar}><Menu size={16}/></button>
      {issue && <>
        <AppLink className="coding-session-dialog-crumb" href={issuePath(workspace, issue)}><StatusIcon state={issue.state} size={14}/><span data-i18n-ignore>{issue.identifier}</span></AppLink>
        <span className="coding-session-dialog-separator" aria-hidden="true">›</span>
      </>}
      <span className="coding-session-page-title">
        <CodingDiffGlyph/>
        <h2 data-i18n-ignore={Boolean(session) || undefined}>{session ? codingSessionTitle(session, data) : t('Coding session')}</h2>
      </span>
      <button type="button" className="coding-session-agent-toggle" aria-pressed={showAgent} aria-label={t(showAgent ? 'Hide agent' : 'Show agent')} title={t(showAgent ? 'Hide agent' : 'Show agent')}
        onClick={() => onNavigate(codingSessionPath(workspace, sessionId, { showAgent: !showAgent }), { replace: true })}><AgentCursorGlyph size={14}/></button>
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
