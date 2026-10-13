import { RotateCcw } from 'lucide-react'
import { IssueActionGlyph } from '@/components/issue/issue-action-glyphs'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { FlowMenuItem, FlowSubmenu } from '@/components/ui/row-context-menu'
import { useI18n } from '@/i18n/i18n'
import { toast } from '@/lib/toast'
import type { BootstrapData, TrashEntry } from '@/types/flow'
import { trashCopyItems, trashRestoreLabel } from './trash-row-menu-model'

/**
 * The menu of a row in a "Recently deleted" list: Copy ▸ and Restore. Deleted items have no
 * permanent-delete action in the app; they are purged automatically 30 days after deletion.
 */
export function TrashRowMenuItems({ entry, data, onRestore }: { entry: TrashEntry; data: BootstrapData; onRestore: () => void | Promise<void> }) {
  const { t } = useI18n()
  const copy = (value: string) => void navigator.clipboard?.writeText(value).then(() => toast.success(t('Copied to clipboard')), () => toast.error(t('Could not copy to clipboard')))
  const items = entry.resourceType === 'issue' || entry.resourceType === 'project' || entry.resourceType === 'initiative' ? trashCopyItems(entry, data) : []
  return <>
    {items.length > 0 && <>
      <FlowSubmenu icon={<FlowGlyph name="copy"/>} label="Copy">
        {items.map(item => <FlowMenuItem key={item.label} icon={<IssueActionGlyph label={item.glyph} fallback={null}/>} label={item.label} shortcut={item.shortcut} onSelect={() => copy(item.value())}/>)}
      </FlowSubmenu>
    </>}
    <FlowMenuItem icon={<RotateCcw size={16}/>} label={trashRestoreLabel(entry.resourceType)} onSelect={() => void onRestore()}/>
  </>
}
