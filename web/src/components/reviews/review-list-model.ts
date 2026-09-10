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
const focusLabels: Record<string, string> = { ready: 'Ready to merge', requested: 'Needs your review', team: 'Team reviews', yours: 'Your reviews', completed: 'Completed' }
export function groupReviews(reviews: CodeReview[], display: ReviewDisplay, viewerId: string) {
  const groups = new Map<string, { id: string; label: string; items: CodeReview[] }>()
  for (const review of reviews) {
    const id = display.grouping === 'none' ? 'all' : display.grouping === 'status' ? reviewStatus(review)
      : display.grouping === 'repository' ? reviewRepository(review) : display.grouping === 'author' ? review.author.id
      : ['merged', 'closed'].includes(review.status) ? 'completed'
      : review.author.id === viewerId ? reviewImportance(review) === 0 ? 'ready' : 'yours'
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
