import type { ComponentType } from 'react'
import { FileDiff, GitMerge, GitPullRequest, GitPullRequestClosed, GitPullRequestDraft } from 'lucide-react'
import { AppLink } from '@/components/ui/app-link'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { useI18n } from '@/i18n/i18n'
import { reviewPath } from '@/lib/app-routes'
import { toast } from '@/lib/toast'
import type { CodeReview } from '@/types/flow'
import type { CodingCommit, CodingSession, SessionPullRequest } from '@/types/coding-sessions'
import { reviewForPullRequest } from './coding-session-model'
import { PULL_REQUEST_CARD_LABELS, pullRequestCardStatus, type PullRequestCardStatus } from './coding-session-stream-model'
import './coding-session-stream.css'

const STATUS_ICONS: Record<PullRequestCardStatus, ComponentType<{ size?: number; 'aria-hidden'?: boolean; className?: string }>> = {
  draft: GitPullRequestDraft, open: GitPullRequest, inReview: GitPullRequest, approved: GitPullRequest, merged: GitMerge, closed: GitPullRequestClosed,
}

const external = (url: string | undefined) => url && /^https?:\/\//i.test(url) ? url : undefined

/** The code host page of a branch or commit. */
function hostUrl(pr: Pick<SessionPullRequest, 'codeHost' | 'hostName' | 'repository'>, kind: 'tree' | 'commit', ref: string) {
  const host = pr.hostName || (pr.codeHost === 'gitlab' ? 'gitlab.com' : 'github.com')
  const base = `https://${host}/${pr.repository}`
  return pr.codeHost === 'gitlab' ? `${base}/-/${kind}/${encodeURIComponent(ref)}` : `${base}/${kind}/${encodeURIComponent(ref)}`
}

async function copy(text: string, done: string, t: (source: string) => string) {
  try { await navigator.clipboard.writeText(text); toast.success(t(done)) }
  catch { toast.error(t('Could not copy')) }
}

function BranchChip({ pr, branch, kind }: { pr: SessionPullRequest; branch: string; kind: 'base' | 'head' }) {
  const { t } = useI18n()
  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <button type="button" className={`cs-branch-chip is-${kind}`} title={branch} data-i18n-ignore>{branch}</button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" className="coding-session-menu">
      <DropdownMenuItem onSelect={() => void copy(branch, 'Copied branch name', t)}>{t('Copy branch name')}</DropdownMenuItem>
      <DropdownMenuItem onSelect={() => window.open(hostUrl(pr, 'tree', branch), '_blank', 'noopener')}>{t(pr.codeHost === 'gitlab' ? 'Open on GitLab' : 'Open on GitHub')}</DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
}

/**
 * The session's pull request after the last response: changed files with +/−, the status icon and label, the title, and
 * the base ← head branches. The whole card opens the PR (Flow's review page when it is synced, else the code host).
 */
export function CodingPullRequestCard({ session, pr, reviews, workspace }: { session: Pick<CodingSession, 'diffs'>; pr: SessionPullRequest; reviews: readonly CodeReview[]; workspace: string }) {
  const { t } = useI18n()
  const review = reviewForPullRequest(pr, reviews)
  const status = pullRequestCardStatus(pr, review)
  const Icon = STATUS_ICONS[status]
  const diffs = session.diffs ?? []
  const additions = diffs.reduce((sum, diff) => sum + diff.additions, 0)
  const deletions = diffs.reduce((sum, diff) => sum + diff.deletions, 0)
  const title = pr.title || review?.title
  const noun = pr.codeHost === 'gitlab' ? 'merge request' : 'pull request'
  const label = `${t(`Open ${noun}`)} ${title ?? ''}`.trim()
  return <article className="cs-pr-card" data-status={status} aria-label={t(pr.codeHost === 'gitlab' ? 'Merge request' : 'Pull request')}>
    {review
      ? <AppLink className="cs-pr-card-link" href={reviewPath(workspace, review)} aria-label={label}/>
      : <a className="cs-pr-card-link" href={external(pr.url)} target="_blank" rel="noreferrer" aria-label={label}/>}
    {diffs.length > 0 && <div className="cs-pr-card-stats">
      <span><strong>{t('Changed')}</strong> <span className="cs-tabular">{t(diffs.length === 1 ? '{count} file' : '{count} files').replace('{count}', String(diffs.length))}</span>
        <span className="cs-additions">+{additions}</span><span className="cs-deletions">-{deletions}</span></span>
    </div>}
    <div className="cs-pr-card-title">
      <span className="cs-pr-card-status"><Icon size={14} aria-hidden className="cs-pr-status-icon"/>{t(PULL_REQUEST_CARD_LABELS[status])}</span>
      {title ? <span className="cs-pr-card-name" data-i18n-ignore>{title}</span> : <span className="cs-pr-card-name is-loading">{t('Loading…')}</span>}
    </div>
    <div className="cs-pr-card-branches">
      <BranchChip pr={pr} branch={pr.baseBranch} kind="base"/>
      <span className="cs-branch-arrow" aria-hidden="true">←</span>
      <span className="sr-only">{t('from')}</span>
      <BranchChip pr={pr} branch={pr.headBranch} kind="head"/>
    </div>
  </article>
}

/** The commits a turn pushed: one short SHA, or "N commits" listing each. */
export function CodingCommitBadge({ commits, pr }: { commits: readonly CodingCommit[]; pr?: Pick<SessionPullRequest, 'codeHost' | 'hostName' | 'repository'> }) {
  const { t } = useI18n()
  const open = (sha: string) => {
    if (pr) window.open(hostUrl(pr, 'commit', sha), '_blank', 'noopener')
    else void copy(sha, 'Copied commit SHA', t)
  }
  if (commits.length === 1) {
    const [commit] = commits
    return <button type="button" className="cs-commit-badge" title={commit.message?.split('\n')[0] || commit.sha} onClick={() => open(commit.sha)}>
      <span className="sr-only">{t('Commit')}</span><code data-i18n-ignore>{commit.sha.slice(0, 7)}</code>
    </button>
  }
  return <DropdownMenu>
    <DropdownMenuTrigger asChild>
      <button type="button" className="cs-commit-badge"><span className="cs-commit-label">{t('{count} commits').replace('{count}', String(commits.length))}</span></button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="start" className="coding-session-menu">
      <DropdownMenuLabel>{t('All {count} commits').replace('{count}', String(commits.length))}</DropdownMenuLabel>
      <DropdownMenuSeparator/>
      {commits.map(commit => <DropdownMenuItem key={commit.sha} onSelect={() => open(commit.sha)}>
        <code className="cs-commit-sha" data-i18n-ignore>{commit.sha.slice(0, 7)}</code><span className="cs-commit-message" data-i18n-ignore>{commit.message?.split('\n')[0]}</span>
      </DropdownMenuItem>)}
    </DropdownMenuContent>
  </DropdownMenu>
}

/** Changes that are in the sandbox but not in a commit or PR yet; opens the changes. */
export function CodingUncommittedChanges({ session, onOpen }: { session: Pick<CodingSession, 'diffs'>; onOpen: () => void }) {
  const { t } = useI18n()
  const diffs = session.diffs ?? []
  const additions = diffs.reduce((sum, diff) => sum + diff.additions, 0)
  const deletions = diffs.reduce((sum, diff) => sum + diff.deletions, 0)
  return <button type="button" className="cs-uncommitted" onClick={onOpen}>
    <FileDiff size={16} aria-hidden="true"/>{t('Uncommitted changes')}<span className="cs-additions">+{additions}</span><span className="cs-deletions">-{deletions}</span>
  </button>
}
