import * as Popover from '@radix-ui/react-popover'
import { ArrowUpRight, CalendarDays, Check, ChevronDown, GitCommitHorizontal, Info } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { toast } from 'sonner'

import { IssueDescriptionEditor } from '@/components/issue/issue-description-editor'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { SettingsCrumb, SettingsPageTitle, SettingsRow, SettingsSection, SettingsToggle } from '@/components/settings/settings-primitives'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'
import { ViewGlyph } from '@/components/views/view-icon-picker'
import { useI18n } from '@/i18n/i18n'
import { createReleasePipeline, updateReleasePipeline, type ReleasePipelineMutation } from '@/lib/api'
import { newReleasePipelinePath } from '@/lib/app-routes'
import { canAdministerReleasePipeline, canCreateReleasePipeline } from '@/lib/settings-access'
import type { BootstrapData, ReleasePipeline, Team } from '@/types/flow'

import { PipelineAccessKeySection, PipelinePathFilters } from './pipeline-access-key'
import { PipelineDeleteDialog } from './pipeline-delete-dialog'
import { Subsection } from './pipeline-settings-subsection'
import { RELEASES_DOCS_URL, RELEASE_API_DOCS_URL, RELEASE_GITHUB_ACTIONS_DOCS_URL } from './pipeline-settings-model'
import { PipelineStageEditor } from './pipeline-stage-editor'
import { DEFAULT_STAGES, stageMutation, stagesFromPipeline, type StageDraft } from './pipeline-stages'
import './pipeline-editor.css'

/** Linear ReleasePipeline.maxNameLength. */
export const PIPELINE_NAME_MAX_LENGTH = 120

type Props = {
  data: BootstrapData
  pipeline?: ReleasePipeline
  /** Back to Settings › Releases. */
  onCancel: () => void
  /** After a pipeline is deleted (reload and go back to the list). */
  onSaved: (pipeline: ReleasePipeline) => Promise<void>
  /** After a new pipeline is created; Linear opens its settings page. */
  onCreated?: (pipeline: ReleasePipeline) => Promise<void>
  /** Reload workspace data after an autosaved settings change. */
  onChanged?: () => Promise<void>
}

/** Settings › Releases › New pipeline, or an existing pipeline's settings, built from the settings primitives. */
export function PipelineEditorPage(props: Props) {
  return props.pipeline ? <PipelineSettingsForm {...props} pipeline={props.pipeline}/> : <NewPipelineForm {...props}/>
}

function DocsLink() {
  const { t } = useI18n()
  return <a className="pipeline-settings-docs" href={RELEASES_DOCS_URL} target="_blank" rel="noreferrer">{t('Docs')}<ArrowUpRight aria-hidden/></a>
}

function nameError(name: string, attempted: boolean): string | undefined {
  const trimmed = name.trim()
  if (!attempted) return undefined
  if (!trimmed) return 'Name is required.'
  if ([...trimmed].length > PIPELINE_NAME_MAX_LENGTH) return 'Name cannot exceed 120 characters.'
  return undefined
}

