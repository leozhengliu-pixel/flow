import { useMemo, type ReactNode } from 'react'
import type { FlowDocument, Project, Team, TeamMember, TeamSettings, User, WorkspaceMember } from '@/types/flow'
import { PeopleContext, type TeamDirectoryEntry } from './people-context'

const EMPTY_MEMBERS: WorkspaceMember[] = []
const EMPTY_TEAMS: Team[] = []
const EMPTY_TEAM_MEMBERS: TeamMember[] = []
const EMPTY_PROJECTS: Project[] = []
const EMPTY_DOCUMENTS: FlowDocument[] = []
const EMPTY_SETTINGS: Record<string, Pick<TeamSettings, 'parentTeamId'>> = {}

export function PeopleProvider({ users, workspaceName, children, members = EMPTY_MEMBERS, teams = EMPTY_TEAMS, teamMembers = EMPTY_TEAM_MEMBERS, projects = EMPTY_PROJECTS, documents = EMPTY_DOCUMENTS, teamSettings = EMPTY_SETTINGS, viewerId }: { users: User[]; viewerId?: string; workspaceName?: string; children: ReactNode; members?: WorkspaceMember[]; teams?: Team[]; teamMembers?: TeamMember[]; projects?: Project[]; documents?: FlowDocument[]; teamSettings?: Record<string, Pick<TeamSettings, 'parentTeamId'>> }) {
  const value = useMemo(() => {
    const teamNames = new Map(teams.map(team => [team.id, team.name]))
    const userTeams = new Map<string, string[]>()
    const userTeamIds = new Map<string, string[]>()
    const userProjects = new Map<string, string[]>()
    const append = (map: Map<string, string[]>, id: string, name: string) => {
      const names = map.get(id)
      if (names) names.push(name)
      else map.set(id, [name])
    }
    for (const member of teamMembers) {
      append(userTeamIds, member.userId, member.teamId)
      const name = teamNames.get(member.teamId)
      if (name) append(userTeams, member.userId, name)
    }
    for (const project of projects) {
      if (project.archivedAt) continue
      for (const id of new Set([...(project.memberIds ?? []), ...(project.lead ? [project.lead.id] : [])])) append(userProjects, id, project.name)
    }
    const teamDirectory = new Map<string, TeamDirectoryEntry>(teams.map(team => [team.id, { parentId: teamSettings[team.id]?.parentTeamId || undefined, memberIds: [], projectCount: 0, documentCount: 0 }]))
    for (const member of teamMembers) teamDirectory.get(member.teamId)?.memberIds.push(member.userId)
    const projectTeams = new Map(projects.map(project => [project.id, project.teamIds ?? []]))
    for (const project of projects) if (!project.archivedAt) for (const id of new Set(project.teamIds ?? [])) { const entry = teamDirectory.get(id); if (entry) entry.projectCount += 1 }
    // A team's documents are the ones filed under it or under one of its projects (as on the team page).
    for (const document of documents) {
      if (document.archivedAt) continue
      for (const id of new Set([...(document.teamIds ?? []), ...(document.projectIds ?? []).flatMap(projectId => projectTeams.get(projectId) ?? [])])) { const entry = teamDirectory.get(id); if (entry) entry.documentCount += 1 }
    }
    return { teamDirectory, users: new Map(users.map(user => [user.id, user])), members: new Map(members.map(member => [member.user.id, member])), teams: userTeams, teamIds: userTeamIds, projects: userProjects, workspaceName, viewerId }
  }, [users, members, teams, teamMembers, projects, documents, teamSettings, workspaceName, viewerId])
  return <PeopleContext.Provider value={value}>{children}</PeopleContext.Provider>
}
