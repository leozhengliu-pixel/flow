/**
 * LS-0249 EntityActivityHistoryGrouping
 * Collapse activity by actorKey + category + bucketKey, with multi-user / AI / workflow labels.
 */

export type ActivityActorPresentation =
  | { type: 'users'; userIds: string[]; names: string[] }
  | { type: 'label'; label: string }
  | { type: 'source'; sourceType: string; sourceId?: string }

export type ActivityActor = {
  key?: string
  presentation: ActivityActorPresentation
}

export type ActivityHistoryCategory =
  | 'comment'
  | 'update'
  | 'property'
  | 'status'
  | 'assignment'
  | 'system'
  | 'other'

export type ActivityGrouping = {
  actorKey: string
  category: ActivityHistoryCategory
  bucketKey: string
  summaryActor: string
  summaryItem?: string
}

export type GroupableActivityItem = {
  id: string
  createdAt: string | Date
  category: ActivityHistoryCategory
  userIds?: string[]
  userNames?: string[]
  performedByLinearAi?: boolean
  sourceMetadata?: {
    type?: string
    id?: string
    externalUserId?: string
    externalUserName?: string
    releaseAutomationMetadata?: unknown
    issueSuggestionAutomationMetadata?: unknown
  }
  fallbackActor?: 'linear' | 'unknown'
  summaryItem?: string
}

/** Hour bucket (UTC) — matches Linear Ok(createdAt) style grouping window. */
export function activityBucketKey(createdAt: string | Date): string {
  const date = createdAt instanceof Date ? createdAt : new Date(createdAt)
  if (!Number.isFinite(date.getTime())) return 'unknown'
  const year = date.getUTCFullYear()
  const month = String(date.getUTCMonth() + 1).padStart(2, '0')
  const day = String(date.getUTCDate()).padStart(2, '0')
  const hour = String(date.getUTCHours()).padStart(2, '0')
  return `${year}-${month}-${day}T${hour}`
}

export function resolveActivityActor(input: {
  userIds?: string[]
  userNames?: string[]
  performedByLinearAi?: boolean
  sourceMetadata?: GroupableActivityItem['sourceMetadata']
  fallbackActor?: 'linear' | 'unknown'
}): ActivityActor | undefined {
  if (input.performedByLinearAi) return linearActor()

  const userIds = (input.userIds ?? []).filter(Boolean)
  const names = input.userNames ?? []
  if (userIds.length > 0) {
    const sorted = [...userIds].sort()
    return {
      key: sorted.length === 1 ? `user:${sorted[0]}` : `users:${sorted.join(',')}`,
      presentation: { type: 'users', userIds: sorted, names },
    }
  }

  const meta = input.sourceMetadata
  if (meta?.externalUserId) {
    return {
      key: `external-user:${meta.externalUserId}`,
      presentation: {
        type: 'users',
        userIds: [meta.externalUserId],
        names: meta.externalUserName ? [meta.externalUserName] : ['External user'],
      },
    }
  }

  if (!meta) {
    if (input.fallbackActor === 'linear') return linearActor()
    if (input.fallbackActor === 'unknown') return unknownActor()
    return undefined
  }

  if (meta.type === 'workflow') {
    if (meta.releaseAutomationMetadata) return linearActor()
    if (meta.issueSuggestionAutomationMetadata) {
      return {
        key: 'product-intelligence',
        presentation: { type: 'label', label: 'Product intelligence' },
      }
    }
    const workflowId = meta.id
    return {
      key: workflowId ? `workflow:${workflowId}` : 'workflow',
      presentation: { type: 'label', label: 'Workflow' },
    }
  }

  return {
    key: meta.id ? `source:${meta.type ?? 'unknown'}:${meta.id}` : meta.type ? `source:${meta.type}` : undefined,
    presentation: { type: 'source', sourceType: meta.type ?? 'source', sourceId: meta.id },
  }
}

function linearActor(): ActivityActor {
  return { key: 'linear', presentation: { type: 'label', label: 'Linear' } }
}

function unknownActor(): ActivityActor {
  return { presentation: { type: 'label', label: 'Unknown' } }
}

export function summarizeActor(actor: ActivityActor): string {
  const presentation = actor.presentation
  if (presentation.type === 'label') return presentation.label
  if (presentation.type === 'source') {
    if (presentation.sourceType === 'workflow') return 'Workflow'
    return presentation.sourceType
  }
  if (presentation.names.length > 2 || presentation.userIds.length > 2) {
    return `${Math.max(presentation.names.length, presentation.userIds.length)} users`
  }
  if (presentation.names.length === 2) return `${presentation.names[0]} and ${presentation.names[1]}`
  if (presentation.names.length === 1) return presentation.names[0]
  if (presentation.userIds.length === 1) return 'User'
  if (presentation.userIds.length === 2) return '2 users'
  return 'Users'
}

export function buildActivityGrouping(input: GroupableActivityItem): ActivityGrouping | undefined {
  const actor = resolveActivityActor(input)
  if (!actor?.key) return undefined
  return {
    actorKey: actor.key,
    category: input.category,
    bucketKey: activityBucketKey(input.createdAt),
    summaryActor: summarizeActor(actor),
    summaryItem: input.summaryItem,
  }
}

export type ActivityHistoryGroup<T extends GroupableActivityItem = GroupableActivityItem> = {
  key: string
  grouping: ActivityGrouping
  items: T[]
}

export function groupActivityHistory<T extends GroupableActivityItem>(
  items: T[],
  options?: { entityId?: string },
): ActivityHistoryGroup<T>[] {
  const groups = new Map<string, ActivityHistoryGroup<T>>()
  for (const item of items) {
    const grouping = buildActivityGrouping(item)
    if (!grouping) {
      const soloKey = `solo:${item.id}`
      groups.set(soloKey, {
        key: soloKey,
        grouping: {
          actorKey: 'unknown',
          category: item.category,
          bucketKey: activityBucketKey(item.createdAt),
          summaryActor: 'Unknown',
          summaryItem: item.summaryItem,
        },
        items: [item],
      })
      continue
    }
    const key = options?.entityId
      ? `entity-activity-group:${options.entityId}:${grouping.category}:${grouping.actorKey}:${grouping.bucketKey}`
      : `${grouping.category}:${grouping.actorKey}:${grouping.bucketKey}`
    const existing = groups.get(key)
    if (existing) existing.items.push(item)
    else groups.set(key, { key, grouping, items: [item] })
  }
  return [...groups.values()].map(group => ({
    ...group,
    items: [...group.items].sort((a, b) => +new Date(b.createdAt) - +new Date(a.createdAt)),
  })).sort((a, b) => +new Date(b.items[0]?.createdAt ?? 0) - +new Date(a.items[0]?.createdAt ?? 0))
}

export function actorBadgeLabel(actorKey: string, summaryActor: string): string {
  if (actorKey === 'linear' || actorKey.startsWith('linear')) return 'AI'
  if (actorKey === 'product-intelligence') return 'AI'
  if (actorKey.startsWith('workflow:')) return 'Workflow'
  if (actorKey.startsWith('users:')) return summaryActor
  return summaryActor
}
