/** Which templates a document can start from, and whether it is still a blank new document. */
import { Building2 } from 'lucide-react'

import { TeamIcon } from '@/components/issue/issue-icons'
import type { PropertyOption } from '@/components/property/property-menu'
import type { DocumentTemplate, FlowDocument, Team } from '@/types/flow'
import { viewerTeams, type DocumentActionContext } from './document-actions'

interface EmptyDocumentShape { title: string; content?: string; contentData?: Record<string, unknown> }

/** A document nobody has typed into: no title and no body (an empty paragraph counts as empty). */
export function isNewEmptyDocument(document: EmptyDocumentShape) {
  if (document.title.trim() || (document.content ?? '').trim()) return false
  const nodes = (document.contentData?.content as Array<{ type?: string; content?: unknown[] }> | undefined) ?? []
  return nodes.every(node => node.type === 'paragraph' && !(node.content?.length))
}

/** Teams whose templates apply to the document: its own teams (directly or through projects), else the viewer's. */
function documentTeams(ctx: DocumentActionContext, document: FlowDocument): Team[] {
  const { data } = ctx
  const ids = new Set([
    ...document.teamIds,
    ...data.projects.filter(project => document.projectIds.includes(project.id)).flatMap(project => project.teamIds),
  ])
  const own = data.teams.filter(team => ids.has(team.id) && !team.archivedAt)
  return own.length ? own : viewerTeams(data)
}

export function documentTemplateOptions(ctx: DocumentActionContext, document: FlowDocument): { options: PropertyOption[]; byId: Map<string, DocumentTemplate> } {
  const { data, t } = ctx
  const teams = documentTeams(ctx, document)
  const byId = new Map<string, DocumentTemplate>()
  const options: PropertyOption[] = []
  const add = (template: DocumentTemplate, groupLabel: string, icon: React.ReactNode) => {
    byId.set(template.id, template)
    options.push({ id: template.id, label: template.name, i18nIgnore: true, groupLabel, icon, keywords: template.title })
  }
  for (const template of (data.documentTemplates ?? []).filter(item => !item.teamId)) add(template, t('Workspace'), <Building2 size={14}/>)
  for (const team of teams) {
    for (const template of (data.documentTemplates ?? []).filter(item => item.teamId === team.id)) add(template, team.name, <TeamIcon team={team} size={14}/>)
  }
  return { options, byId }
}
