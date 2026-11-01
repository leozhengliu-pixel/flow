import { Box, Circle, Layers3, Lightbulb, Users, UserRound } from 'lucide-react'
import { createElement, type ReactNode } from 'react'
import type { BootstrapData, SearchResult } from '@/types/flow'
import { cyclePath, initiativePath, memberProfileHandle, memberProfilePath, projectPath, teamHomePath } from '@/lib/app-routes'
import { searchResultLink } from '@/lib/search-result-link'
import { favoriteDescriptor } from '@/components/layout/sidebar'
import type { CommandPage, PageOption } from './context-commands'

export const navigationPageLabels = {
  projectOpen: 'Open project…',
  cycleOpen: 'Open a cycle',
  teamOpen: 'Open a team',
  userOpen: 'Open a user',
  viewOpen: 'Open view',
  initiativeOpen: 'Open initiative',
  favoriteOpen: 'Open a favorite',
} as const
export type NavigationPageId = keyof typeof navigationPageLabels
export const NAVIGATION_PAGES = new Set<string>(Object.keys(navigationPageLabels))

export function navigationOptions(data: BootstrapData | undefined, page: CommandPage | undefined, results: SearchResult[], navigate: (path: string) => void, close: () => void): PageOption[] {
  if (!data || !page) return []
  const workspace = data.workspace.urlKey
  const option = (id: string, label: string, href: string, icon?: ReactNode, keywords?: string): PageOption => ({ id, label, entity: true, icon, keywords, select: () => { close(); navigate(href) } })
  switch (page.id) {
    case 'projectOpen': {
      const projects = data.projects.filter(project => !project.archivedAt).map(project => option(project.id, project.name, projectPath(workspace, project), createElement(Box)))
      return [...projects, ...results.filter(result => result.type === 'project' && !projects.some(project => project.id === result.id)).map(result => option(result.id, result.title, searchResultLink(data, result), createElement(Box)))]
    }
    case 'cycleOpen': return data.cycles.flatMap(cycle => {
      const team = data.teams.find(team => team.id === cycle.teamId)
      return team ? [option(cycle.id, cycle.name || `Cycle ${cycle.number}`, cyclePath(workspace, team.key, cycle), createElement(Circle), team.name)] : []
    })
    case 'teamOpen': return data.teams.filter(team => !team.archivedAt && !team.retiredAt).map(team => option(team.id, team.name, teamHomePath(workspace, team.key), createElement(Users), team.key))
    case 'userOpen': return data.users.filter(user => user.active !== false).map(user => option(user.id, user.displayName || user.name, memberProfilePath(workspace, memberProfileHandle(user)), createElement(UserRound), user.email))
    case 'viewOpen': return data.savedViews.map(view => option(view.id, view.name, searchResultLink(data, { type: 'view', id: view.id, title: view.name, score: 0 }), createElement(Layers3)))
    case 'initiativeOpen': return data.initiatives.map(initiative => option(initiative.id, initiative.name, initiativePath(workspace, initiative), createElement(Lightbulb)))
    case 'favoriteOpen': return data.favorites.filter(favorite => favorite.userId === data.viewer.id).flatMap(favorite => {
      const target = favoriteDescriptor(data, favorite, workspace)
      return target ? [option(favorite.id, target.title, target.href, target.icon, target.parent)] : []
    })
    default: return []
  }
}
