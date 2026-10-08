import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { ArrowDownNarrowWide, ArrowDownWideNarrow, Check, ChevronDown, X } from 'lucide-react'
import { useState } from 'react'

import { TeamIcon } from '@/components/issue/issue-icons'
import { FlowTooltip } from '@/components/ui/tooltip'
import { DirectoryFilterMenu, type DirectoryFilterGroup } from '@/components/workspace-directory/directory-menus'
import { TeamDateFilterDialog } from '@/components/workspace-directory/team-directory-controls'
import { useI18n } from '@/i18n/i18n'
import type { Team } from '@/types/flow'

import { EMPTY_LABEL_FILTERS, LAST_APPLIED_CHOICES, type LabelColumn, type LabelDisplayState, type LabelFilters, type LabelGrouping } from './label-settings-model'
import './label-settings.css'

const CUSTOM_DATE = 'custom'

/** Team and Last applied filter groups of Linear's label settings filter menu. */
function useFilterGroups(teams: Team[]) {
  const { t } = useI18n()
  const groups: DirectoryFilterGroup[] = []
  if (teams.length) groups.push({
    id: 'team', label: t('Team'), icon: <TeamIcon size={14}/>, hideSearch: teams.length < 8,
    choices: teams.map(team => ({ id: team.id, label: team.name, icon: <TeamIcon team={team} size={14}/>, entity: true })),
  })
  groups.push({
    id: 'lastApplied', label: t('Last applied'), icon: <LastAppliedIcon/>, selectionMode: 'single', hideSearch: false,
    choices: [...LAST_APPLIED_CHOICES.map(choice => ({ id: choice.id, label: t(choice.label) })), { id: CUSTOM_DATE, label: t('Custom date or timeframe…') }],
  })
  return groups
}

export function LabelFilterMenu({ teams, filters, onChange, trigger = 'icon' }: { teams: Team[]; filters: LabelFilters; onChange: (filters: LabelFilters) => void; trigger?: 'icon' | 'add' }) {
  const { t } = useI18n()
  const groups = useFilterGroups(teams)
  const [dateOpen, setDateOpen] = useState(false)
  const selected = { team: new Set(filters.teams), lastApplied: new Set(filters.lastApplied ? [filters.lastApplied] : []) }
  const choose = (group: string, id: string, checked: boolean) => {
    if (group === 'team') onChange({ ...filters, teams: checked ? [...new Set([...filters.teams, id])] : filters.teams.filter(item => item !== id) })
    else if (id === CUSTOM_DATE) setDateOpen(true)
    else onChange({ ...filters, lastApplied: id })
  }
  return <>
    <DirectoryFilterMenu
      groups={groups}
      selected={selected}
      trigger={trigger}
      onChoice={choose}
      showAdvanced={false}
      hideSearch
      triggerClassName="label-settings-icon-button"
      menuClassName="label-settings-filter-menu"
      tooltip={trigger === 'icon' ? { label: t('Filter'), shortcut: 'F' } : undefined}
    />
    <TeamDateFilterDialog open={dateOpen} value={filters.lastApplied} title={t('Last applied')} fromLabel={t('Last applied on or after')} toLabel={t('Last applied through')} onClose={() => setDateOpen(false)} onApply={value => { onChange({ ...filters, lastApplied: value }); setDateOpen(false) }}/>
  </>
}

