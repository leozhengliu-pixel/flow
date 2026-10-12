import { useId, useState } from 'react'
import type { Team } from '@/types/flow'
import { useI18n } from '@/i18n/i18n'
import './application-team-access.css'

type Access = { allPublicTeams: boolean; teamIds: string[] }
export function ApplicationTeamAccess({ teams, value, onChange }: { teams: Team[]; value: Access; onChange: (value: Access) => void }) {
  const { t } = useI18n()
  const name = useId()
  const [mode, setMode] = useState(value.allPublicTeams ? value.teamIds.length ? 'mixed' : 'public' : 'selected')
  const options = [{ value: 'public', label: 'All public teams' }, { value: 'mixed', label: 'All public teams and select other teams…' }, { value: 'selected', label: 'Only select teams…' }]
  return <fieldset className="application-team-access"><legend>{t('Team access')}</legend>
    {options.map(option => <label key={option.value}><input type="radio" name={name} checked={mode === option.value} onChange={() => {
      setMode(option.value)
      onChange({ allPublicTeams: option.value !== 'selected', teamIds: option.value === 'public' ? [] : option.value === 'mixed' ? value.teamIds.filter(id => teams.find(team => team.id === id)?.private) : value.teamIds })
    }}/>{t(option.label)}</label>)}
    {mode !== 'public' && <div className="application-team-options">{teams.filter(team => !team.retiredAt && (mode !== 'mixed' || team.private)).map(team => <label key={team.id}><input type="checkbox" checked={value.teamIds.includes(team.id)} onChange={event => onChange({ allPublicTeams: mode === 'mixed', teamIds: event.target.checked ? [...value.teamIds, team.id] : value.teamIds.filter(id => id !== team.id) })}/><span data-i18n-ignore>{team.name}</span></label>)}</div>}
  </fieldset>
}
