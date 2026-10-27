import type { ReactNode } from 'react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import { orderTeamTree, projectTeamsLabel, teamGlyph, setProjectLeadTeam, toggleProjectTeam, type ProjectTeamChoice } from './project-teams'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'
import { usePeopleDirectory } from '@/components/property/people-context'
import { TEAM_HOVER_CLASS, TeamHoverCard } from './team-hover-card'

/**
 * Orders the picker rows: the project's teams, then the remaining teams. In each block a sub-team follows its parent
 * (indented one level per ancestor shown in the same block).
 */
function orderedTeams(teams: ProjectTeamChoice[], teamIds: string[], parentOf: (id: string) => string | undefined) {
  const selected = teamIds.map(id => teams.find(team => team.id === id)).filter((team): team is ProjectTeamChoice => Boolean(team))
  return { selected: orderTeamTree(selected, parentOf), others: orderTeamTree(teams.filter(team => !teamIds.includes(team.id)), parentOf), lead: selected[0] }
}

function teamRowLabel(team: ProjectTeamChoice, leadText?: string): ReactNode {
  return <><span className="project-teams-menu__name" data-i18n-ignore>{team.name}</span>{leadText ? <span className="project-teams-menu__badge">{leadText}</span> : team.key ? <span className="project-teams-menu__key" data-i18n-ignore>{team.key}</span> : null}</>
}

export function ProjectTeamsPicker({ teams, teamIds, onChange, fallbackLabel = 'Team', side, align, alignOffset, triggerClassName, surfaceClassName, ariaLabel = 'Change project teams', triggerIconSize = 16, tooltip, tooltipShortcut, searchShortcut, open, onOpenChange, triggerIconClassName }: {
  teams: ProjectTeamChoice[]
  teamIds: string[]
  /** Receives the project's new ordered team ids (lead team first). */
  onChange: (teamIds: string[]) => void
  fallbackLabel?: string
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  alignOffset?: number
  triggerClassName: string
  surfaceClassName?: string
  ariaLabel?: string
  triggerIconSize?: number
  triggerIconClassName?: string
  tooltip?: string
  tooltipShortcut?: string
  searchShortcut?: string
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const { t } = useI18n()
  const directory = usePeopleDirectory()
  const parents = new Map(teams.map(team => [team.id, team.parentId ?? directory.teamDirectory.get(team.id)?.parentId]))
  const { selected, others, lead } = orderedTeams(teams, teamIds, id => parents.get(id) ?? directory.teamDirectory.get(id)?.parentId)
  const label = projectTeamsLabel(teams, teamIds, fallbackLabel)
  const multiple = selected.length > 1
  const toOption = (team: ProjectTeamChoice, extra: Partial<PropertyOption> = {}): PropertyOption => ({
    id: team.id,
    label: team.name,
    labelContent: teamRowLabel(team),
    icon: <TeamIcon team={teamGlyph(team)} size={14}/>,
    i18nIgnore: true,
    keywords: team.key,
    ...extra,
  })
  const withHover = (team: ProjectTeamChoice, option: PropertyOption): PropertyOption => ({ ...option, hoverContent: <TeamHoverCard team={team}/>, hoverClassName: TEAM_HOVER_CLASS, hoverBesideMenu: true })
  const leadSubmenu = {
    placeholder: 'Change lead team…',
    heading: 'Project teams',
    selectedId: lead?.id ?? '',
    options: selected.map(row => toOption(row.team)),
    onChoose: (id: string) => { const next = setProjectLeadTeam(teamIds, id); if (next !== teamIds) onChange(next) },
  }
  const options: PropertyOption[] = [
    ...selected.map(({ team, depth }) => withHover(team, team.id === lead?.id && multiple ? toOption(team, { labelContent: teamRowLabel(team, t('Lead')), submenu: leadSubmenu, indent: depth }) : toOption(team, { indent: depth }))),
    ...others.map(({ team, depth }) => withHover(team, toOption(team, { groupId: 'your-teams', groupLabel: 'Your teams', indent: depth }))),
  ]
  return <PropertyMenu
    compact
    multiple
    closeOnSelect
    label="Teams"
    ariaLabel={ariaLabel}
    value={label}
    valueIsEntityName={Boolean(lead)}
    selectedIds={selected.map(row => row.team.id)}
    options={options}
    searchPlaceholder="Change teams…"
    searchShortcut={searchShortcut}
    triggerClassName={triggerClassName}
    surfaceClassName={`project-teams-menu${surfaceClassName ? ` ${surfaceClassName}` : ''}`}
    side={side}
    align={align}
    alignOffset={alignOffset}
    open={open}
    onOpenChange={onOpenChange}
    tooltip={tooltip}
    tooltipShortcut={tooltipShortcut}
    trigger={<><TeamIcon className={triggerIconClassName} team={lead ? teamGlyph(lead) : undefined} size={triggerIconSize}/><span data-i18n-ignore={lead ? true : undefined}>{label}</span></>}
    onChange={id => { const next = toggleProjectTeam(teamIds, id); if (next !== teamIds) onChange(next) }}
  />
}
