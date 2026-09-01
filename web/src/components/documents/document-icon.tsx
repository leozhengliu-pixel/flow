import type { ComponentProps } from 'react'

import { ViewGlyph, ViewIconPicker, type ViewVisual } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument } from '@/types/flow'

export const DEFAULT_DOCUMENT_ICON = 'Page'
export const DEFAULT_DOCUMENT_COLOR = '#8b8b90'

type DocumentVisual = Pick<FlowDocument, 'icon' | 'color'>

export function DocumentGlyph({ className, color, document, icon }: {
  className?: string
  color?: string
  document?: DocumentVisual
  icon?: string
}) {
  return <ViewGlyph
    className={className}
    color={color || document?.color || DEFAULT_DOCUMENT_COLOR}
    icon={icon || document?.icon || DEFAULT_DOCUMENT_ICON}
  />
}

export function DocumentIconPicker({ document, ...props }: {
  document: DocumentVisual
  onChange: (visual: ViewVisual) => void
} & Omit<ComponentProps<typeof ViewIconPicker>, 'color' | 'icon'>) {
  const { t } = useI18n()
  return <ViewIconPicker
    {...props}
    ariaLabel={props.ariaLabel ?? t('Document icon')}
    color={document.color || DEFAULT_DOCUMENT_COLOR}
    icon={document.icon || DEFAULT_DOCUMENT_ICON}
  />
}
