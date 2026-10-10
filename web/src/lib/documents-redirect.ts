import { viewerTeams } from '@/components/documents/document-actions'
import type { BootstrapData } from '@/types/flow'
import { teamDocumentsPath, teamsPath } from './app-routes'

/**
 * Where the old workspace "/documents" address lands. There is no workspace-wide
 * Documents list: it opens the Documents tab of the first team the viewer belongs
 * to, else the first team, else the teams directory.
 */
export function documentsRedirectPath(data: Pick<BootstrapData, 'teams' | 'teamMembers' | 'viewer' | 'workspace'>) {
  const team = viewerTeams(data as BootstrapData)[0] ?? data.teams.find(item => !item.archivedAt) ?? data.teams[0]
  return team ? teamDocumentsPath(data.workspace.urlKey, team.key) : teamsPath(data.workspace.urlKey)
}
