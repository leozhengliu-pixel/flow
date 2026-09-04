import * as Popover from '@radix-ui/react-popover'
import { FolderKanban, Search, UsersRound } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Avatar } from '@/components/issue/issue-row'
import { NoAssigneeIcon, PriorityIcon, TeamIcon } from '@/components/issue/issue-icons'
import type { ProjectPropertyOption } from '@/components/projects-page/project-property-picker'
import { ProjectTargetDatePicker } from '@/components/projects-page/project-target-date-picker'
import { PropertyMenu } from '@/components/property/property-menu'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { LabelPicker } from '@/components/issue/label-project-pickers'
import type { Initiative, InitiativeMutationInput, IssueLabel, LabelGroup, Project, Team, User } from '@/types/flow'
import { formatTarget, titleCase } from './initiative-model'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { ViewGlyph } from '@/components/views/view-icon-picker'

const INITIATIVE_STATUS_OPTIONS: ProjectPropertyOption[] = [
  { value: 'proposed', label: 'Proposed', statusType: 'backlog', shortcut: '1' },
  { value: 'planned', label: 'Planned', statusType: 'planned', shortcut: '2' },
  { value: 'active', label: 'Active', statusType: 'started', shortcut: '3' },
  { value: 'completed', label: 'Completed', statusType: 'completed', shortcut: '4' },
  { value: 'canceled', label: 'Canceled', statusType: 'canceled', shortcut: '5' },
]
const PRIORITY_OPTIONS: ProjectPropertyOption[] = [
  { value: 'none', label: 'No priority', shortcut: '0' }, { value: 'urgent', label: 'Urgent', shortcut: '1' },
  { value: 'high', label: 'High', shortcut: '2' }, { value: 'medium', label: 'Medium', shortcut: '3' }, { value: 'low', label: 'Low', shortcut: '4' },
]

export function InitiativeStatusIcon({ status }: { status: Initiative['status'] }) {
  if (status === 'proposed') return <svg aria-hidden="true" fill="#BEC2C8" height="16" viewBox="0 0 16 16" width="16"><path fillRule="evenodd" d="M4.402 12.156a5.49 5.49 0 0 0 2.847 1.29v1.512a6.98 6.98 0 0 1-3.91-1.738l1.063-1.064Zm8.259 1.064a6.98 6.98 0 0 1-3.911 1.738v-1.512a5.49 5.49 0 0 0 2.848-1.29l1.063 1.064ZM2.506 8.247c.045 1.025.372 1.976.903 2.78l-1.077 1.079a6.97 6.97 0 0 1-1.326-3.86h1.5Zm12.488 0a6.97 6.97 0 0 1-1.326 3.857l-1.078-1.077c.532-.804.859-1.755.904-2.78h1.5ZM7.487 4.79a.6.6 0 0 1 1.026 0l2.896 4.788c.318.525-.256 1.137-.795.848L8.094 9.072a.2.2 0 0 0-.188 0l-2.52 1.354c-.539.289-1.113-.323-.795-.848L7.487 4.79ZM3.842 4.401a5.47 5.47 0 0 0-1.197 2.346h-1.53A6.97 6.97 0 0 1 2.778 3.338l1.064 1.063Zm9.379-1.064a6.97 6.97 0 0 1 1.665 3.41h-1.531a5.47 5.47 0 0 0-1.198-2.347l1.064-1.063ZM7.249 2.551a5.47 5.47 0 0 0-2.278.857L3.893 2.331A6.96 6.96 0 0 1 7.249 1.04V2.55ZM8.75 1.039a6.96 6.96 0 0 1 3.355 1.291l-1.078 1.078a5.47 5.47 0 0 0-2.277-.857V1.04Z"/></svg>
  if (status === 'planned') return <svg aria-hidden="true" fill="#949698" height="16" viewBox="0 0 16 16" width="16"><path d="M9 8.002a1 1 0 1 1-2 0 1 1 0 0 1 2 0Z"/><path fillRule="evenodd" d="M8 12.002a4 4 0 1 0 0-8 4 4 0 0 0 0 8Zm0-1.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5Z"/><path fillRule="evenodd" d="M15 8.002a7 7 0 1 1-14 0 7 7 0 0 1 14 0Zm-1.5 0a5.5 5.5 0 1 1-11 0 5.5 5.5 0 0 1 11 0Z"/></svg>
  if (status === 'active') return <svg aria-hidden="true" fill="lch(80% 90 85)" height="16" viewBox="0 0 16 16" width="16"><path d="m4.591 9.581 2.897-4.79a.6.6 0 0 1 1.024 0l2.897 4.79c.318.524-.256 1.136-.795.846L8.094 9.074a.2.2 0 0 0-.188 0l-2.52 1.353c-.539.29-1.112-.322-.795-.846Z"/><path fillRule="evenodd" d="M15 8.002a7 7 0 1 1-14 0 7 7 0 0 1 14 0Zm-1.5 0a5.5 5.5 0 1 1-11 0 5.5 5.5 0 0 1 11 0Z"/></svg>
  if (status === 'completed') return <svg aria-hidden="true" fill="#5E68D0" height="16" viewBox="0 0 16 16" width="16"><path d="M11.28 6.782a.75.75 0 0 0-1.06-1.06L7.25 8.69 5.78 7.222a.75.75 0 0 0-1.06 1.06l2 2a.75.75 0 0 0 1.06 0l3.5-3.5Z"/><path fillRule="evenodd" d="M15 8.002a7 7 0 1 1-14 0 7 7 0 0 1 14 0Zm-1.5 0a5.5 5.5 0 1 1-11 0 5.5 5.5 0 0 1 11 0Z"/></svg>
  return <svg aria-hidden="true" fill="#95A2B3" height="16" viewBox="0 0 16 16" width="16"><path fillRule="evenodd" d="M9.47 5.47a.75.75 0 1 1 1.06 1.06L9.061 8l1.47 1.47a.75.75 0 0 1-1.061 1.06L8 9.061l-1.47 1.47a.75.75 0 0 1-1.06-1.061L6.939 8 5.47 6.53a.75.75 0 0 1 1.06-1.06L8 6.939l1.47-1.47Z"/><path fillRule="evenodd" d="M8 1a7 7 0 1 1 0 14A7 7 0 0 1 8 1Zm0 1.5a5.5 5.5 0 1 0 0 11 5.5 5.5 0 0 0 0-11Z"/></svg>
}

