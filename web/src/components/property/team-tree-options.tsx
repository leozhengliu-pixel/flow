import type { ReactNode } from 'react'
import { ChevronRight } from 'lucide-react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { teamHierarchy, type TeamHierarchySettings } from '@/lib/team-hierarchy'
import type { Team } from '@/types/flow'
import type { PropertyOption } from './property-menu'
import '@/components/issue/issue-team-picker.css'

/**
 * Options for a team menu: sub-teams follow their parent and are indented one level per ancestor shown in the same
 * list (`indent`), so the menu draws the shared tree elbow. On a phone the sheet is flat and a sub-team reads
 * "Parent > Team" instead. `showKey` adds the team key after the name.
 */
export function teamTreeOptions(data: { teams: Team[]; teamSettings?: TeamHierarchySettings; teamParents?: Record<string, string> }, teams: Team[], { phone = false, showKey = true, extra }: { phone?: boolean; showKey?: boolean; extra?: (team: Team) => Partial<PropertyOption> } = {}): PropertyOption[] {
  const hierarchy = teamHierarchy(data.teams, data.teamSettings, data.teamParents)
  return hierarchy.rows(teams).map(({ team, depth }) => {
    const ancestors = hierarchy.ancestors.get(team.id) ?? []
    const path = [...ancestors, team].map(item => item.name).join(' > ')
    const label: ReactNode = <span className="issue-team-path">{phone && ancestors.map(parent => <span className="issue-team-parent" key={parent.id}><span>{parent.name}</span><ChevronRight viewBox="8 5 8 14"/></span>)}<span className="issue-team-name">{team.name}</span>{showKey && <span className="issue-team-key">{team.key}</span>}</span>
    return {
      id: team.id,
      label: showKey ? `${path} ${team.key}` : path,
      ariaLabel: showKey ? `${path} ${team.key}` : path,
      keywords: `${path} ${team.key}`,
      indent: phone ? undefined : depth || undefined,
      labelContent: label,
      icon: <TeamIcon team={team} size={phone ? 16 : 14}/>,
      i18nIgnore: true,
      ...extra?.(team),
    }
  })
}