function NewPipelineForm({ data, onCancel, onSaved, onCreated }: Props) {
  const { t } = useI18n()
  const location = useLocation()
  // Duplicate… opens this form with ?copyFrom=<slug>, like Linear's sourcePipeline.
  const source = useMemo(() => {
    const slug = new URLSearchParams(location.search).get('copyFrom')
    return slug ? data.releasePipelines.find(item => item.slugId === slug || item.id === slug) : undefined
  }, [data.releasePipelines, location.search])
  const [name, setName] = useState(source ? `${source.name} copy` : '')
  const [teamIds, setTeamIds] = useState<string[]>(source?.teamIds ?? [])
  const [production, setProduction] = useState(source?.production ?? true)
  const [type, setType] = useState<ReleasePipeline['type']>(source?.type ?? 'scheduled')
  const [stages, setStages] = useState<StageDraft[]>(() => source?.type === 'scheduled' ? stagesFromPipeline(source).map(stage => ({ ...stage, originalName: undefined })) : DEFAULT_STAGES.map(stage => ({ ...stage })))
  const [attempts, setAttempts] = useState(0)
  const [saving, setSaving] = useState(false)
  const error = nameError(name, attempts > 0)
  const valid = Boolean(name.trim()) && [...name.trim()].length <= PIPELINE_NAME_MAX_LENGTH
  const create = async () => {
    setAttempts(value => value + 1)
    if (!valid || saving || !canCreateReleasePipeline(data)) return
    setSaving(true)
    try {
      const { stageRenames: _renames, ...stageFields } = stageMutation(stages)
      void _renames
      const created = await createReleasePipeline({ name: name.trim(), teamIds, production, type, ...(type === 'scheduled' ? stageFields : {}) })
      await (onCreated ?? onSaved)(created)
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t('Could not create release pipeline'))
      setSaving(false)
    }
  }
  return <form className="pipeline-settings-page" aria-label={t('New release pipeline')} onSubmit={event => { event.preventDefault(); void create() }} onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void create() } }}>
    <SettingsCrumb pinned onClick={onCancel}>{t('Releases')}</SettingsCrumb>
    <SettingsPageTitle description={<>{t('Track the lifecycle of your releases.')} <DocsLink/></>}>{t('Create a new release pipeline')}</SettingsPageTitle>
    <SettingsSection title={t('General')}>
      <NameRow name={name} error={error} onChange={setName} autoFocus/>
      <TeamsRow data={data} teamIds={teamIds} onChange={setTeamIds}/>
      <SettingsRow title={t('Production')} description={t('Targets a production environment')}><SettingsToggle label={t('Production')} checked={production} onChange={setProduction}/></SettingsRow>
      <div className="settings-row pipeline-settings-type-row">
        <div className="settings-row-copy"><strong>{t('Type')}</strong><span>{t('Choose how releases are created and tracked in this pipeline')}</span></div>
        <TypeCards type={type} onChange={setType}/>
      </div>
    </SettingsSection>
    {type === 'scheduled' && <SettingsSection title={t('Stages')} description={t('Manage the stages that releases move through in this pipeline. Syncs won’t automatically add issues to frozen stages.')} grouped>
      <PipelineStageEditor stages={stages} onChange={setStages} releaseCountByStage={{}}/>
    </SettingsSection>}
    <footer className="pipeline-settings-actions">
      <button type="button" className="settings-action" disabled={saving} onClick={onCancel}>{t('Cancel')}</button>
      <button type="submit" className="settings-action primary" disabled={saving || !name.trim()}>{t(saving ? 'Creating…' : 'Create pipeline')}</button>
    </footer>
  </form>
}

