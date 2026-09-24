import { useMemo, useState } from 'react'
import * as Popover from '@radix-ui/react-popover'
import { Link2 } from 'lucide-react'
import { toast } from 'sonner'
import { fetchIssueRecord, updateIssue } from '@/lib/api'
import { usePropertyCommand } from '@/components/property/use-property-command'
import type { BootstrapData, Issue } from '@/types/flow'

/** Linear "Link existing issue as sub-issue…": pick any issue and make it a child of `parent`. */
export function LinkExistingSubIssue({ parent, data, onIssueUpdated, label = false }: { parent: Issue; data: BootstrapData; onIssueUpdated?: (issue: Issue) => void; label?: boolean }) {
  const [open, setOpen] = useState(false)
  const ancestors = useMemo(() => { const ids = new Set<string>([parent.id]); let cursor = parent.parentId; while (cursor && !ids.has(cursor)) { ids.add(cursor); cursor = data.issues.find(item => item.id === cursor)?.parentId } return ids }, [data.issues, parent.id, parent.parentId])
  const options = useMemo(() => data.issues.filter(item => !ancestors.has(item.id) && item.parentId !== parent.id && !item.archivedAt).map(item => ({ id: item.id, label: `${item.identifier} ${item.title}` })), [ancestors, data.issues, parent.id])
  const link = async (id: string) => {
    try {
      const child = await updateIssue(id, { parentId: parent.id })
      onIssueUpdated?.(child)
      onIssueUpdated?.(await fetchIssueRecord(parent.id, undefined, data.workspace.urlKey))
      toast.success(`${child.identifier} is now a sub-issue`)
    } catch (error) { toast.error(error instanceof Error ? error.message : 'Could not link sub-issue') }
  }
  const command = usePropertyCommand({ open, options, onOpenChange: setOpen, onSelect: option => link(option.id) })
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button type="button" className={label ? 'issue-empty-sub-issue-action link-existing-sub-issue' : 'sub-issues-create'} aria-label="Link existing issue as sub-issue"><Link2 size={label ? 16 : 14}/>{label ? 'Link existing issue' : null}</button></Popover.Trigger>
    <Popover.Portal><Popover.Content data-flow-motion="floating" className="link-sub-issue-menu" align="end" sideOffset={4} collisionPadding={10} onKeyDown={command.onKeyDown}>
      <input ref={command.inputRef} autoFocus aria-label="Search issues" placeholder="Link existing issue as sub-issue…" value={command.query} onChange={event => command.onQueryChange(event.target.value)}/>
      <div role="listbox">{command.filteredOptions.slice(0, 10).map(option => <button key={option.id} type="button" role="option" aria-selected={command.activeId === option.id} onPointerMove={() => command.setActiveId(option.id)} onClick={() => command.choose(option)} data-i18n-ignore>{option.label}</button>)}</div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}
