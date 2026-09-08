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
  return <Tooltip.Provider delayDuration={450} skipDelayDuration={300}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content data-flow-motion="tooltip" className="label-hover-preview" side={side} align={align} sideOffset={5} collisionPadding={8}>
          <LabelHoverPreviewContent label={label}/>
        </Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip.Root>
  </Tooltip.Provider>
}

export function LabelHoverPreviewContent({ label }: { label: LabelHoverPreviewData }) {
  const { t } = useI18n()
  const issueCount = label.issueCount
  const resource = label.resourceType === 'project' ? 'project' : label.resourceType === 'initiative' ? 'initiative' : 'issue'
  const issueCountLabel = issueCount === undefined ? undefined : t(`${issueCount} labeled ${resource}${issueCount === 1 ? '' : 's'}`)
  return <div className="label-hover-preview-inner">
            <div className="label-hover-preview-summary">
              <div className="label-hover-preview-title"><i style={{ backgroundColor: label.color }}/><strong data-i18n-ignore>{label.name}</strong></div>
              <span data-i18n-ignore={label.description?true:undefined}>{label.description || 'No description'}</span>
            </div>
            <div className="label-hover-preview-footer">
              <span>{issueCountLabel && <><Copy size={12}/>{issueCountLabel}</>}</span>
              <span><Building2 size={12}/>{label.scope || 'Workspace'}</span>
            </div>
          </div>
}
