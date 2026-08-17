import { useEffect, useMemo, useRef, useState } from 'react'
import type { MyIssuesGroupData, MyIssuesRowData } from './my-issues-list'

export interface MyIssuesSelectionState {
  selectedIds: ReadonlySet<string>
  selectedIssues: MyIssuesRowData[]
  previewIssue?: MyIssuesRowData
  previewIssueId?: string
  clearSelection: () => void
  closePreview: () => void
  openPreview: (issue: MyIssuesRowData) => void
  selectIssue: (issueId: string, selected: boolean, range: boolean) => void
  setSelectedIds: (ids: Iterable<string>) => void
}

export function useMyIssuesSelection(groups: MyIssuesGroupData[], pruneHidden = true): MyIssuesSelectionState {
  const visibleIssues = useMemo(() => groups.flatMap(group => group.issues), [groups])
  const visibleIds = useMemo(() => new Set(visibleIssues.map(issue => issue.id)), [visibleIssues])
  const [selectedIds, updateSelectedIds] = useState<Set<string>>(() => new Set())
  const [previewIssueId, setPreviewIssueId] = useState<string | undefined>(undefined)
  const rangeAnchor = useRef<string | undefined>(undefined)

  useEffect(() => {
    if (!pruneHidden) return
    updateSelectedIds(current => {
      const next = new Set([...current].filter(id => visibleIds.has(id)))
      return sameSet(current, next) ? current : next
    })
    setPreviewIssueId(current => current && visibleIds.has(current) ? current : undefined)
  }, [pruneHidden, visibleIds])

  const selectIssue = (issueId: string, selected: boolean, range: boolean) => {
    updateSelectedIds(current => {
      const next = new Set(current)
      if (range && rangeAnchor.current) {
        const anchorIndex = visibleIssues.findIndex(issue => issue.id === rangeAnchor.current)
        const issueIndex = visibleIssues.findIndex(issue => issue.id === issueId)
        if (anchorIndex >= 0 && issueIndex >= 0) {
          const [start, end] = anchorIndex < issueIndex ? [anchorIndex, issueIndex] : [issueIndex, anchorIndex]
          for (const issue of visibleIssues.slice(start, end + 1)) {
            if (selected) next.add(issue.id)
            else next.delete(issue.id)
          }
        }
      } else if (selected) {
        next.add(issueId)
      } else {
        next.delete(issueId)
      }
      return next
    })
    rangeAnchor.current = issueId
  }

  return {
    selectedIds,
    selectedIssues: visibleIssues.filter(issue => selectedIds.has(issue.id)),
    previewIssue: visibleIssues.find(issue => issue.id === previewIssueId),
    previewIssueId,
    clearSelection: () => { updateSelectedIds(new Set()); rangeAnchor.current = undefined },
    closePreview: () => setPreviewIssueId(undefined),
    openPreview: issue => setPreviewIssueId(issue.id),
    selectIssue,
    setSelectedIds: ids => updateSelectedIds(new Set(ids)),
  }
}

function sameSet(a: ReadonlySet<string>, b: ReadonlySet<string>) { return a.size === b.size && [...a].every(value => b.has(value)) }
