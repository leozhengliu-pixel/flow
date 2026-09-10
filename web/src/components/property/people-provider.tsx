import { useMemo, type ReactNode } from 'react'
import type { Project, Team, TeamMember, User, WorkspaceMember } from '@/types/flow'
import { PeopleContext } from './people-context'

const EMPTY_MEMBERS: WorkspaceMember[] = []
const EMPTY_TEAMS: Team[] = []
const EMPTY_TEAM_MEMBERS: TeamMember[] = []
const EMPTY_PROJECTS: Project[] = []

export function PeopleProvider({ users, workspaceName, children, members = EMPTY_MEMBERS, teams = EMPTY_TEAMS, teamMembers = EMPTY_TEAM_MEMBERS, projects = EMPTY_PROJECTS }: { users: User[]; workspaceName?: string; children: ReactNode; members?: WorkspaceMember[]; teams?: Team[]; teamMembers?: TeamMember[]; projects?: Project[] }) {
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
    return { users: new Map(users.map(user => [user.id, user])), members: new Map(members.map(member => [member.user.id, member])), teams: userTeams, teamIds: userTeamIds, projects: userProjects, workspaceName }
  }, [users, members, teams, teamMembers, projects, workspaceName])
  return <PeopleContext.Provider value={value}>{children}</PeopleContext.Provider>
}
