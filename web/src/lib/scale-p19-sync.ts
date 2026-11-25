import { modelRecordKind } from '@/lib/scale-p13-wire'
import type { SyncChanges } from '@/types/flow'

const coreKinds = new Set(['userSettings', 'workspaceSettings', 'settings', 'teamSettings', 'cycleSettings', 'user', 'team', 'label', 'labelGroups', 'state', 'cycle', 'favorite', 'favoriteFolders', 'savedView', 'subscriptions', 'projectStatuses', 'projectTemplates', 'customers', 'customerStatuses', 'customerTiers', 'agentSkills'])
const windowKinds: Record<string, string> = { projectUpdates: 'project', initiativeUpdates: 'initiative', projectRelations: 'project', initiativeRelations: 'initiative' }

export function scaleP19NeedsCore(page: SyncChanges) {
  return page.transactions.some(transaction => [...transaction.upserts, ...transaction.removals].some(row => coreKinds.has(modelRecordKind(row.key.kind)) || modelRecordKind(row.key.kind) === 'initiative') || transaction.invalidations.some(kind => coreKinds.has(modelRecordKind(kind)) || modelRecordKind(kind) === 'initiative'))
}

/** Histories stay on their open preview's bounded endpoint, not in entity state. */
export function scaleP19WindowChanges(page: SyncChanges): SyncChanges {
  return { ...page, transactions: page.transactions.map(transaction => {
    const invalidations = new Set<string>()
    const retain = (kind: string) => {
      kind = modelRecordKind(kind)
      if (windowKinds[kind]) { invalidations.add(windowKinds[kind]); return false }
      return !coreKinds.has(kind)
    }
    const upserts = transaction.upserts.filter(row => retain(row.key.kind))
    const removals = transaction.removals.filter(row => retain(row.key.kind))
    for (const kind of transaction.invalidations) if (retain(kind)) invalidations.add(kind)
    return { ...transaction, upserts, removals, invalidations: [...invalidations] }
  }) }
}
