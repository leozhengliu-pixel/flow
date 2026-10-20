import type { BootstrapData, CodeReview } from '@/types/flow'

export type ReviewView = 'for-you' | 'created'
export type ReviewGrouping = 'none' | 'focus' | 'status' | 'author' | 'repository'
export type ReviewOrdering = 'importance' | 'name' | 'author' | 'status' | 'opened' | 'updated'
export type ReviewProperty = 'repository' | 'id' | 'author' | 'opened' | 'status' | 'quick'
export interface ReviewDisplay {
  grouping: ReviewGrouping
  ordering: ReviewOrdering
  descending: boolean
  closed: 'all' | 'day' | 'week' | 'month' | 'none'
  showDrafts: boolean
  showTeams: boolean
  properties: ReviewProperty[]
}
export type ReviewFilterField = 'status' | 'author' | 'reviewer' | 'repository' | 'quick' | 'missingIssue'
export interface ReviewFilter { id: ReviewFilterField; fieldLabel: string; operator: 'is' | 'isNot'; values: { id: string; label: string }[] }
export const reviewStatuses = ['draft', 'open', 'inReview', 'approved', 'merged', 'closed'] as const
export const reviewStatusLabels: Record<string, string> = { draft: 'Draft', open: 'Open', inReview: 'In review', approved: 'Approved', merged: 'Merged', closed: 'Closed' }
export const reviewFieldLabels: Record<ReviewFilterField, string> = { status: 'Status', author: 'Author', reviewer: 'Reviewers', repository: 'Repository name', quick: 'Quick to review', missingIssue: 'Missing issue' }
export const reviewPropertyOptions: { id: ReviewProperty; label: string }[] = [
  { id: 'repository', label: 'Repository' }, { id: 'id', label: 'ID' }, { id: 'author', label: 'Author' },
  { id: 'opened', label: 'Opened at' }, { id: 'status', label: 'Status' }, { id: 'quick', label: 'Quick to review' },
]
export function defaultReviewDisplay(view: ReviewView): ReviewDisplay {
  return { grouping: view === 'for-you' ? 'focus' : 'status', ordering: view === 'for-you' ? 'importance' : 'opened', descending: view !== 'for-you', closed: view === 'for-you' ? 'day' : 'week', showDrafts: true, showTeams: true, properties: ['opened', 'quick'] }
}
export function changeReviewGrouping(display: ReviewDisplay, grouping: ReviewGrouping): ReviewDisplay {
  if (grouping === 'focus') return { ...display, grouping, ordering: 'importance', descending: false }
  return { ...display, grouping, ...(display.ordering === 'importance' ? { ordering: 'opened' as const, descending: true } : {}) }
}
export function reviewStatus(review: CodeReview) { return review.draft && !['merged', 'closed'].includes(review.status) ? 'draft' : review.status }
export function reviewRepository(review: CodeReview) { return `${review.repositoryOwner}/${review.repositoryName}` }
export function reviewTeamIds(data: BootstrapData) { return new Set((data.teamMembers ?? []).filter(member => member.userId === data.viewer.id).map(member => member.teamId)) }
export function reviewIsTeamRequest(review: CodeReview, teamIds: Set<string>) { return review.provider === 'github' && review.teamReviewers.some(id => teamIds.has(id)) }
export function reviewBaseItems(data: BootstrapData, view: ReviewView, display: ReviewDisplay, now = Date.now()) {
  const teamIds = reviewTeamIds(data)
  return data.reviews.filter(review => {
    const own = review.author.id === data.viewer.id, direct = review.reviewerIds.includes(data.viewer.id)
    if (view === 'created' ? !own : !(own || direct || (display.showTeams && reviewIsTeamRequest(review, teamIds)))) return false
    if (!display.showDrafts && review.draft) return false
    if (review.status !== 'merged' && review.status !== 'closed') return true
    if (display.closed === 'all') return true
    if (display.closed === 'none') return false
    const closedAt = review.status === 'merged' ? review.mergedAt ?? review.closedAt : review.closedAt
    // Provider records without a closure time cannot be placed in an age window.
    if (!closedAt || !Number.isFinite(Date.parse(closedAt))) return true
    const days = display.closed === 'day' ? 1 : display.closed === 'week' ? 7 : 30
    return now - Date.parse(closedAt) <= days * 86_400_000
  })
}
export function matchesReviewFilters(review: CodeReview, filters: ReviewFilter[]) {
  return filters.every(filter => {
    const actual = filter.id === 'status' ? [reviewStatus(review)] : filter.id === 'author' ? [review.author.id]
      : filter.id === 'reviewer' ? review.reviewerIds : filter.id === 'repository' ? [reviewRepository(review)]
      : [String(filter.id === 'quick' ? review.quickToReview : !review.issueIds.length)]
    const matched = filter.values.some(value => actual.includes(value.id))
    return filter.operator === 'isNot' ? !matched : matched
  })
}
export function reviewImportance(review: CodeReview) {
  if (review.status === 'merged' || review.status === 'closed') return 5
  if (review.draft) return 4
  if (review.status === 'approved' && review.branchState !== 'conflicted' && review.checks.every(check => check.status === 'passed' || check.status === 'skipped')) return 0
  if (review.status === 'approved') return 1
  return review.status === 'inReview' ? 2 : 3
}
export function compareReviews(a: CodeReview, b: CodeReview, display: ReviewDisplay) {
  const direction = display.descending ? -1 : 1
  const value = display.ordering === 'importance' ? reviewImportance(a) - reviewImportance(b)
    : display.ordering === 'name' ? a.title.localeCompare(b.title)
    : display.ordering === 'author' ? a.author.displayName.localeCompare(b.author.displayName)
    : display.ordering === 'status' ? reviewStatuses.indexOf(reviewStatus(a)) - reviewStatuses.indexOf(reviewStatus(b))
    : Date.parse(display.ordering === 'opened' ? a.createdAt : a.updatedAt) - Date.parse(display.ordering === 'opened' ? b.createdAt : b.updatedAt)
  return (Number.isFinite(value) ? value : 0) * (display.ordering === 'importance' ? 1 : direction) || b.createdAt.localeCompare(a.createdAt) || a.id.localeCompare(b.id)
}
// Focus groups in the reference order: ready, needs review (direct, then team),
// created by you, already reviewing, completed.
const focusLabels: Record<string, string> = { ready: 'Ready to merge', requested: 'Needs your review', team: 'Needs review from your GitHub team', yours: 'Created by you', reviewing: 'You’re reviewing', completed: 'Completed' }
const reviewDecisionEvents = new Set(['approved', 'changes_requested', 'review_commented'])
/** True once the viewer has submitted a review (approve, request changes or review comment). */
export function viewerHasReviewed(review: CodeReview, viewerId: string) { return review.events.some(event => event.actor.id === viewerId && reviewDecisionEvents.has(event.type)) }
/** Groups that start collapsed: finished work. */
export function reviewGroupCollapsedByDefault(id: string) { return id === 'completed' || id === 'merged' || id === 'closed' }
export function groupReviews(reviews: CodeReview[], display: ReviewDisplay, viewerId: string) {
  const groups = new Map<string, { id: string; label: string; items: CodeReview[] }>()
  for (const review of reviews) {
    const id = display.grouping === 'none' ? 'all' : display.grouping === 'status' ? reviewStatus(review)
      : display.grouping === 'repository' ? reviewRepository(review) : display.grouping === 'author' ? review.author.id
      : ['merged', 'closed'].includes(review.status) ? 'completed'
      : review.author.id === viewerId ? reviewReadyToMerge(review) ? 'ready' : 'yours'
      : viewerHasReviewed(review, viewerId) ? 'reviewing'
      : review.reviewerIds.includes(viewerId) ? 'requested' : 'team'
    const label = display.grouping === 'focus' ? focusLabels[id] : display.grouping === 'status' ? reviewStatusLabels[id] : display.grouping === 'author' ? review.author.displayName : id
    const group = groups.get(id) ?? { id, label, items: [] }
    group.items.push(review); groups.set(id, group)
  }
  const order: readonly string[] = display.grouping === 'focus' ? Object.keys(focusLabels) : reviewStatuses
  return [...groups.values()].sort((a, b) => display.grouping === 'focus' || display.grouping === 'status' ? order.indexOf(a.id) - order.indexOf(b.id)
    : display.grouping === 'author' && (a.id === viewerId || b.id === viewerId) ? a.id === viewerId ? -1 : 1 : a.label.localeCompare(b.label))
}
export function parseReviewFilters(raw: string | null): ReviewFilter[] {
  try { const items = JSON.parse(raw ?? '[]'); if (!Array.isArray(items)) return []
    const seen = new Set<string>()
    return items.flatMap(item => {
      if (!item || !Object.hasOwn(reviewFieldLabels, item.id) || !Array.isArray(item.values) || seen.has(item.id)) return []
      seen.add(item.id)
      const values = item.values.filter((value: { id?: unknown; label?: unknown }) => value && typeof value.id === 'string' && typeof value.label === 'string')
      return values.length ? [{ id: item.id, fieldLabel: reviewFieldLabels[item.id as ReviewFilterField], operator: item.operator === 'isNot' ? 'isNot' : 'is', values }] : []
    })
  } catch { return [] }
}
export function parseReviewDisplay(raw: string | null, view: ReviewView): ReviewDisplay {
  const fallback = defaultReviewDisplay(view)
  try {
    const saved = JSON.parse(raw ?? 'null'); if (!saved) return fallback
    const grouping: ReviewGrouping = ['none', 'status', 'author', 'repository', ...(view === 'for-you' ? ['focus'] : [])].includes(saved.grouping) ? saved.grouping : fallback.grouping
    const ordering: ReviewOrdering = ['name', 'author', 'status', 'opened', 'updated', ...(grouping === 'focus' ? ['importance'] : [])].includes(saved.ordering) ? saved.ordering : grouping === 'focus' ? 'importance' : 'opened'
    return { ...fallback, grouping, ordering, descending: ordering !== 'importance' && (typeof saved.descending === 'boolean' ? saved.descending : fallback.descending), closed: ['all', 'day', 'week', 'month', 'none'].includes(saved.closed) ? saved.closed : fallback.closed,
      showDrafts: typeof saved.showDrafts === 'boolean' ? saved.showDrafts : fallback.showDrafts,
      showTeams: typeof saved.showTeams === 'boolean' ? saved.showTeams : fallback.showTeams,
      properties: Array.isArray(saved.properties) ? saved.properties.filter((id: ReviewProperty) => reviewPropertyOptions.some(option => option.id === id)) : fallback.properties }
  } catch { return fallback }
}

