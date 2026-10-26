import type { ReactNode } from 'react'
import { TeamIcon } from '@/components/issue/issue-icons'
import { useI18n } from '@/i18n/i18n'
import { projectTeamsLabel, setProjectLeadTeam, toggleProjectTeam, type ProjectTeamChoice } from './project-teams'
import { PropertyMenu, type PropertyOption } from '@/components/property/property-menu'

function teamGlyph(team: ProjectTeamChoice) { return { icon: team.icon ?? 'Team', color: team.color ?? '#5e6ad2' } }

/** Orders the picker rows: the project's teams (lead first), then the remaining teams. */
function orderedTeams(teams: ProjectTeamChoice[], teamIds: string[]) {
  const selected = teamIds.map(id => teams.find(team => team.id === id)).filter((team): team is ProjectTeamChoice => Boolean(team))
  return { selected, others: teams.filter(team => !teamIds.includes(team.id)) }
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
  const { selected, others } = orderedTeams(teams, teamIds)
  const label = projectTeamsLabel(teams, teamIds, fallbackLabel)
  const lead = selected[0]
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
  const leadSubmenu = {
    placeholder: 'Change lead team…',
    heading: 'Project teams',
    selectedId: lead?.id ?? '',
    options: selected.map(team => toOption(team)),
    onChoose: (id: string) => { const next = setProjectLeadTeam(teamIds, id); if (next !== teamIds) onChange(next) },
  }
  const options: PropertyOption[] = [
    ...selected.map((team, index) => index === 0 && multiple ? toOption(team, { labelContent: teamRowLabel(team, t('Lead')), submenu: leadSubmenu }) : toOption(team)),
    ...others.map(team => toOption(team, { groupId: 'your-teams', groupLabel: 'Your teams' })),
  ]
  return <PropertyMenu
    compact
    multiple
    closeOnSelect
    label="Teams"
    ariaLabel={ariaLabel}
    value={label}
    valueIsEntityName={selected.length > 0}
    selectedIds={selected.map(team => team.id)}
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
    trigger={<><TeamIcon className={triggerIconClassName} team={lead ? teamGlyph(lead) : undefined} size={triggerIconSize}/><span data-i18n-ignore={selected.length ? true : undefined}>{label}</span></>}
    onChange={id => { const next = toggleProjectTeam(teamIds, id); if (next !== teamIds) onChange(next) }}
  />
}