function PipelineSettingsForm({ data, pipeline, onCancel, onSaved, onChanged }: Props & { pipeline: ReleasePipeline }) {
  const { t } = useI18n()
  const navigate = useNavigate()
  // Linear: settings are editable by workspace admins and by owners of every
  // team the pipeline belongs to; everyone else sees them read-only.
  const readOnly = !canAdministerReleasePipeline(data, pipeline)
  const canDuplicate = canCreateReleasePipeline(data)
  const [name, setName] = useState(pipeline.name)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [stages, setStages] = useState<StageDraft[]>(() => stagesFromPipeline(pipeline))
  const stageTimer = useRef<number>(undefined)
  const templateTimer = useRef<number>(undefined)
  const templateDraft = useRef(pipeline.releaseNotesTemplate ?? '')
  useEffect(() => { setName(pipeline.name) }, [pipeline.name])
  useEffect(() => () => { window.clearTimeout(stageTimer.current); window.clearTimeout(templateTimer.current) }, [])
  const reload = onChanged ?? (async () => {})
  // Linear autosaves every pipeline setting; there is no Save button.
  const save = async (input: ReleasePipelineMutation) => {
    if (readOnly) return undefined
    try {
      const updated = await updateReleasePipeline(pipeline.id, input)
      await reload()
      return updated
    } catch (cause) {
      toast.error(cause instanceof Error ? cause.message : t('Could not save release pipeline'))
      await reload()
      return undefined
    }
  }
  const commitName = () => {
    const trimmed = name.trim()
    if (!trimmed || [...trimmed].length > PIPELINE_NAME_MAX_LENGTH) { setName(pipeline.name); return }
    if (trimmed !== pipeline.name) void save({ name: trimmed })
  }
  const changeStages = (next: StageDraft[]) => {
    setStages(next)
    window.clearTimeout(stageTimer.current)
    stageTimer.current = window.setTimeout(() => {
      const sent = new Map(next.map(stage => [stage.key, stage.name]))
      void save(stageMutation(next)).then(updated => {
        if (!updated) { setStages(stagesFromPipeline(pipeline)); return }
        // Saved names become the originals the next rename is measured from.
        setStages(current => current.map(stage => sent.has(stage.key) ? { ...stage, originalName: sent.get(stage.key) } : stage))
      })
    }, 400)
  }
  // Linear saves the template 600ms after the last edit.
  const changeTemplate = (markdown: string) => {
    const value = markdown.trim() ? markdown : ''
    if (value === templateDraft.current) return
    templateDraft.current = value
    window.clearTimeout(templateTimer.current)
    templateTimer.current = window.setTimeout(() => { void save({ releaseNotesTemplate: value }) }, 600)
  }
  const releaseCountByStage = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const release of data.releases ?? []) if (release.pipelineId === pipeline.id && release.stage) counts[release.stage] = (counts[release.stage] ?? 0) + 1
    return counts
  }, [data.releases, pipeline.id])
  const menu = (canDuplicate || !readOnly) && <DropdownMenu>
    <DropdownMenuTrigger asChild><button type="button" className="pipeline-settings-icon-button" aria-label={t('Pipeline options')}><LinearGlyph name="ellipsis" size={16}/></button></DropdownMenuTrigger>
    <DropdownMenuContent align="end" className="flow-pipelines-settings-menu">
      {canDuplicate && <DropdownMenuItem onSelect={() => navigate(`${newReleasePipelinePath(data.workspace.urlKey)}?copyFrom=${encodeURIComponent(pipeline.slugId)}`)}><LinearGlyph name="copy" size={14}/><span>{t('Duplicate…')}</span></DropdownMenuItem>}
      {canDuplicate && !readOnly && <DropdownMenuSeparator/>}
      {!readOnly && <DropdownMenuItem onSelect={() => setDeleteOpen(true)}><LinearGlyph name="delete" size={14}/><span>{t('Delete')}</span></DropdownMenuItem>}
    </DropdownMenuContent>
  </DropdownMenu>
  return <div className="pipeline-settings-page" role="form" aria-label={t('Release pipeline settings')}>
    <SettingsCrumb pinned onClick={onCancel}>{t('Releases')}</SettingsCrumb>
    <SettingsPageTitle action={menu || undefined} description={<>{t('Configure the release pipeline.')} <DocsLink/></>}>
      <span className="pipeline-settings-title"><span data-i18n-ignore>{pipeline.name}</span><FlowTooltip label={t(pipeline.type === 'scheduled' ? 'Collect changes into a release and track its progress over time' : 'Create a new release automatically for each deploy')}><span className="pipeline-settings-type-badge">{t(pipeline.type === 'scheduled' ? 'Scheduled' : 'Continuous')}</span></FlowTooltip></span>
    </SettingsPageTitle>
    {readOnly && <p className="pipeline-settings-read-only" role="note">{t('Only admins and team owners can modify this pipeline')}</p>}
    <fieldset className="pipeline-settings-fieldset" disabled={readOnly}>
      <SettingsSection title={t('General')}>
        <NameRow name={name} error={nameError(name, name !== pipeline.name)} onChange={setName} onBlur={commitName}/>
        <TeamsRow data={data} teamIds={pipeline.teamIds} disabled={readOnly} onChange={teamIds => void save({ teamIds })}/>
        <SettingsRow title={t('Production')} description={t('Targets a production environment')}><SettingsToggle label={t('Production')} checked={pipeline.production} disabled={readOnly} onChange={production => void save({ production })}/></SettingsRow>
      </SettingsSection>
      {pipeline.type === 'scheduled' && <SettingsSection title={t('Stages')} description={t('Manage the stages that releases move through in this pipeline. Syncs won’t automatically add issues to frozen stages.')} grouped>
        <PipelineStageEditor stages={stages} onChange={changeStages} releaseCountByStage={releaseCountByStage}/>
      </SettingsSection>}
      {pipeline.type === 'scheduled' && <SettingsSection title={t('Completion')}>
        <SettingsRow title={t('Move open issues to the next release')} description={t('Turn off to leave open issues on a release when it completes')}><SettingsToggle label={t('Move open issues to the next release')} checked={pipeline.moveOpenIssuesToNextRelease ?? true} disabled={readOnly} onChange={value => void save({ moveOpenIssuesToNextRelease: value })}/></SettingsRow>
      </SettingsSection>}
      <SettingsSection title={t('Release notes')} grouped>
        <div className="settings-card">
          <SettingsRow title={t('Auto-generate on completion')} description={t('Automatically create a release note when a release is completed')}><SettingsToggle label={t('Auto-generate on completion')} checked={pipeline.autoGenerateReleaseNotes} disabled={readOnly} onChange={value => void save({ autoGenerateReleaseNotes: value })}/></SettingsRow>
        </div>
        <Subsection title={t('Template')} description={t('Define the template used when generating release notes. Include the sections you want; a {{issues}} line receives the list of completed issues.')}>
          <div className="pipeline-settings-template">
            <IssueDescriptionEditor
              key={pipeline.id}
              ariaLabel={t('Release notes template content')}
              className="pipeline-settings-template-editor"
              placeholder={t('e.g. New, Improvements, Fixes…')}
              readOnly={readOnly}
              users={data.users}
              value={pipeline.releaseNotesTemplate ?? ''}
              onChange={snapshot => changeTemplate(snapshot.markdown)}
            />
          </div>
        </Subsection>
      </SettingsSection>
      <SettingsSection title={t('CI setup')} description={t('Integrate your CI/CD pipeline to automatically track deployments and create releases.')} grouped>
        <div className="pipeline-settings-links">
          <a href={RELEASE_GITHUB_ACTIONS_DOCS_URL} target="_blank" rel="noreferrer">{t('GitHub Actions')}<ArrowUpRight aria-hidden/></a>
          <a href={RELEASE_API_DOCS_URL} target="_blank" rel="noreferrer">{t('Release API')}<ArrowUpRight aria-hidden/></a>
        </div>
        <PipelineAccessKeySection key={`access-key-${pipeline.id}`} pipeline={pipeline} disabled={readOnly} onChanged={reload}/>
        <PipelinePathFilters key={`path-filters-${pipeline.id}`} pipeline={pipeline} disabled={readOnly} onSave={pathFilters => void save({ pathFilters })}/>
      </SettingsSection>
    </fieldset>
    {deleteOpen && <PipelineDeleteDialog pipelines={[pipeline]} onClose={() => setDeleteOpen(false)} onDeleted={() => onSaved(pipeline)} onViewDeleted={() => navigate(`/${data.workspace.urlKey}/settings/releases?display=deleted`)}/>}
  </div>
}