/** Compact age used by review rows: 49s, 5m, 16h, 3d, 2w, 4mo, 1y. */
export function reviewShortAge(value: string, now = Date.now()) {
  const seconds = Math.max(0, Math.floor((now - Date.parse(value)) / 1000))
  if (!Number.isFinite(seconds)) return ''
  if (seconds < 60) return `${Math.max(1, seconds)}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d`
  if (days < 30) return `${Math.floor(days / 7)}w`
  if (days < 365) return `${Math.floor(days / 30)}mo`
  return `${Math.floor(days / 365)}y`
}
/** Splits a pull request title into text and `inline code` segments. */
export function reviewTitleSegments(title: string) {
  const segments: { code: boolean; text: string }[] = []
  title.split(/(`[^`]+`)/).forEach(part => { if (part) segments.push(part.length > 2 && part.startsWith('`') && part.endsWith('`') ? { code: true, text: part.slice(1, -1) } : { code: false, text: part }) })
  return segments
}
/** Pull request state shown by the row and header icons. */
export function reviewVisualState(review: CodeReview): 'draft' | 'open' | 'merged' | 'closed' {
  if (review.status === 'merged') return 'merged'
  if (review.status === 'closed') return 'closed'
  return review.draft ? 'draft' : 'open'
}

/** Activity and comment times: "just now", "1min ago", "16h ago", "3d ago". */
export function reviewTimeAgo(value: string, now = Date.now()) {
  const seconds = Math.floor((now - Date.parse(value)) / 1000)
  if (!Number.isFinite(seconds)) return ''
  if (seconds < 60) return 'just now'
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}min ago`
  return `${reviewShortAge(value, now)} ago`
}
/** An open pull request the viewer authored that can merge now (not a draft, checks green, no conflicts or requested changes). */
export function reviewReadyToMerge(review: CodeReview) {
  return !review.draft && (review.status === 'open' || review.status === 'approved') && review.branchState !== 'conflicted'
    && review.checks.every(check => check.status === 'passed' || check.status === 'skipped')
}
