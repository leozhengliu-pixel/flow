import * as Popover from '@radix-ui/react-popover'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Dialog from '@radix-ui/react-dialog'
import {
  CalendarDays, Check, ChevronDown, ChevronRight, CircleCheck, CircleDashed, CircleDotDashed,
  CircleHelp, CircleX, Copy, KeyRound, MoreHorizontal, Plus, Repeat2, Trash2,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'

import { useI18n } from '@/i18n/i18n'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { Toggle } from '@/components/ui/toggle'
import { createReleasePipeline, deleteReleasePipeline, rotateReleasePipelineAccessKey, updateReleasePipeline } from '@/lib/api'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'
import './pipeline-editor.css'

type StageDraft = { name: string; status: Release['status'] }

const DEFAULT_STAGES: StageDraft[] = [
  { name: 'Planned', status: 'planned' },
  { name: 'In Progress', status: 'inProgress' },
  { name: 'Released', status: 'released' },
  { name: 'Canceled', status: 'canceled' },
]

export function PipelineEditorPage({ data, pipeline, onCancel, onSaved }: {
  data: BootstrapData
  pipeline?: ReleasePipeline
  onCancel: () => void
  onSaved: (pipeline: ReleasePipeline) => Promise<void>
}) {
  const { t } = useI18n()
  const [name, setName] = useState(pipeline?.name??'')
  const [teamIds, setTeamIds] = useState<string[]>(pipeline?.teamIds??[])
  const [teamOpen, setTeamOpen] = useState(false)
  const [production, setProduction] = useState(pipeline?.production??true)
  const [type, setType] = useState<ReleasePipeline['type']>(pipeline?.type??'scheduled')
  const [stages, setStages] = useState<StageDraft[]>(pipeline?.stages.map(value=>({name:value,status:pipeline.stageStatuses[value]??'planned'}))??DEFAULT_STAGES)
  const [moveOpenIssues,setMoveOpenIssues]=useState(pipeline?.moveOpenIssuesToNextRelease??true)
  const [autoNotes,setAutoNotes]=useState(pipeline?.autoGenerateReleaseNotes??false)
  const [notesTemplate,setNotesTemplate]=useState(pipeline?.releaseNotesTemplate??'')
  const [pathFilters,setPathFilters]=useState((pipeline?.pathFilters??[]).join('\n'))
  const [accessKey,setAccessKey]=useState<string>()
  const [deleteOpen,setDeleteOpen]=useState(false)
  const [addingStage, setAddingStage] = useState(false)
  const [stageName, setStageName] = useState('')
  const [saving, setSaving] = useState(false)
  const teamOptions = useMemo(() => data.teams.map(team => ({ id: team.id, label: team.name, keywords: team.key })), [data.teams])
  const selectedTeams = data.teams.filter(team => teamIds.includes(team.id))

  const toggleTeam = (id: string) => setTeamIds(current => current.includes(id) ? current.filter(value => value !== id) : [...current, id])
  const teamCommand = usePropertyCommand({ closeOnSelect: false, open: teamOpen, options: teamOptions, selectedIds: teamIds, onOpenChange: setTeamOpen, onSelect: option => toggleTeam(option.id) })
  const filteredTeams = teamCommand.filteredOptions.map(option => data.teams.find(team => team.id === option.id)!).filter(Boolean)
  const addStage = () => {
    const next = stageName.trim()
    if (!next || stages.some(stage => stage.name.toLowerCase() === next.toLowerCase())) return
    setStages(current => [...current.slice(0, -2), { name: next, status: 'inProgress' }, ...current.slice(-2)])
    setStageName('')
    setAddingStage(false)
  }
  const save = async () => {
    if (!name.trim() || saving) return
    setSaving(true)
    try {
      const stageStatuses = Object.fromEntries(stages.map(stage => [stage.name, stage.status])) as ReleasePipeline['stageStatuses']
      const input={
        name: name.trim(), teamIds, production, type,
        stages: stages.map(stage => stage.name), stageStatuses,
        moveOpenIssuesToNextRelease:moveOpenIssues,autoGenerateReleaseNotes:autoNotes,
        releaseNotesTemplate:notesTemplate,pathFilters:pathFilters.split('\n').map(value=>value.trim()).filter(Boolean),
      }
      await onSaved(pipeline?await updateReleasePipeline(pipeline.id,input):await createReleasePipeline(input))
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not create release pipeline'))
    } finally {
      setSaving(false)
    }
  }

  const duplicate=async()=>{if(!pipeline||saving)return;setSaving(true);try{await onSaved(await createReleasePipeline({name:`${pipeline.name} copy`,teamIds:pipeline.teamIds,type:pipeline.type,production:pipeline.production,stages:pipeline.stages,stageStatuses:pipeline.stageStatuses,moveOpenIssuesToNextRelease:pipeline.moveOpenIssuesToNextRelease??true,autoGenerateReleaseNotes:pipeline.autoGenerateReleaseNotes,releaseNotesTemplate:pipeline.releaseNotesTemplate,pathFilters:pipeline.pathFilters}))}catch(error){toast.error(error instanceof Error?error.message:t('Could not create release pipeline'))}finally{setSaving(false)}}
  const remove=async()=>{if(!pipeline||saving)return;setSaving(true);try{await deleteReleasePipeline(pipeline.id);await onSaved(pipeline)}catch(error){toast.error(error instanceof Error?error.message:t('Could not delete release pipeline'))}finally{setSaving(false)}}
  const generateKey=async()=>{if(!pipeline||saving)return;setSaving(true);try{const key=await rotateReleasePipelineAccessKey(pipeline.id);setAccessKey(key.secret)}catch(error){toast.error(error instanceof Error?error.message:t('Could not generate access key'))}finally{setSaving(false)}}
  return <form className="flow-pipeline-settings-editor" aria-label={t(pipeline?'Release pipeline settings':'New release pipeline')} onSubmit={event => { event.preventDefault(); void save() }}>
    <header className="flow-pipeline-settings-heading"><div><button type="button" onClick={onCancel}>{t('Releases')}</button><ChevronRight/><strong data-i18n-ignore>{pipeline?.name||t('Create a new release pipeline')}</strong>{pipeline&&<span>{t(pipeline.type==='scheduled'?'Scheduled':'Continuous')}</span>}</div>{pipeline&&<DropdownMenu.Root><DropdownMenu.Trigger asChild><button type="button" aria-label={t('Open menu')}><MoreHorizontal/></button></DropdownMenu.Trigger><DropdownMenu.Portal><DropdownMenu.Content className="flow-pipeline-settings-menu" align="end"><DropdownMenu.Item onSelect={()=>void duplicate()}><Copy/><span>{t('Duplicate…')}</span></DropdownMenu.Item><DropdownMenu.Separator/><DropdownMenu.Item className="danger" onSelect={()=>setDeleteOpen(true)}><Trash2/><span>{t('Delete')}</span></DropdownMenu.Item></DropdownMenu.Content></DropdownMenu.Portal></DropdownMenu.Root>}<p>{t(pipeline?'Configure the release pipeline.':'Track the lifecycle of your releases.')}</p></header>
    <section className="flow-pipeline-general" aria-labelledby="pipeline-general-heading">
      <h2 id="pipeline-general-heading">{t('General')}</h2>
      <div className="flow-pipeline-general-rows">
        <div className="flow-pipeline-setting-row">
          <label htmlFor="pipeline-name">{t('Name')}</label>
          <input id="pipeline-name" autoFocus value={name} onChange={event => setName(event.target.value)} placeholder={t('Pipeline name')}/>
        </div>

        <div className="flow-pipeline-setting-row flow-pipeline-team-row">
          <div className="flow-pipeline-setting-copy"><span>{t('Teams')}</span><small>{t('Optionally select the teams that own this pipeline.')}</small></div>
          <Popover.Root open={teamOpen} onOpenChange={setTeamOpen}>
            <Popover.Anchor asChild>
              <div className="flow-pipeline-team-trigger">
                <input ref={teamCommand.inputRef} data-i18n-ignore={selectedTeams.length ? true : undefined} role="combobox" aria-label={t('Teams')} aria-expanded={teamOpen} aria-haspopup="listbox" aria-controls="flow-pipeline-team-options" aria-activedescendant={teamOpen&&teamCommand.activeId?`flow-pipeline-team-${teamCommand.activeId}`:undefined} autoComplete="off" spellCheck={false} placeholder={t('Select teams…')} value={teamOpen?teamCommand.query:selectedTeams.map(team=>team.name).join(', ')} onFocus={()=>setTeamOpen(true)} onClick={()=>setTeamOpen(true)} onChange={event=>{teamCommand.onQueryChange(event.target.value);setTeamOpen(true)}} onKeyDown={teamCommand.onKeyDown}/>
                <ChevronDown/>
              </div>
            </Popover.Anchor>
            <Popover.Portal>
              <Popover.Content className="flow-pipeline-team-menu" align="start" sideOffset={5} collisionPadding={12} onOpenAutoFocus={event => event.preventDefault()}>
                <div id="flow-pipeline-team-options" role="listbox" aria-label={t('Teams')} aria-multiselectable="true">
                  {filteredTeams.map(team => <button id={`flow-pipeline-team-${team.id}`} type="button" role="option" aria-selected={teamCommand.activeId===team.id} aria-checked={teamCommand.isSelected(team.id)} className={teamCommand.activeId===team.id?'active':''} key={team.id} onPointerMove={()=>teamCommand.setActiveId(team.id)} onFocus={()=>teamCommand.setActiveId(team.id)} onMouseDown={event=>event.preventDefault()} onClick={()=>teamCommand.choose(teamOptions.find(option=>option.id===team.id)!)}>
                    <span className="flow-pipeline-team-check">{teamIds.includes(team.id) && <Check/>}</span><i style={{ color: team.color }} data-i18n-ignore>{team.icon || team.key.slice(0, 1)}</i><strong data-i18n-ignore>{team.name}</strong>
                  </button>)}
                  {!filteredTeams.length && <p>{t('No teams found')}</p>}
                </div>
              </Popover.Content>
            </Popover.Portal>
          </Popover.Root>
        </div>

        <div className="flow-pipeline-setting-row">
          <div className="flow-pipeline-setting-copy"><span>{t('Production')}</span><small>{t('Targets a production environment')}</small></div>
          <button type="button" className="flow-pipeline-switch" role="switch" aria-label={t('Production')} aria-checked={production} onClick={() => setProduction(value => !value)}><span/></button>
        </div>
      </div>

      {!pipeline&&<><div className="flow-pipeline-type-heading"><span>{t('Type')}</span><small>{t('Choose how releases are created in this pipeline.')}</small></div>
      <div className="flow-pipeline-type-cards" role="radiogroup" aria-label={t('Type')}>
        <button type="button" role="radio" aria-checked={type === 'scheduled'} className={type === 'scheduled' ? 'selected' : ''} onClick={() => setType('scheduled')}>
          <span className="flow-pipeline-type-icon"><CalendarDays/></span><strong>{t('Scheduled')}</strong><small>{t('Plan releases around target dates and ordered stages.')}</small>{type === 'scheduled' && <Check className="flow-pipeline-type-check"/>}
        </button>
        <button type="button" role="radio" aria-checked={type === 'continuous'} className={type === 'continuous' ? 'selected' : ''} onClick={() => setType('continuous')}>
          <span className="flow-pipeline-type-icon"><Repeat2/></span><strong>{t('Continuous')}</strong><small>{t('Create releases continuously from your delivery workflow.')}</small>{type === 'continuous' && <Check className="flow-pipeline-type-check"/>}
        </button>
      </div></>}
    </section>

    {type==='scheduled'&&<section className="flow-pipeline-stages-section" aria-labelledby="pipeline-stages-heading">
      <header><h2 id="pipeline-stages-heading">{t('Stages')}</h2><p>{t('Manage the stages that releases move through in this pipeline. Syncs won’t automatically add issues to frozen stages.')}</p></header>
      <div className="flow-pipeline-stage-list">
        <StageRow stage={stages[0]}/>
        <div className="flow-pipeline-started-label"><span>{t('Started')}</span><button type="button" aria-label={t('Add stage')} title={t('Add stage')} onClick={() => setAddingStage(true)}><Plus/></button><span className="flow-pipeline-stage-help" title={t('Stages between planned and released are considered started.')}><CircleHelp/></span></div>
        {stages.slice(1, -2).map(stage => <StageRow inset key={stage.name} stage={stage}/>)}
        {addingStage && <div className="flow-pipeline-stage-edit-row">
          <span className="flow-pipeline-stage-edit-icon"><CircleDotDashed/></span>
          <input autoFocus value={stageName} onChange={event => setStageName(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); addStage() } if (event.key === 'Escape') { event.preventDefault(); setAddingStage(false); setStageName('') } }} placeholder={t('Stage name')}/>
          <div><button type="button" onClick={() => { setAddingStage(false); setStageName('') }}>{t('Cancel')}</button><button type="button" className="primary" disabled={!stageName.trim() || stages.some(stage => stage.name.toLowerCase() === stageName.trim().toLowerCase())} onClick={addStage}>{t('Create')}</button></div>
        </div>}
        <StageRow stage={stages.at(-2)!}/>
        <StageRow stage={stages.at(-1)!}/>
      </div>
    </section>}

    {type==='scheduled'&&<section className="flow-pipeline-settings-section"><header><h2>{t('Completion')}</h2></header><label className="flow-pipeline-setting-toggle"><span><strong>{t('Move open issues to the next release')}</strong><small>{t('Turn off to leave open issues on a release when it completes')}</small></span><div className="flow-pipeline-toggle-control"><Toggle checked={moveOpenIssues} label={t('Move open issues to the next release')} onChange={setMoveOpenIssues} size="regular"/></div></label></section>}
    <section className="flow-pipeline-settings-section"><header><h2>{t('Release notes')}</h2></header><label className="flow-pipeline-setting-toggle"><span><strong>{t('Auto-generate on completion')}</strong><small>{t('Automatically create a release note when a release is completed')}</small></span><div className="flow-pipeline-toggle-control"><Toggle checked={autoNotes} label={t('Auto-generate on completion')} onChange={setAutoNotes} size="regular"/></div></label><label className="flow-pipeline-template-field"><strong>{t('Template')}</strong><small>{t('Define the template used when generating release notes.')}</small><textarea aria-label={t('Release notes template content')} value={notesTemplate} onChange={event=>setNotesTemplate(event.target.value)} placeholder={t('e.g. New, Improvements, Fixes…')}/></label></section>
    <section className="flow-pipeline-settings-section"><header><h2>{t('CI setup')}</h2><p>{t('Integrate your CI/CD pipeline to automatically track deployments and create releases.')}</p></header><div className="flow-pipeline-ci-links"><a href="https://github.com/leozhengliu-pixel/flow/blob/main/docs/release-automation.md" target="_blank" rel="noreferrer">{t('Flow API guide')}</a></div><div className="flow-pipeline-access-key"><KeyRound/><div><strong>{t('Access key')}</strong><small>{accessKey?<span data-i18n-ignore>{accessKey}</span>:pipeline?.accessKeyPrefix?<span data-i18n-ignore>{pipeline.accessKeyPrefix}…</span>:t('No access key has been generated yet.')}</small></div><button type="button" disabled={!pipeline||saving} onClick={()=>void generateKey()}>{t(pipeline?.accessKeyPrefix?'Regenerate access key':'Generate access key')}</button></div><label className="flow-pipeline-template-field"><strong>{t('Path filters')}</strong><small>{t('Filter releases to only include commits affecting specific paths.')}</small><textarea disabled={!pipeline?.accessKeyPrefix&&!accessKey} value={pathFilters} onChange={event=>setPathFilters(event.target.value)} placeholder={'frontend/**\npackages/api/**'}/></label></section>

    <footer className="flow-pipeline-settings-actions"><button type="button" disabled={saving} onClick={onCancel}>{t('Cancel')}</button><button type="submit" className="primary" disabled={saving || !name.trim()}>{t(saving ? 'Saving…' : pipeline?'Save changes':'Create pipeline')}</button></footer>
    {pipeline&&<Dialog.Root open={deleteOpen} onOpenChange={setDeleteOpen}><Dialog.Portal><Dialog.Overlay className="flow-pipeline-delete-overlay"/><Dialog.Content aria-describedby={undefined} className="flow-pipeline-delete-dialog"><Dialog.Title>{t('Delete release pipeline')}</Dialog.Title><p>{t('This moves the release pipeline to recently deleted.')} <strong data-i18n-ignore>{pipeline.name}</strong></p><footer><Dialog.Close>{t('Cancel')}</Dialog.Close><button type="button" className="danger" disabled={saving} onClick={()=>void remove()}>{t('Delete')}</button></footer></Dialog.Content></Dialog.Portal></Dialog.Root>}
  </form>
}

function StageRow({ stage, inset = false }: { stage: StageDraft; inset?: boolean }) {
  const Icon = stage.status === 'planned' ? CircleDashed : stage.status === 'inProgress' ? CircleDotDashed : stage.status === 'released' ? CircleCheck : CircleX
  return <div className={`flow-pipeline-stage-row status-${stage.status}${inset ? ' inset' : ''}`}><div><Icon/><strong data-i18n-ignore>{stage.name}</strong></div></div>
}