export function InitiativeProperties({ initiative, users, teams = [], onUpdate, compact = false, only }: {
	initiative: Initiative; users: User[]; teams?: Team[]; compact?: boolean; only?: 'status'|'priority'|'owner'|'leadTeam'|'contributingTeams'|'target'; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const ownerOptions = useMemo(() => [{ id: '', label: 'No owner', icon: <NoAssigneeIcon size={15}/>, shortcut: '0' }, ...users.map((user, index) => ({ id: user.id, label: user.displayName || user.name, icon: <Avatar name={user.displayName || user.name}/>, shortcut: index === 0 ? '1' : undefined, end: user.active ? undefined : 'Invited', i18nIgnore: true }))], [users])
  const teamOptions = useMemo(() => [{ id: '', label: 'No lead team', icon: <TeamIcon size={15}/>, shortcut: '0' }, ...teams.map(team => ({ id: team.id, label: team.name, icon: <TeamIcon team={team} size={15}/>, groupLabel: 'Your teams', end: team.key, i18nIgnore: true }))], [teams])
  const leadTeam = teams.find(team => team.id === initiative.leadTeamId)
  const creating = initiative.id === 'draft'
  const controls = {
    status: <PropertyMenu ariaLabel={creating ? 'Change status' : titleCase(initiative.status)} label="Status" onChange={value => onUpdate({ status: value as Initiative['status'] })} options={INITIATIVE_STATUS_OPTIONS.map(option => ({ id: option.value, label: option.label, shortcut: option.shortcut, icon: <InitiativeStatusIcon status={option.value as Initiative['status']}/> }))} searchPlaceholder="Change status…" searchShortcut="S" selectedId={initiative.status} surfaceClassName="li-initiative-command li-initiative-command--status" trigger={<><InitiativeStatusIcon status={initiative.status}/><span>{titleCase(initiative.status)}</span></>} triggerClassName="lp-project-property-trigger" triggerRole={creating ? 'combobox' : 'button'} value={initiative.status}/>,
    priority: <PropertyMenu ariaLabel={creating ? 'Change priority' : initiative.priorityLabel} label="Priority" onChange={value => onUpdate({ priority: priorityNumber(value) })} options={PRIORITY_OPTIONS.map(option => ({ id: option.value, label: option.label, shortcut: option.shortcut, icon: <PriorityIcon priority={priorityNumber(option.value)} size={15}/> }))} searchPlaceholder="Change priority…" selectedId={priorityValue(initiative.priority)} surfaceClassName="li-initiative-command li-initiative-command--priority" trigger={<><PriorityIcon priority={initiative.priority} size={15}/><span>{initiative.priorityLabel}</span></>} triggerClassName="lp-project-property-trigger" triggerRole={creating ? 'combobox' : 'button'} value={initiative.priorityLabel}/>,
    owner: <PropertyMenu ariaLabel={creating ? 'Change initiative owner' : initiative.owner?.displayName || initiative.owner?.name || 'Owner'} label="Owner" onChange={value => onUpdate({ ownerId: value })} options={ownerOptions} searchPlaceholder="Set owner…" searchShortcut="N, then O" selectedId={initiative.owner?.id ?? ''} surfaceClassName="li-initiative-command li-initiative-command--owner" trigger={<>{initiative.owner ? <Avatar name={initiative.owner.displayName || initiative.owner.name}/> : <NoAssigneeIcon size={15}/>}<span data-i18n-ignore={initiative.owner ? true : undefined}>{initiative.owner?.displayName || initiative.owner?.name || 'Owner'}</span></>} triggerClassName="lp-project-property-trigger" triggerRole={creating ? 'combobox' : 'button'} value={initiative.owner?.displayName || initiative.owner?.name || 'Owner'} valueIsEntityName={Boolean(initiative.owner)}/>,
    leadTeam: <PropertyMenu ariaLabel={creating ? 'Change lead team' : leadTeam?.key || 'Lead team'} label="Lead team" onChange={value => onUpdate({ leadTeamId: value })} options={teamOptions} searchPlaceholder="Set lead team…" selectedId={initiative.leadTeamId ?? ''} surfaceClassName="li-initiative-command li-initiative-command--team" trigger={<><TeamIcon team={leadTeam} size={15}/><span data-i18n-ignore={leadTeam ? true : undefined}>{leadTeam?.key || 'Lead team'}</span></>} triggerClassName="lp-project-property-trigger" triggerRole={creating ? 'combobox' : 'button'} value={leadTeam?.name || 'Lead team'} valueIsEntityName={Boolean(leadTeam)}/>,
    contributingTeams: <InitiativeTeamsPicker initiative={initiative} teams={teams} onUpdate={onUpdate}/>,
    target: <ProjectTargetDatePicker ariaLabel={creating ? 'Set initiative target date' : 'Change initiative target date'} compactPeriods defaultMode="quarter" displayValue={formatTarget(initiative.targetDate, initiative.targetDateResolution)} onChange={(value, targetDateResolution) => onUpdate({ targetDate: value, targetDateResolution: targetDateResolution ?? '' })} resolution={initiative.targetDateResolution} triggerRole={creating ? 'combobox' : 'button'} value={initiative.targetDate}>
      <span className={`li-target-glyph ${initiative.targetDate ? '' : 'is-empty'}`} aria-hidden="true"/><span>{formatTarget(initiative.targetDate, initiative.targetDateResolution) || 'Target date'}</span>
    </ProjectTargetDatePicker>,
  }
  return <div className={`${compact ? 'li-properties li-properties--compact' : 'li-properties'} ${only ? `li-properties--${only}` : ''}`}>{only ? controls[only] : <>{controls.status}{controls.priority}{controls.owner}{controls.leadTeam}{controls.contributingTeams}{controls.target}</>}</div>
}

export function InitiativeTeamsPicker({ initiative, teams, onUpdate }: { initiative: Initiative; teams: Team[]; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown> }) {
  const selected = teams.filter(team => initiative.contributingTeamIds?.includes(team.id))
  const toggle = (teamId: string) => void onUpdate({ contributingTeamIds: initiative.contributingTeamIds?.includes(teamId) ? initiative.contributingTeamIds.filter(id => id !== teamId) : [...(initiative.contributingTeamIds ?? []), teamId] })
  return <Popover.Root><Popover.Trigger asChild><button aria-label="Change contributing teams" className="li-team-picker" type="button">{selected.length ? <><span className="li-team-mark"><ViewGlyph color={selected[0].color} icon={selected[0].icon || 'Team'}/></span><span data-i18n-ignore>{selected.length === 1 ? selected[0].name : `${selected.length} teams`}</span></> : <><UsersRound size={14}/><span>Contributing teams</span></>}</button></Popover.Trigger><Popover.Portal><Popover.Content align="start" className="li-team-picker-menu" collisionPadding={8} sideOffset={4}><header>Contributing teams</header>{teams.map(team => <button aria-checked={initiative.contributingTeamIds?.includes(team.id)} key={team.id} onClick={() => toggle(team.id)} role="checkbox" type="button"><span className="li-picker-checkbox">{initiative.contributingTeamIds?.includes(team.id) && <CheckboxMark/>}</span><span className="li-team-mark"><ViewGlyph color={team.color} icon={team.icon || 'Team'}/></span><span data-i18n-ignore>{team.name}</span></button>)}</Popover.Content></Popover.Portal></Popover.Root>
}

export function InitiativeLabelsPicker({ initiative, labels, labelGroups = [], onUpdate, onCreateLabel, compact = false }: {
  initiative: Initiative
  labels: IssueLabel[]
  labelGroups?: LabelGroup[]
  compact?: boolean
  onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
  onCreateLabel?: (name: string) => Promise<IssueLabel>
}) {
  const selected = labels.filter(label => initiative.labelIds.includes(label.id))
  return <div className={compact ? 'li-label-picker li-label-picker--compact' : 'li-label-picker'}>
    <LabelPicker emptyLabel="Start typing to create a new label" labels={labels} labelGroups={labelGroups} searchShortcut="N, then L" showGroupHeadings={false} surfaceClassName="li-initiative-label-command" value={selected} onToggle={labelId => { void onUpdate({ labelIds: initiative.labelIds.includes(labelId) ? initiative.labelIds.filter(id => id !== labelId) : [...initiative.labelIds, labelId] }) }} onCreate={onCreateLabel ? async name => { const label = await onCreateLabel(name); await onUpdate({ labelIds: [...new Set([...initiative.labelIds, label.id])] }) } : undefined}/>
  </div>
}

export function ProjectAssociationPicker({ children, initiative, projects, onUpdate, label = 'Add project' }: {
	children?: ReactNode; initiative: Initiative; projects: Project[]; label?: string; onUpdate: (input: InitiativeMutationInput) => void | Promise<unknown>
}) {
  const [open, setOpen] = useState(false)
  const toggle = (id: string) => {
    const next = initiative.projectIds.includes(id) ? initiative.projectIds.filter(item => item !== id) : [...initiative.projectIds, id]
    void onUpdate({ projectIds: next })
  }
  const options=projects.map(project=>({id:project.id,label:project.name,keywords:project.summary})),command=usePropertyCommand({closeOnSelect:false,open,options,selectedIds:initiative.projectIds,onOpenChange:setOpen,onSelect:option=>toggle(option.id)})
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>{children ?? <button className="li-icon-button ui-pill" aria-label={label} type="button"><span>+</span></button>}</Popover.Trigger>
    <Popover.Portal><Popover.Content align="end" className="li-project-picker" collisionPadding={8} sideOffset={4} onOpenAutoFocus={event => event.preventDefault()}>
      <label><Search size={14}/><input ref={command.inputRef} autoFocus aria-label="Search projects…" placeholder="Search projects…" value={command.query} onChange={event=>command.onQueryChange(event.target.value)} onKeyDown={command.onKeyDown}/></label>
      <div role="listbox" aria-multiselectable="true" onKeyDown={command.onKeyDown}>{command.filteredOptions.map(option=>{const project=projects.find(item=>item.id===option.id)!;return <button aria-checked={command.isSelected(option.id)} aria-selected={command.activeId===option.id} key={option.id} onPointerMove={()=>command.setActiveId(option.id)} onFocus={()=>command.setActiveId(option.id)} onClick={()=>command.choose(option)} role="option" type="button"><span className="li-picker-checkbox">{command.isSelected(option.id)&&<CheckboxMark/>}</span><span className="li-project-icon" style={{color:project.color}}><FolderKanban size={14}/></span><span><strong data-i18n-ignore>{project.name}</strong><small data-i18n-ignore>{project.summary||project.status.name}</small></span></button>})}{!command.filteredOptions.length&&<p>No projects found</p>}</div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function priorityValue(value: number) { return ['none', 'urgent', 'high', 'medium', 'low'][value] ?? 'none' }
function priorityNumber(value: string) { return Math.max(0, ['none', 'urgent', 'high', 'medium', 'low'].indexOf(value)) }
