/**
 * Header breadcrumb: `Team › Documents › [icon] title` (or project / initiative /
 * issue › title) — the document's single parent comes from documentParent().
 * Without a parent the page falls back to the navigation `origin` (where the
 * viewer came from) or a plain "Documents" crumb that goes back.
 */
import { FileText } from 'lucide-react'
import type { ReactNode } from 'react'

import { TeamIcon } from '@/components/issue/issue-icons'
import { AppLink } from '@/components/ui/app-link'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { normalizeProjectIcon } from '@/components/views/project-icon'
import { useI18n } from '@/i18n/i18n'
import { initiativePath, issuePath, projectPath, teamDocumentsPath, teamHomePath } from '@/lib/app-routes'
import type { BootstrapData, FlowDocument } from '@/types/flow'
import { documentDisplayTitle, documentParent } from './document-actions'
import { DocumentGlyph } from './document-icon'

export interface DocumentOrigin { label: string; entity?: boolean }

export function DocumentBreadcrumb({ data, document, origin, onBack }: {
  data: BootstrapData
  document: FlowDocument
  origin?: DocumentOrigin
  onBack: () => void
}) {
  const { t } = useI18n()
  const workspace = data.workspace.urlKey
  const parent = documentParent(data, document)
  const separator = <span aria-hidden="true" className="document-breadcrumb-separator">›</span>
  const documentsLabel = t('Documents')
  const issue = parent?.type === 'issue' ? data.issues.find(item => item.id === parent.id) : undefined
  let crumbs: ReactNode
  if (parent?.type === 'team') {
    crumbs = <>
      <AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={teamHomePath(workspace, parent.team.key)}><TeamIcon team={parent.team} size={16}/><span data-i18n-ignore>{parent.team.name}</span></AppLink>
      {separator}
      <AppLink className="document-breadcrumb-link document-breadcrumb-documents" href={teamDocumentsPath(workspace, parent.team.key)}>{documentsLabel}</AppLink>
    </>
  } else if (parent?.type === 'project') {
    crumbs = <AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={projectPath(workspace, parent.project)}><ViewGlyph color={parent.project.color} icon={normalizeProjectIcon(parent.project.icon)}/><span data-i18n-ignore>{parent.project.name}</span></AppLink>
  } else if (parent?.type === 'initiative') {
    crumbs = <AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={initiativePath(workspace, parent.initiative)}><ViewGlyph color={parent.initiative.color} icon={parent.initiative.icon || 'Initiative'}/><span data-i18n-ignore>{parent.initiative.name}</span></AppLink>
  } else if (issue) {
    crumbs = <AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={issuePath(workspace, issue)}><FileText/><span data-i18n-ignore>{issue.identifier}</span></AppLink>
  } else if (origin) {
    crumbs = <button className="document-breadcrumb-link document-breadcrumb-parent" onClick={onBack} type="button"><FileText/><span data-i18n-ignore={origin.entity || undefined}>{origin.label}</span></button>
  } else {
    crumbs = <button className="document-breadcrumb-link document-breadcrumb-parent" onClick={onBack} type="button"><FileText/><span>{documentsLabel}</span></button>
  }
  return <nav aria-label={t('Document breadcrumb')} className="document-breadcrumbs">
    {crumbs}
    {separator}
    <strong className="document-breadcrumb-current"><DocumentGlyph document={document}/><span data-i18n-ignore>{documentDisplayTitle(document, t)}</span></strong>
  </nav>
}
