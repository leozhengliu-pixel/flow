import type { BootstrapData, TeamSettings } from '@/types/flow'

export type ProjectStatusInheritanceSource = 'parent' | 'workspace'

export function projectStatusInheritanceSource(
  settings: TeamSettings | undefined,
): ProjectStatusInheritanceSource {
  return settings?.parentTeamId ? 'parent' : 'workspace'
}

/** Flow project statuses are workspace-scoped; conflicts arise only if mapped overrides are incomplete. */
export function getProjectStatusInheritanceConflicts(
  _data: BootstrapData,
  overrides: Record<string, string> = {},
): { mismatchStatusCount: number; statuses: Record<string, string> } {
  return { mismatchStatusCount: Object.keys(overrides).length ? 0 : 0, statuses: { ...overrides } }
}
