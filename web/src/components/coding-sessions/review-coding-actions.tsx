import { useRef, useState } from 'react'
import * as RadixMenu from '@radix-ui/react-dropdown-menu'
import { ChevronDown } from 'lucide-react'
import { AgentCursorGlyph } from '@/components/ui/agent-glyph'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { useI18n } from '@/i18n/i18n'
import { startReviewCodingSession } from '@/lib/coding-sessions-api'
import { toast } from '@/lib/toast'
import type { BootstrapData, CodeReview } from '@/types/flow'
import type { CodingSession, ReviewCodingSessionKind } from '@/types/coding-sessions'
import { CodingSessionDialog } from './coding-session-card'
import { notifyCodingSessionChanged } from './coding-session-events'
import { toastCodingStartError } from './coding-session-start'
import { useCodingAvailability } from './use-coding-sessions'
import './coding-sessions.css'

const BRANCH_PREFIX = 'flow/'

const DONE: Record<ReviewCodingSessionKind, string> = {
  fix: 'Started a session to fix checks and comments',
  followUp: 'Sent to the coding agent',
  rebase: 'Started a session to rebase the branch',
  lint: 'Started a session to fix lint',
  test: 'Started a session to fix tests',
}

/**
 * Reviews: "Fix with agent" (failing checks and unresolved threads) plus the quick actions rebase, lint, test and a
 * free follow-up prompt (§15, `POST /api/reviews/{id}/coding-session`, track L). The started session opens in a dialog.
 */
export function ReviewCodingSessionActions({ review, data }: { review: Pick<CodeReview, 'id' | 'status' | 'headBranch' | 'provider'>; data: BootstrapData }) {
  const { t } = useI18n()
  const available = useCodingAvailability(data)
  const [busy, setBusy] = useState(false)
  const busyRef = useRef(false)
  const [session, setSession] = useState<CodingSession>()
  const [prompting, setPrompting] = useState(false)
  const [confirmRebase, setConfirmRebase] = useState(false)
  if (!available || review.status === 'merged' || review.status === 'closed') return null
  const start = async (kind: ReviewCodingSessionKind, prompt?: string) => {
    if (busyRef.current) return
    busyRef.current = true; setBusy(true)
    try {
      const started = await startReviewCodingSession(review.id, { kind, prompt })
      notifyCodingSessionChanged({ sessionId: started.id, agentSessionId: started.agentSessionId, issueId: started.issueId })
      toast.success(t(DONE[kind]))
      setSession(started)
      setPrompting(false); setConfirmRebase(false)
    } catch (error) { toastCodingStartError(error, data.workspace.urlKey, t) }
    finally { busyRef.current = false; setBusy(false) }
  }
  const rebase = () => review.headBranch?.startsWith(BRANCH_PREFIX) ? void start('rebase') : setConfirmRebase(true)
  return <>
    <div className="coding-review-actions" role="group" aria-label={t('Coding agent')}>
      <button type="button" className="coding-session-button" disabled={busy} onClick={() => void start('fix')}><AgentCursorGlyph size={14}/>{t('Fix with agent')}</button>
      <RadixMenu.Root>
        <RadixMenu.Trigger asChild><button type="button" className="coding-session-button is-icon" aria-label={t('More coding agent actions')} disabled={busy}><ChevronDown size={13}/></button></RadixMenu.Trigger>
        <RadixMenu.Portal>
          <RadixMenu.Content data-flow-motion="floating" align="end" sideOffset={4} collisionPadding={16} className="project-action-menu coding-review-menu" aria-label={t('Coding agent actions')}>
            <RadixMenu.Item onSelect={() => void start('fix')}>{t('Fix failing checks and review comments')}</RadixMenu.Item>
            <RadixMenu.Item onSelect={rebase}>{t('Rebase on base branch')}</RadixMenu.Item>
            <RadixMenu.Item onSelect={() => void start('lint')}>{t('Fix lint')}</RadixMenu.Item>
            <RadixMenu.Item onSelect={() => void start('test')}>{t('Fix tests')}</RadixMenu.Item>
            <RadixMenu.Separator/>
            <RadixMenu.Item onSelect={() => setPrompting(true)}>{t('Ask the agent…')}</RadixMenu.Item>
          </RadixMenu.Content>
        </RadixMenu.Portal>
      </RadixMenu.Root>
    </div>
    {prompting && <ReviewPromptDialog busy={busy} onCancel={() => setPrompting(false)} onSubmit={prompt => void start('followUp', prompt)}/>}
    {confirmRebase && <Dialog open onOpenChange={open => { if (!open) setConfirmRebase(false) }}>
      <DialogContent className="coding-review-dialog" aria-describedby={undefined} closeLabel={t('Close')}>
        <DialogTitle>{t('Rebase this branch?')}</DialogTitle>
        <p>{t('The agent rebases the branch on its base and force-pushes it with --force-with-lease. This branch wasn’t created by Flow.')}</p>
        <footer>
          <button type="button" className="coding-session-button" onClick={() => setConfirmRebase(false)}>{t('Cancel')}</button>
          <button type="button" className="coding-session-button is-primary" disabled={busy} onClick={() => void start('rebase')}>{t('Rebase')}</button>
        </footer>
      </DialogContent>
    </Dialog>}
    {session && <CodingSessionDialog sessionId={session.id} initial={session} data={data} open onOpenChange={open => { if (!open) setSession(undefined) }}/>}
  </>
}

function ReviewPromptDialog({ busy, onCancel, onSubmit }: { busy: boolean; onCancel: () => void; onSubmit: (prompt: string) => void }) {
  const { t } = useI18n()
  const [prompt, setPrompt] = useState('')
  return <Dialog open onOpenChange={open => { if (!open) onCancel() }}>
    <DialogContent className="coding-review-dialog" aria-describedby={undefined} closeLabel={t('Close')}>
      <DialogTitle>{t('Ask the coding agent')}</DialogTitle>
      <form onSubmit={event => { event.preventDefault(); if (prompt.trim()) onSubmit(prompt.trim()) }}>
        <textarea aria-label={t('Prompt')} autoFocus value={prompt} onChange={event => setPrompt(event.target.value)} placeholder={t('What should the agent change?')}/>
        <footer>
          <button type="button" className="coding-session-button" onClick={onCancel}>{t('Cancel')}</button>
          <button type="submit" className="coding-session-button is-primary" disabled={busy || !prompt.trim()}>{t('Send')}</button>
        </footer>
      </form>
    </DialogContent>
  </Dialog>
}
