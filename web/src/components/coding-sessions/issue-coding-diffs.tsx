import { AppLink } from '@/components/ui/app-link'
import { useI18n } from '@/i18n/i18n'
import { codingSessionPath } from '@/lib/app-routes'
import type { BootstrapData, Issue } from '@/types/flow'
import { CodingDiffGlyph } from './coding-diff-glyph'
import { useCodingAvailability, useCodingSessions } from './use-coding-sessions'
import './coding-sessions.css'

/**
 * The issue sidebar's "Diffs" section (the reference app shows it once the issue has a coding session): "Code changes"
 * with the latest session's +/- counts, opening that session's page.
 */
export function IssueCodingDiffs({ issue, data }: { issue: Pick<Issue, 'id'>; data: BootstrapData }) {
  const { t } = useI18n()
  const available = useCodingAvailability(data)
  const { sessions } = useCodingSessions({ issueId: issue.id }, available)
  const latest = [...sessions].sort((a, b) => (Date.parse(b.createdAt) || 0) - (Date.parse(a.createdAt) || 0))[0]
  if (!available || !latest) return null
  const additions = (latest.diffs ?? []).reduce((sum, diff) => sum + diff.additions, 0)
  const deletions = (latest.diffs ?? []).reduce((sum, diff) => sum + diff.deletions, 0)
  return <section className="property-group issue-coding-diffs" aria-label={t('Diffs')}>
    <h4>{t('Diffs')}</h4>
    <AppLink className="issue-coding-diffs-row" href={codingSessionPath(data.workspace.urlKey, latest.id)}>
      <CodingDiffGlyph size={14}/>
      <span className="issue-coding-diffs-label">{t('Code changes')}</span>
      {(additions > 0 || deletions > 0) && <span className="coding-diff-counts"><span className="coding-additions">+{additions}</span><span className="coding-deletions">−{deletions}</span></span>}
    </AppLink>
  </section>
}
