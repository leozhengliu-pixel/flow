import * as Tooltip from '@radix-ui/react-tooltip'
import { Building2, Copy } from 'lucide-react'
import type { ReactElement } from 'react'
import { useI18n } from '@/i18n/i18n'
import type { LabelResourceType } from '@/types/flow'

export interface LabelHoverPreviewData {
  name: string
  color: string
  description?: string
  issueCount?: number
  scope?: string
  resourceType?: LabelResourceType
}

export function LabelHoverPreview({ label, children, side='left', align='start' }: { label: LabelHoverPreviewData; children: ReactElement; side?:'top'|'right'|'bottom'|'left'; align?:'start'|'center'|'end' }) {
  const { t } = useI18n()
  const issueCount = label.issueCount ?? 0
  const resource = label.resourceType === 'project' ? 'project' : label.resourceType === 'initiative' ? 'initiative' : 'issue'
  const issueCountLabel = t(`${issueCount} labeled ${resource}${issueCount === 1 ? '' : 's'}`)
  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="label-hover-preview" side={side} align={align} sideOffset={5} collisionPadding={8}>
          <div className="label-hover-preview-inner">
            <div className="label-hover-preview-summary">
              <div className="label-hover-preview-title"><i style={{ backgroundColor: label.color }}/><strong data-i18n-ignore>{label.name}</strong></div>
              <span data-i18n-ignore={label.description?true:undefined}>{label.description || 'No description'}</span>
            </div>
            <div className="label-hover-preview-footer">
              <span><Copy size={12}/>{issueCountLabel}</span>
              <span><Building2 size={12}/>{label.scope || 'Workspace'}</span>
            </div>
          </div>
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>
}