function NameRow({ name, error, autoFocus = false, onChange, onBlur }: { name: string; error?: string; autoFocus?: boolean; onChange: (value: string) => void; onBlur?: () => void }) {
  const { t } = useI18n()
  return <SettingsRow title={<label htmlFor="pipeline-name">{t('Name')}</label>} description={error && <span className="pipeline-settings-error" role="alert">{t(error)}</span>}>
    <input id="pipeline-name" className="settings-input pipeline-settings-input" autoFocus={autoFocus} autoComplete="off" value={name} maxLength={PIPELINE_NAME_MAX_LENGTH + 20} placeholder={t('Pipeline name')} aria-invalid={Boolean(error) || undefined} onChange={event => onChange(event.target.value)} onBlur={onBlur} onKeyDown={event => { if (event.key === 'Escape') event.currentTarget.blur(); if (event.key === 'Enter' && onBlur) { event.preventDefault(); event.currentTarget.blur() } }}/>
  </SettingsRow>
}

function TypeCards({ type, onChange }: { type: ReleasePipeline['type']; onChange: (type: ReleasePipeline['type']) => void }) {
  const { t } = useI18n()
  const cards = [
    { value: 'scheduled' as const, icon: <CalendarDays/>, label: 'Scheduled', description: 'Collect changes into a release and track its progress over time', hint: 'Common for teams with a release cadence or mobile releases' },
    { value: 'continuous' as const, icon: <GitCommitHorizontal/>, label: 'Continuous', description: 'Create a new release automatically for each deploy', hint: 'Common for continuous delivery or frequent deploys' },
  ]
  return <div className="pipeline-settings-type-cards" role="radiogroup" aria-label={t('Type')}>
    {cards.map(card => <button key={card.value} type="button" role="radio" aria-checked={type === card.value} className={type === card.value ? 'is-selected' : ''} onClick={() => onChange(card.value)}>
      <span className="pipeline-settings-type-title">{card.icon}<b>{t(card.label)}</b></span>
      <span className="pipeline-settings-type-description">{t(card.description)}</span>
      <small>{t(card.hint)}</small>
    </button>)}
  </div>
}

