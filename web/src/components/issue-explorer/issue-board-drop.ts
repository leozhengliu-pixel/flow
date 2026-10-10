import type { MyIssuesGroupData, MyIssuesRowData } from '@/components/my-issues/my-issues-list'

function isTriageGroup(group?: MyIssuesGroupData) {
  if (!group) return false
  const needle = `${group.id} ${group.label}`.toLowerCase()
  return needle.includes('triage')
}

/** Cheap canDrop gate — triage leave / priority required. Returns reject reason or null. */
export function boardDropRejectReason(issue: MyIssuesRowData, sourceGroup: MyIssuesGroupData | undefined, targetGroup: MyIssuesGroupData | undefined): string | null {
  if (!sourceGroup || !targetGroup) return null
  if (isTriageGroup(sourceGroup) && !isTriageGroup(targetGroup) && issue.priority === 0) {
    return "Can't move out of triage without a priority"
  }
  return null
}