/** Linear's applied-filter strip under the label toolbar: one chip per filter, "+" and Clear. */
export function LabelFilterBar({ teams, filters, onChange }: { teams: Team[]; filters: LabelFilters; onChange: (filters: LabelFilters) => void }) {
  const { t } = useI18n()
  if (!filters.teams.length && !filters.lastApplied) return null
  const teamNames = filters.teams.map(id => teams.find(team => team.id === id)?.name ?? t('Unknown team'))
  const lastApplied = filters.lastApplied
  const lastAppliedLabel = !lastApplied ? '' : lastApplied === 'never' ? t('never applied')
    : lastApplied.startsWith('date:') ? lastApplied.slice(5).replace('/', ' – ')
    : t(LAST_APPLIED_CHOICES.find(choice => choice.id === lastApplied)?.label ?? lastApplied)
  return <div className="label-settings-filter-bar" role="group" aria-label={t('Applied filters')}>
    <div className="label-settings-filter-bar__chips">
      {filters.teams.length > 0 && <span className="label-settings-filter-chip">
        <span className="label-settings-filter-chip__field"><TeamIcon size={12}/>{t('Team')}</span>
        <button type="button" className="label-settings-filter-chip__operator" onClick={() => onChange({ ...filters, teamOperator: filters.teamOperator === 'is' ? 'isNot' : 'is' })}>{t(filters.teamOperator === 'is' ? (filters.teams.length > 1 ? 'is any of' : 'is') : (filters.teams.length > 1 ? 'is not any of' : 'is not'))}</button>
        <span className="label-settings-filter-chip__value" data-i18n-ignore>{teamNames.join(', ')}</span>
        <button type="button" className="label-settings-filter-chip__remove" aria-label={t('Remove filter')} onClick={() => onChange({ ...filters, teams: [], teamOperator: 'is' })}><X/></button>
      </span>}
      {lastApplied && <span className="label-settings-filter-chip">
        <span className="label-settings-filter-chip__field"><LastAppliedIcon size={12}/>{t('Last applied')}</span>
        {lastApplied === 'never'
          ? <span className="label-settings-filter-chip__operator is-static">{t('is')}</span>
          : <button type="button" className="label-settings-filter-chip__operator" onClick={() => onChange({ ...filters, lastAppliedOperator: filters.lastAppliedOperator === 'after' ? 'before' : 'after' })}>{t(lastApplied.includes('/') ? (filters.lastAppliedOperator === 'after' ? 'between' : 'not between') : filters.lastAppliedOperator)}</button>}
        <span className="label-settings-filter-chip__value">{lastAppliedLabel}</span>
        <button type="button" className="label-settings-filter-chip__remove" aria-label={t('Remove filter')} onClick={() => onChange({ ...filters, lastApplied: undefined, lastAppliedOperator: 'after' })}><X/></button>
      </span>}
      <LabelFilterMenu teams={teams} filters={filters} onChange={onChange} trigger="add"/>
    </div>
    <button type="button" className="label-settings-filter-bar__clear" aria-label={t('Clear all filters')} onClick={() => onChange(EMPTY_LABEL_FILTERS)}>{t('Clear')}</button>
  </div>
}

export interface LabelDisplayOptionsProps {
  display: LabelDisplayState
  columns: { id: LabelColumn; label: string }[]
  canGroup: boolean
  canShowTeamLabels: boolean
  teamLabelsLabel: string
  onChange: (display: LabelDisplayState) => void
}

/** Linear's label display options: Grouping, Ordering (direction + column), team and archived toggles. */
export function LabelDisplayOptions({ display, columns, canGroup, canShowTeamLabels, teamLabelsLabel, onChange }: LabelDisplayOptionsProps) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const ordering = columns.find(column => column.id === display.ordering) ?? columns[0]
  const groupings: { id: LabelGrouping; label: string }[] = [{ id: 'none', label: t('No grouping') }, { id: 'team', label: t('Team') }]
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <FlowTooltip disabled={open} label={t('Display options')}>
      <Popover.Trigger asChild><button type="button" aria-label={t('Display options')} className="label-settings-icon-button"><DisplayIcon/></button></Popover.Trigger>
    </FlowTooltip>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" data-i18n-ignore align="end" sideOffset={4} collisionPadding={8} className="label-settings-display-menu">
        {canGroup && <div className="label-settings-display-menu__row">
          <span>{t('Grouping')}</span>
          <PillSelect label={t('Grouping')} value={display.grouping} options={groupings} onChange={grouping => onChange({ ...display, grouping })}/>
        </div>}
        <div className="label-settings-display-menu__row">
          <span>{t('Ordering')}</span>
          <button type="button" className="label-settings-display-menu__direction" aria-label={t(display.descending ? 'Descending' : 'Ascending')} title={t(display.descending ? 'Descending' : 'Ascending')} onClick={() => onChange({ ...display, descending: !display.descending })}>{display.descending ? <ArrowDownWideNarrow/> : <ArrowDownNarrowWide/>}</button>
          <PillSelect label={t('Ordering')} value={ordering?.id} options={columns} onChange={column => onChange({ ...display, ordering: column, descending: column === display.ordering ? display.descending : column === 'rules' || column === 'usage' || column === 'archivedAt' })}/>
        </div>
        {canShowTeamLabels && <Switch label={teamLabelsLabel} checked={display.showTeamLabels} onChange={showTeamLabels => onChange({ ...display, showTeamLabels })}/>}
        <Switch label={t('Show archived')} checked={display.showArchived} onChange={showArchived => onChange({ ...display, showArchived })}/>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function PillSelect<T extends string>({ label, value, options, onChange }: { label: string; value?: T; options: { id: T; label: string }[]; onChange: (value: T) => void }) {
  const current = options.find(option => option.id === value) ?? options[0]
  return <DropdownMenu.Root>
    <DropdownMenu.Trigger asChild><button type="button" role="combobox" aria-label={label} aria-expanded={undefined} className="label-settings-display-menu__select"><span>{current?.label}</span><ChevronDown/></button></DropdownMenu.Trigger>
    <DropdownMenu.Portal>
      <DropdownMenu.Content data-flow-motion="floating" data-i18n-ignore align="end" sideOffset={4} className="label-settings-display-select-menu">
        {options.map(option => <DropdownMenu.Item key={option.id} onSelect={() => onChange(option.id)}><span>{option.label}</span>{option.id === current?.id && <Check/>}</DropdownMenu.Item>)}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>
}

