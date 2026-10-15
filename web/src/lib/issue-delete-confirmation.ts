import { withTrashRetention } from '@/lib/trash-retention'
import type { Issue } from '@/types/flow'

type Translate = (value: string) => string

/** The body for deleting issues: they stay restorable for the trash retention ({days} is filled by issueDeleteDescription). */
export const ISSUE_DELETE_DESCRIPTION = 'Deleted issues are available in the "Recently deleted" view for {days} days, before they are permanently deleted.'

/** The issue delete body, translated, with the trash retention filled in. */
export function issueDeleteDescription(t: Translate = value => value) {
  return withTrashRetention(t(ISSUE_DELETE_DESCRIPTION))
}

/**
 * The reference app's confirmation for deleting one issue: 'Delete "ENG-1 – Title"?', or with its sub-issues (they are deleted,
 * and restored, with it) 'Delete "ENG-1 – Title" and 2 sub-issues?'.
 */
export function issueDeleteConfirmation(issue: Pick<Issue, 'identifier' | 'title' | 'subIssueIds'>, t: Translate) {
  const name = `${issue.identifier} – ${issue.title}`
  const count = issue.subIssueIds?.length ?? 0
  const title = count === 0
    ? t('Delete "{name}"?').replace('{name}', name)
    : t(count === 1 ? 'Delete "{name}" and 1 sub-issue?' : 'Delete "{name}" and {count} sub-issues?').replace('{name}', name).replace('{count}', String(count))
  return { title, description: issueDeleteDescription(t), confirmLabel: t('Delete') }
}
