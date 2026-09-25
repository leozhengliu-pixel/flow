import { useMemo, useState } from 'react'
import { Link2 } from 'lucide-react'
import { toast } from 'sonner'
import { fetchIssueRecord, updateIssue } from '@/lib/api'
import { useIssueCandidates } from '@/components/issue/use-issue-candidates'
import { StatusIcon } from '@/components/issue/issue-icons'
import { PropertyMenu } from '@/components/property/property-menu'
import type { BootstrapData, Issue } from '@/types/flow'

/** "Link existing issue as sub-issue…": pick any issue and make it a child of `parent`. */
export function LinkExistingSubIssue({ parent, data, onIssueUpdated, label = false }: { parent: Issue; data: BootstrapData; onIssueUpdated?: (issue: Issue) => void; label?: boolean }) {
  const [open, setOpen] = useState(false)
  const candidates = useIssueCandidates(data, open)
  const ancestors = useMemo(() => { const ids = new Set<string>([parent.id]); let cursor = parent.parentId; while (cursor && !ids.has(cursor)) { ids.add(cursor); cursor = data.issues.find(item => item.id === cursor)?.parentId } return ids }, [data.issues, parent.id, parent.parentId])
  const options = useMemo(() => candidates.filter(item => !ancestors.has(item.id) && item.parentId !== parent.id).map(item => ({
    id: item.id,
    label: `${item.identifier} ${item.title}`,
    labelContent: <><span className="link-sub-issue-identifier">{item.identifier}</span>{item.title}</>,
    icon: <StatusIcon state={item.state} size={14}/>,
    i18nIgnore: true,
  })), [ancestors, candidates, parent.id])
  const link = async (id: string) => {
    try {
      const child = await updateIssue(id, { parentId: parent.id })
      onIssueUpdated?.(child)
      onIssueUpdated?.(await fetchIssueRecord(parent.id, undefined, data.workspace.urlKey))
      toast.success(`${child.identifier} is now a sub-issue`)
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Could not link sub-issue') }
  }
  return <PropertyMenu
    label="Link existing issue"
    ariaLabel="Link existing issue as sub-issue"
    value=""
    triggerRole="button"
    triggerClassName={label ? 'issue-empty-sub-issue-action link-existing-sub-issue' : 'sub-issues-create'}
    trigger={<><Link2 size={label ? 16 : 14}/>{label ? 'Link existing issue' : null}</>}
    align="end"
    surfaceClassName="link-sub-issue-picker"
    searchPlaceholder="Link existing issue as sub-issue…"
    emptyLabel="No matching issues"
    options={options}
    open={open}
    onOpenChange={setOpen}
    onChange={id => { void link(id) }}
  />
}