function Switch({ label, checked, onChange }: { label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return <label className="label-settings-display-menu__row is-toggle">
    <span>{label}</span>
    <button type="button" role="switch" aria-label={label} aria-checked={checked} onClick={() => onChange(!checked)}><i/></button>
  </label>
}

/** Linear's empty label list: dashed ellipse with four label tokens, then the status line. */
export function LabelsEmptyState({ filtering }: { filtering: boolean }) {
  const { t } = useI18n()
  const token = (x: number, y: number, front: boolean) => <rect key={`${x}-${y}-${front}`} className={front ? 'is-front' : 'is-back'} x="1.20654" y="0.380419" width="17.7" height="17.7" rx="8.85" transform={`matrix(0.965926 -0.258819 0.642788 0.766044 ${x} ${y})`} strokeWidth="1.5"/>
  return <div className="label-settings-empty" role="status">
    <svg aria-hidden="true" className="label-settings-empty__art" fill="none" viewBox="0 0 161 93">
      {[[51.9216, 30.6583], [75.57, 58.8486], [81.0075, 39.3782], [45.4606, 48.9036]].map(([x, y]) => token(x, y, false))}
      {[[52.7653, 27.1331], [76.4216, 55.3236], [81.8513, 35.8533], [46.3044, 45.3778]].map(([x, y]) => token(x, y, true))}
      <rect className="is-ring" x="1.20654" y="0.380419" width="94.5" height="94.5" rx="47.25" transform="matrix(0.965926 -0.258819 0.642788 0.766044 -0.00810446 25.2479)" strokeWidth="1.5" strokeDasharray="4 4"/>
    </svg>
    <span>{t(filtering ? 'No matching labels' : 'No labels found')}</span>
  </div>
}

export function LastAppliedIcon({ size = 14 }: { size?: number }) {
  return <svg aria-hidden="true" width={size} height={size} viewBox="0 0 16 16" fill="currentColor"><path fillRule="evenodd" clipRule="evenodd" d="M15 5C15 2.79086 13.2091 1 11 1H5C2.79086 1 1 2.79086 1 5V11C1 13.2091 2.79086 15 5 15H6.25C6.66421 15 7 14.6642 7 14.25C7 13.8358 6.66421 13.5 6.25 13.5H5C3.61929 13.5 2.5 12.3807 2.5 11V6H13.5V6.25C13.5 6.66421 13.8358 7 14.25 7C14.6642 7 15 6.66421 15 6.25V5ZM11 7.25C10.5858 7.25 10.25 7.58579 10.25 8C10.25 8.41421 10.5858 8.75 11 8.75C12.2426 8.75 13.25 9.75736 13.25 11H12.6403C12.2622 11 12.0952 11.4761 12.3904 11.7123L13.7501 12.8001C13.8962 12.917 14.1038 12.917 14.2499 12.8001L15.6095 11.7123C15.9048 11.4761 15.7378 11 15.3597 11H14.75C14.75 8.92893 13.071 7.25 11 7.25ZM6.64029 11H7.24998C7.24998 13.0711 8.92891 14.75 11 14.75C11.4142 14.75 11.75 14.4142 11.75 14C11.75 13.5858 11.4142 13.25 11 13.25C9.75734 13.25 8.74998 12.2426 8.74998 11H9.35967C9.73778 11 9.9048 10.5239 9.60955 10.2877L8.24986 9.1999C8.10377 9.08303 7.89619 9.08303 7.7501 9.1999L6.39041 10.2877C6.09516 10.5239 6.26218 11 6.64029 11Z"/></svg>
}

function DisplayIcon() {
  return <svg aria-hidden="true" viewBox="0 0 16 16" fill="currentColor"><path fillRule="evenodd" clipRule="evenodd" d="M7 2.5c1.12 0 2.066.736 2.385 1.75h5.365a.75.75 0 0 1 0 1.5H9.385A2.501 2.501 0 0 1 4.615 5.75H2.25a.75.75 0 0 1 0-1.5h2.365A2.501 2.501 0 0 1 7 2.5ZM7 4a1 1 0 1 0 0 2 1 1 0 0 0 0-2Zm3 9.5a2.501 2.501 0 0 1-2.385-1.75H2.25a.75.75 0 0 1 0-1.5h5.365a2.501 2.501 0 0 1 4.77 0h2.365a.75.75 0 0 1 0 1.5h-2.365A2.501 2.501 0 0 1 10 13.5Zm0-1.5a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z"/></svg>
}
