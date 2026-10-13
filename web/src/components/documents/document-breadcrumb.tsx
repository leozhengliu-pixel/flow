/**
 * Header breadcrumb, measured against the reference app:
 * - project / initiative document: `[page glyph in the parent's colour] Parent › [page glyph] title`;
 * - team document: `[page glyph in the team colour] Team › Documents › [page glyph] title`;
 * - issue / origin fallbacks keep a plain document glyph in front of their label.
 * A parent crumb is a link (hover background, hover card for projects, initiatives and teams); the title
 * crumb is plain text. The document's single parent comes from documentParent().
 */
import { FileText } from 'lucide-react'
import type { ReactElement, ReactNode } from 'react'

import { AgentEntityHover } from '@/components/agent/agent-entity-hover'
import { AppLink } from '@/components/ui/app-link'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { initiativePath, issuePath, projectPath, teamDocumentsPath, teamHomePath } from '@/lib/app-routes'
import type { BootstrapData, FlowDocument } from '@/types/flow'
import { documentDisplayTitle, documentParent } from './document-actions'
import { DEFAULT_DOCUMENT_COLOR, DocumentGlyph } from './document-icon'

export interface DocumentOrigin { label: string; entity?: boolean }

/** The reference draws every parent with the page glyph in the parent's own colour (not the parent's icon). */
function ParentGlyph({ color }: { color?: string }) {
  return <span aria-hidden="true" className="document-breadcrumb-glyph"><ViewGlyph color={color} icon="Page"/></span>
}

/** An untouched document icon takes the header's dim glyph colour; a chosen colour is kept. */
function documentCrumbColor(document: FlowDocument) {
  const color = document.color?.trim().toLowerCase()
  return !color || color === DEFAULT_DOCUMENT_COLOR ? 'var(--issue-header-icon)' : document.color
}

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
  const hover = (link: ReactElement, entity: Parameters<typeof AgentEntityHover>[0]['entity']) => <AgentEntityHover data={data} entity={entity} side="bottom" sideOffset={8} alignOffset={4}>{link}</AgentEntityHover>
  let crumbs: ReactNode
  if (parent?.type === 'team') {
    crumbs = <>
      {hover(<AppLink className="document-breadcrumb-link document-breadcrumb-parent document-breadcrumb-team" href={teamHomePath(workspace, parent.team.key)}><ParentGlyph color={parent.team.color}/><span data-i18n-ignore>{parent.team.name}</span></AppLink>, { kind: 'team', team: parent.team })}
      {separator}
      <AppLink className="document-breadcrumb-link document-breadcrumb-documents" href={teamDocumentsPath(workspace, parent.team.key)}><span>{documentsLabel}</span></AppLink>
    </>
  } else if (parent?.type === 'project') {
    crumbs = hover(<AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={projectPath(workspace, parent.project)}><ParentGlyph color={parent.project.color}/><span data-i18n-ignore>{parent.project.name}</span></AppLink>, { kind: 'project', project: parent.project })
  } else if (parent?.type === 'initiative') {
    crumbs = hover(<AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={initiativePath(workspace, parent.initiative)}><ParentGlyph color={parent.initiative.color}/><span data-i18n-ignore>{parent.initiative.name}</span></AppLink>, { kind: 'initiative', initiative: parent.initiative })
  } else if (issue) {
    crumbs = <AppLink className="document-breadcrumb-link document-breadcrumb-parent" href={issuePath(workspace, issue)}><FileText aria-hidden="true"/><span data-i18n-ignore>{issue.identifier}</span></AppLink>
  } else if (origin) {
    crumbs = <button className="document-breadcrumb-link document-breadcrumb-parent" onClick={onBack} type="button"><FileText aria-hidden="true"/><span data-i18n-ignore={origin.entity || undefined}>{origin.label}</span></button>
  } else {
    crumbs = <button className="document-breadcrumb-link document-breadcrumb-parent" onClick={onBack} type="button"><FileText aria-hidden="true"/><span>{documentsLabel}</span></button>
  }
  return <nav aria-label={t('Document breadcrumb')} className="document-breadcrumbs">
    {crumbs}
    {separator}
    <strong className="document-breadcrumb-current"><span aria-hidden="true" className="document-breadcrumb-glyph is-document"><DocumentGlyph color={documentCrumbColor(document)} document={document}/></span><span data-i18n-ignore>{documentDisplayTitle(document, t)}</span></strong>
  </nav>
}