/** Teams picker grouped like Linear: "Your teams", then "Other teams". */
function TeamsRow({ data, teamIds, disabled = false, onChange }: { data: BootstrapData; teamIds: string[]; disabled?: boolean; onChange: (teamIds: string[]) => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(teamIds)
  useEffect(() => { if (!open) setDraft(teamIds) }, [open, teamIds])
  const memberTeamIds = useMemo(() => new Set((data.teamMembers ?? []).filter(member => member.userId === data.viewer.id).map(member => member.teamId)), [data.teamMembers, data.viewer.id])
  const teams = useMemo(() => {
    const active = data.teams.filter(team => !team.archivedAt && !team.retiredAt)
    return [...active.filter(team => memberTeamIds.has(team.id)), ...active.filter(team => !memberTeamIds.has(team.id))]
  }, [data.teams, memberTeamIds])
  const options = useMemo(() => teams.map(team => ({ id: team.id, label: team.name, keywords: team.key })), [teams])
  const toggle = (id: string) => {
    const next = draft.includes(id) ? draft.filter(value => value !== id) : [...draft, id]
    setDraft(next)
    onChange(next)
  }
  const command = usePropertyCommand({ closeOnSelect: false, open, options, selectedIds: draft, onOpenChange: setOpen, onSelect: option => toggle(option.id) })
  const filtered = command.filteredOptions.map(option => teams.find(team => team.id === option.id)).filter((team): team is Team => Boolean(team))
  const selected = data.teams.filter(team => draft.includes(team.id))
  const groupOf = (team: Team) => memberTeamIds.has(team.id) ? 'Your teams' : 'Other teams'
  return <SettingsRow title={t('Teams')} description={<span className="pipeline-settings-hint">{t('Optionally set team ownership')}<FlowTooltip label={t('Teams set ownership and improve default behaviors like suggested releases. Issues from other teams can still be added to releases.')}><Info aria-label={t('More information')} tabIndex={0}/></FlowTooltip></span>}>
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Anchor asChild>
        <div className="pipeline-settings-team-trigger" data-disabled={disabled || undefined}>
          <input ref={command.inputRef} disabled={disabled} data-i18n-ignore={selected.length ? true : undefined} role="combobox" aria-label={t('Teams')} aria-expanded={open} aria-haspopup="listbox" aria-controls="flow-pipeline-team-options" aria-activedescendant={open && command.activeId ? `flow-pipeline-team-${command.activeId}` : undefined} autoComplete="off" spellCheck={false} placeholder={t('Select teams…')} value={open ? command.query : selected.map(team => team.name).join(', ')} onFocus={() => setOpen(true)} onClick={() => setOpen(true)} onChange={event => { command.onQueryChange(event.target.value); setOpen(true) }} onKeyDown={command.onKeyDown}/>
          <ChevronDown aria-hidden/>
        </div>
      </Popover.Anchor>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" className="flow-pipeline-team-menu" align="start" sideOffset={5} collisionPadding={12} onOpenAutoFocus={event => event.preventDefault()}>
          <div id="flow-pipeline-team-options" role="listbox" aria-label={t('Teams')} aria-multiselectable="true">
            {filtered.map((team, index) => <div key={team.id} role="presentation">
              {(index === 0 || groupOf(filtered[index - 1]) !== groupOf(team)) && memberTeamIds.size > 0 && <div className="flow-pipeline-team-group" role="presentation">{t(groupOf(team))}</div>}
              <button id={`flow-pipeline-team-${team.id}`} type="button" role="option" aria-selected={command.activeId === team.id} aria-checked={command.isSelected(team.id)} className={command.activeId === team.id ? 'active' : ''} onPointerMove={() => command.setActiveId(team.id)} onFocus={() => command.setActiveId(team.id)} onMouseDown={event => event.preventDefault()} onClick={() => command.choose(options.find(option => option.id === team.id)!)}>
                <span className="flow-pipeline-team-check">{draft.includes(team.id) && <Check/>}</span><i data-i18n-ignore><ViewGlyph color={team.color} icon={team.icon || 'Team'}/></i><strong data-i18n-ignore>{team.name}</strong>
              </button>
            </div>)}
            {!filtered.length && <p>{t('No teams found')}</p>}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  </SettingsRow>
}
