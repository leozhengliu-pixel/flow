import { Check, ChevronLeft, Plus, Rocket, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { createReleasePipeline } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, ReleasePipeline } from '@/types/flow'

export function PipelineEditorPage({ data, onCancel, onCreated, onOpenSidebar }: {
  data: BootstrapData
  onCancel: () => void
  onCreated: (pipeline: ReleasePipeline) => Promise<void>
  onOpenSidebar: () => void
}) {
  const { t } = useI18n()
  const [name, setName] = useState('')
  const [teamIds, setTeamIds] = useState<string[]>([])
  const [production, setProduction] = useState(true)
  const [type, setType] = useState<ReleasePipeline['type']>('scheduled')
  const [stages, setStages] = useState(['Planned', 'In Progress', 'Released', 'Canceled'])
  const [stageDraft, setStageDraft] = useState<string>()
  const [saving, setSaving] = useState(false)
  const toggleTeam = (id: string) => setTeamIds(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])
  const createStage = () => {
    const next = stageDraft?.trim()
    if (!next || stages.includes(next)) return
    setStages(current => [...current, next])
    setStageDraft(undefined)
  }
  const save = async () => {
    setSaving(true)
    try {
      const stageStatuses = Object.fromEntries(stages.map(stage => [stage, stage === 'In Progress' ? 'inProgress' : stage === 'Released' ? 'released' : stage === 'Canceled' ? 'canceled' : 'planned'])) as ReleasePipeline['stageStatuses']
      await onCreated(await createReleasePipeline({ name: name.trim(), teamIds, production, type, stages, stageStatuses }))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not create release pipeline'))
    } finally { setSaving(false) }
  }
  return <main className="main-panel flow-pipeline-editor-page" aria-label={t('New release pipeline')}>
    <header className="flow-releases-topbar"><button className="flow-releases-mobile-menu" aria-label={t('Open sidebar')} onClick={onOpenSidebar}><Rocket/></button><button className="flow-pipeline-editor-back" onClick={onCancel}><ChevronLeft/>{t('Releases')}</button><h1>{t('New pipeline')}</h1></header>
    <div className="flow-pipeline-editor-content">
      <section><header><h2>{t('General')}</h2></header><div className="flow-pipeline-editor-form">
        <label><span>{t('Name')}</span><input autoFocus value={name} onChange={event => setName(event.target.value)} placeholder={t('Pipeline name')}/></label>
        <fieldset><legend>{t('Teams')}</legend><div className="flow-pipeline-team-picker">{data.teams.map(team => <button aria-checked={teamIds.includes(team.id)} key={team.id} onClick={() => toggleTeam(team.id)} role="checkbox"><span>{teamIds.includes(team.id) && <Check/>}</span><i style={{ background: team.color }} data-i18n-ignore>{team.key.slice(0, 2)}</i><strong data-i18n-ignore>{team.name}</strong></button>)}</div><small>{teamIds.length ? t(`${teamIds.length} teams`) : t('All teams')}</small></fieldset>
        <label className="flow-pipeline-production"><input checked={production} onChange={event => setProduction(event.target.checked)} type="checkbox"/><span><strong>{t('Production')}</strong><small>{t('Mark this pipeline as a production release pipeline.')}</small></span></label>
        <fieldset className="flow-pipeline-type"><legend>{t('Type')}</legend><div>{(['scheduled','continuous'] as const).map(value => <button aria-checked={type === value} className={type === value ? 'active' : ''} key={value} onClick={() => setType(value)} role="radio"><span><Rocket/></span><strong>{t(value === 'scheduled' ? 'Scheduled' : 'Continuous')}</strong><small>{t(value === 'scheduled' ? 'Plan releases around target dates and ordered stages.' : 'Create releases continuously from your delivery workflow.')}</small>{type === value && <Check/>}</button>)}</div></fieldset>
      </div></section>
      <section><header><h2>{t('Stages')}</h2><p>{t('Define the ordered steps every release moves through.')}</p></header><div className="flow-pipeline-stages">{stages.map((stage, index) => <div key={`${stage}-${index}`}><span>{index + 1}</span><strong data-i18n-ignore>{stage}</strong><button aria-label={`${t('Remove')} ${stage}`} disabled={stages.length === 1} onClick={() => setStages(current => current.filter((_, itemIndex) => itemIndex !== index))}><Trash2/></button></div>)}{stageDraft === undefined ? <button className="flow-pipeline-add-stage" onClick={() => setStageDraft('')}><Plus/>{t('Add stage')}</button> : <div className="flow-pipeline-stage-editor"><input autoFocus value={stageDraft} onChange={event => setStageDraft(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') createStage(); if (event.key === 'Escape') setStageDraft(undefined) }} placeholder={t('Stage name')}/><button onClick={() => setStageDraft(undefined)}>{t('Cancel')}</button><button className="primary" disabled={!stageDraft.trim() || stages.includes(stageDraft.trim())} onClick={createStage}>{t('Create')}</button></div>}</div></section>
    </div>
    <footer className="flow-pipeline-editor-footer"><button disabled={saving} onClick={onCancel}>{t('Cancel')}</button><button className="primary" disabled={saving || !name.trim() || !stages.length} onClick={() => void save()}>{t(saving ? 'Creating…' : 'Create pipeline')}</button></footer>
  </main>
}
