/**
 * The "Template" chip shown beside the icon of a new, empty document. It lists
 * the workspace templates and those of the document's teams; picking one sets
 * the document's title, icon and body (the page re-keys its editor on reload).
 */
import { ChevronDown } from 'lucide-react'
import { useMemo } from 'react'

import { PropertyMenu } from '@/components/property/property-menu'
import { useI18n } from '@/i18n/i18n'
import type { FlowDocument } from '@/types/flow'
import { applyTemplateToDocument, type DocumentActionContext } from './document-actions'
import { documentTemplateOptions, isNewEmptyDocument } from './document-template-options'
import './document-template-chip.css'

export interface DocumentTemplateChipProps {
  ctx: DocumentActionContext
  document: FlowDocument
  className?: string
}

export function DocumentTemplateChip({ ctx, document, className }: DocumentTemplateChipProps) {
  const { t } = useI18n()
  const { options, byId } = useMemo(() => documentTemplateOptions(ctx, document), [ctx, document])
  if (!isNewEmptyDocument(document)) return null
  return <PropertyMenu
    label="Template"
    ariaLabel={t('Choose a template')}
    searchPlaceholder={t('Apply template…')}
    emptyLabel="No templates"
    options={options}
    selectedId=""
    trigger={<><span>{t('Template')}</span><ChevronDown size={12} aria-hidden="true"/></>}
    triggerClassName={`document-template-chip-trigger${className ? ` ${className}` : ''}`}
    triggerRole="button"
    closeOnSelect
    onChange={id => { const template = byId.get(id); if (template) return applyTemplateToDocument(ctx, document, template) }}
  />
}
