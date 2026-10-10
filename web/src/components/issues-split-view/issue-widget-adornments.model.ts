import type { CodeReview } from '@/types/flow'

export type PullRequestLifecycle = 'open' | 'inReview' | 'approved' | 'merged' | 'closed' | 'draft'

export interface IssueWidgetAdornmentsModel {
  pullRequestLifecycle?: PullRequestLifecycle
  pullRequestCount: number
  blockedByCount: number
  blockingCount: number
}

/** Prefer merged > closed > approved > inReview > open > draft for row chrome. */
export function resolvePullRequestLifecycle(
  reviews: Array<Pick<CodeReview, 'status' | 'draft'>>,
): PullRequestLifecycle | undefined {
  if (!reviews.length) return undefined
  const statuses = reviews.map((review) =>
    review.draft && review.status === 'open' ? ('draft' as const) : review.status,
  )
  const rank: PullRequestLifecycle[] = ['merged', 'closed', 'approved', 'inReview', 'open', 'draft']
  for (const status of rank) {
    if (statuses.includes(status)) return status
  }
  return statuses[0]
}

export function countRelationTypes(relationTypes: string[] | undefined, type: string) {
  return relationTypes?.filter((value) => value === type).length ?? 0
}

export function buildIssueWidgetAdornments(input: {
  reviews?: Array<Pick<CodeReview, 'status' | 'draft'>>
  relationTypes?: string[]
  pullRequestCount?: number
}): IssueWidgetAdornmentsModel {
  const reviews = input.reviews ?? []
  return {
    pullRequestLifecycle: resolvePullRequestLifecycle(reviews),
    pullRequestCount: input.pullRequestCount ?? reviews.length,
    blockedByCount: countRelationTypes(input.relationTypes, 'blocked_by'),
    blockingCount: countRelationTypes(input.relationTypes, 'blocks'),
  }
}
