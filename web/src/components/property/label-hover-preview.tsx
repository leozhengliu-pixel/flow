import * as Tooltip from '@radix-ui/react-tooltip'
import { Building2, Copy } from 'lucide-react'
import type { ReactElement } from 'react'

export interface LabelHoverPreviewData {
  name: string
  color: string
  description?: string
  issueCount?: number
  scope?: string
}

export function LabelHoverPreview({ label, children }: { label: LabelHoverPreviewData; children: ReactElement }) {
  const issueCount = label.issueCount ?? 0
  const issueCountLabel = `${issueCount} labeled issue${issueCount === 1 ? '' : 's'}`
  return <Tooltip.Provider delayDuration={500} skipDelayDuration={0}>
    <Tooltip.Root>
      <Tooltip.Trigger asChild>{children}</Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content className="label-hover-preview" side="left" align="start" sideOffset={5} collisionPadding={8}>
          <div className="label-hover-preview-inner">
            <div className="label-hover-preview-summary">
              <div className="label-hover-preview-title"><i style={{ backgroundColor: label.color }}/><strong>{label.name}</strong></div>
              <span>{label.description || 'No description'}</span>
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
