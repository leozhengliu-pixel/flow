import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as Popover from '@radix-ui/react-popover'
import { Lock, MoreHorizontal, Pencil, Plus, Trash2 } from 'lucide-react'
import { useEffect, useRef, useState, type DragEvent, type ReactNode } from 'react'
import { toast } from 'sonner'

import { useI18n } from '@/i18n/i18n'
import { FLOW_COLOR_PALETTE } from '@/components/ui/color-palette'
import { ScopedFlowTooltip as FlowTooltip } from '@/components/ui/tooltip'

import { ReleasesIcon, ReleaseStatusIcon } from './release-icons'

import { DEFAULT_STARTED_STAGE_COLOR, nextStageKey, NAME_MAX_LENGTH, STATUS_ORDER, TYPE_LABELS, type StageDraft, type StageStatus } from './pipeline-stages'

type EditState = { key: string; name: string; color: string; isNew: boolean }

/**
 * Linear's release pipeline stage editor (Settings › Releases › pipeline ›
 * Stages): fixed Planned row, a "Started" group whose stages can be created,
 * edited, recolored, frozen, reordered and deleted, then Released and Canceled.
 */
export function PipelineStageEditor({ stages, onChange, releaseCountByStage }: {
  stages: StageDraft[]
  onChange: (stages: StageDraft[]) => void
  /** Releases per persisted stage name; such stages can't be deleted. */
  releaseCountByStage: Record<string, number>
}) {
  const { t } = useI18n()
  const [edit, setEdit] = useState<EditState>()
  const [dragKey, setDragKey] = useState<string>()
  const started = stages.filter(stage => stage.status === 'inProgress')
  const byStatus = (status: StageStatus) => stages.filter(stage => stage.status === status)

  const replaceStarted = (next: StageDraft[]) => onChange(STATUS_ORDER.flatMap(status => status === 'inProgress' ? next : byStatus(status)))
  const nameTaken = (name: string, key: string) => stages.some(stage => stage.key !== key && stage.name.trim().toLowerCase() === name.trim().toLowerCase())

  const startCreate = () => setEdit({ key: nextStageKey(), name: '', color: DEFAULT_STARTED_STAGE_COLOR, isNew: true })
  const submit = () => {
    if (!edit) return
    const name = edit.name.trim()
    if (!name || name.length > NAME_MAX_LENGTH || nameTaken(name, edit.key)) return
    if (edit.isNew) replaceStarted([...started, { key: edit.key, name, status: 'inProgress', color: edit.color, frozen: false }])
    else replaceStarted(started.map(stage => stage.key === edit.key ? { ...stage, name, color: edit.color } : stage))
    setEdit(undefined)
  }
  const remove = (stage: StageDraft) => {
    if (started.length <= 1) {
      toast.warning(t('Can’t delete the "{name}" release stage').replace('{name}', stage.name || t('unnamed')), { description: t('Each type of release stage must have at least one option. You can edit this stage, or create a replacement stage prior to deleting it.') })
      return
    }
    if (stage.originalName && (releaseCountByStage[stage.originalName] ?? 0) > 0) {
      toast.warning(t('Can’t archive the "{name}" release stage').replace('{name}', stage.name), { description: t('This stage has releases associated with it. You can edit this stage, or move the releases to a different stage prior to archiving it.') })
      return
    }
    if (!stage.frozen && !started.some(other => other.key !== stage.key && !other.frozen)) {
      toast.warning(t('Can’t archive the "{name}" release stage').replace('{name}', stage.name), { description: t('At least one started stage must remain non-frozen.') })
      return
    }
    replaceStarted(started.filter(other => other.key !== stage.key))
  }
  const toggleFrozen = (stage: StageDraft) => replaceStarted(started.map(other => other.key === stage.key ? { ...other, frozen: !other.frozen } : other))
  const onDragOver = (event: DragEvent, target: StageDraft) => {
    if (!dragKey || dragKey === target.key) return
    event.preventDefault()
    const from = started.findIndex(stage => stage.key === dragKey)
    const to = started.findIndex(stage => stage.key === target.key)
    if (from < 0 || to < 0) return
    const next = [...started]
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    replaceStarted(next)
  }

  return <div className="flow-pipeline-stage-card" role="list" aria-label={t('Stages')}>
    {byStatus('planned').map(stage => <FixedStageRow key={stage.key} stage={stage}/>)}
    <div className="flow-pipeline-started-group" role="group" aria-label={t('Started')}>
      <div className="flow-pipeline-started-header">
        <span className="flow-pipeline-stage-label"><ReleasesIcon className="flow-pipeline-started-icon"/><span>{t('Started')}</span></span>
        <FlowTooltip label={t('Create new release stage')}>
          <button type="button" className="flow-pipeline-stage-icon-button" aria-label={t('Create new release stage')} disabled={Boolean(edit)} onClick={startCreate}><Plus/></button>
        </FlowTooltip>
      </div>
      {started.map(stage => edit && !edit.isNew && edit.key === stage.key
        ? <StageEditRow key={stage.key} edit={edit} taken={nameTaken(edit.name, edit.key)} onChange={setEdit} onCancel={() => setEdit(undefined)} onSubmit={submit}/>
        : <StartedStageRow
          key={stage.key}
          stage={stage}
          dragging={dragKey === stage.key}
          disabled={Boolean(edit)}
          canFreeze={stage.frozen || started.some(other => other.key !== stage.key && !other.frozen)}
          onDragStart={() => setDragKey(stage.key)}
          onDragEnd={() => setDragKey(undefined)}
          onDragOver={event => onDragOver(event, stage)}
          onEdit={() => setEdit({ key: stage.key, name: stage.name, color: stage.color ?? DEFAULT_STARTED_STAGE_COLOR, isNew: false })}
          onToggleFrozen={() => toggleFrozen(stage)}
          onDelete={() => remove(stage)}
        />)}
      {edit?.isNew && <StageEditRow isNew edit={edit} taken={nameTaken(edit.name, edit.key)} onChange={setEdit} onCancel={() => setEdit(undefined)} onSubmit={submit}/>}
    </div>
    {byStatus('released').map(stage => <FixedStageRow key={stage.key} stage={stage}/>)}
    {byStatus('canceled').map(stage => <FixedStageRow key={stage.key} stage={stage}/>)}
  </div>
}

function StageName({ name }: { name: string }) {
  const { t } = useI18n()
  return TYPE_LABELS.has(name) ? <span>{t(name)}</span> : <span data-i18n-ignore>{name}</span>
}

function FixedStageRow({ stage }: { stage: StageDraft }) {
  return <div className={`flow-pipeline-stage-row status-${stage.status}`} role="listitem">
    <span className="flow-pipeline-stage-label"><ReleaseStatusIcon status={stage.status}/><StageName name={stage.name}/></span>
  </div>
}

function StartedStageRow({ stage, dragging, disabled, canFreeze, onDragStart, onDragEnd, onDragOver, onEdit, onToggleFrozen, onDelete }: {
  stage: StageDraft
  dragging: boolean
  disabled: boolean
  canFreeze: boolean
  onDragStart: () => void
  onDragEnd: () => void
  onDragOver: (event: DragEvent) => void
  onEdit: () => void
  onToggleFrozen: () => void
  onDelete: () => void
}) {
  const { t } = useI18n()
  const [menuOpen, setMenuOpen] = useState(false)
  return <div
    className={`flow-pipeline-stage-row is-started${dragging ? ' is-dragging' : ''}${menuOpen ? ' is-menu-open' : ''}`}
    role="listitem"
    draggable={!disabled}
    onDragStart={event => { event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', stage.name); onDragStart() }}
    onDragEnd={onDragEnd}
    onDragOver={onDragOver}
    onDrop={event => event.preventDefault()}
  >
    <span className="flow-pipeline-stage-label">
      <ReleaseStatusIcon status="inProgress" color={stage.color ?? DEFAULT_STARTED_STAGE_COLOR}/>
      <StageName name={stage.name}/>
      {stage.frozen && <FlowTooltip label={t('Syncs won’t automatically add issues to this stage')}><span className="flow-pipeline-frozen-badge">{t('Frozen')}</span></FlowTooltip>}
    </span>
    {!disabled && <DropdownMenu.Root open={menuOpen} onOpenChange={setMenuOpen}>
      <DropdownMenu.Trigger asChild><button type="button" className="flow-pipeline-stage-icon-button flow-pipeline-stage-menu-trigger" aria-label={t('Open menu')}><MoreHorizontal/></button></DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content data-flow-motion="floating" className="flow-pipeline-settings-menu flow-pipeline-stage-menu" align="end" sideOffset={4}>
          <DropdownMenu.Item onSelect={onEdit}><Pencil/><span>{t('Edit')}</span></DropdownMenu.Item>
          <MenuItemWithReason disabled={!canFreeze} reason={t('At least one started stage must remain non-frozen.')} onSelect={onToggleFrozen}><Lock/><span>{t(stage.frozen ? 'Unfreeze' : 'Freeze')}</span></MenuItemWithReason>
          <DropdownMenu.Item onSelect={onDelete}><Trash2/><span>{t('Delete')}</span></DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>}
  </div>
}

/** A disabled menu item still explains itself on hover, like Linear's disabledReason. */
function MenuItemWithReason({ disabled, reason, onSelect, children }: { disabled: boolean; reason: string; onSelect: () => void; children: ReactNode }) {
  const item = <DropdownMenu.Item disabled={disabled} onSelect={onSelect}>{children}</DropdownMenu.Item>
  return disabled ? <FlowTooltip label={reason} side="right">{<div className="flow-pipeline-menu-reason">{item}</div>}</FlowTooltip> : item
}

function StageEditRow({ edit, isNew = false, taken, onChange, onCancel, onSubmit }: {
  edit: EditState
  isNew?: boolean
  taken: boolean
  onChange: (edit: EditState) => void
  onCancel: () => void
  onSubmit: () => void
}) {
  const { t } = useI18n()
  const inputRef = useRef<HTMLInputElement>(null)
  useEffect(() => { inputRef.current?.focus() }, [])
  const valid = Boolean(edit.name.trim()) && edit.name.trim().length <= NAME_MAX_LENGTH && !taken
  return <div className={`flow-pipeline-stage-row flow-pipeline-stage-edit${isNew ? ' is-new' : ''}`} role="listitem" onKeyDown={event => {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onCancel() }
  }}>
    <StageColorPicker color={edit.color} onChange={color => onChange({ ...edit, color })}/>
    <input
      ref={inputRef}
      aria-label={t('Name')}
      autoComplete="off"
      maxLength={NAME_MAX_LENGTH}
      placeholder={t('Name')}
      value={edit.name}
      onChange={event => onChange({ ...edit, name: event.target.value })}
      onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); onSubmit() } }}
    />
    <div className="flow-pipeline-stage-edit-actions">
      <button type="button" aria-label={t('Cancel')} onClick={onCancel}>{t('Cancel')}</button>
      <button type="button" className="primary" aria-label={t('Submit')} disabled={!valid} onClick={onSubmit}>{t(edit.isNew ? 'Create' : 'Save')}</button>
    </div>
  </div>
}

function StageColorPicker({ color, onChange }: { color: string; onChange: (color: string) => void }) {
  const { t } = useI18n()
  const customColorRef = useRef<HTMLInputElement>(null)
  const normalized = color.toLowerCase()
  const preset = FLOW_COLOR_PALETTE.some(option => option.value === normalized)
  return <Popover.Root>
    <Popover.Trigger asChild><button type="button" className="flow-pipeline-stage-swatch" aria-label={t('Stage color')}><ReleaseStatusIcon status="inProgress" color={color}/></button></Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating" data-i18n-ignore className="domain-label-color-popover" align="center" side="bottom" sideOffset={4} collisionPadding={8} onCloseAutoFocus={event => event.preventDefault()}>
        <div className="domain-label-color-presets">{FLOW_COLOR_PALETTE.map(option => <Popover.Close asChild key={option.value}><button aria-label={t(option.name)} data-selected={normalized === option.value} onClick={() => onChange(option.value)} style={{ color: option.value }} type="button"><span style={{ background: option.value }}/></button></Popover.Close>)}</div>
        <button aria-label={t('Set custom color')} className="domain-label-custom-color" data-selected={!preset} onClick={() => customColorRef.current?.click()} type="button"><span/>{!preset && <i/>}</button>
        <input aria-hidden="true" className="domain-label-native-color" tabIndex={-1} type="color" ref={customColorRef} value={/^#[0-9a-f]{6}$/i.test(color) ? color : DEFAULT_STARTED_STAGE_COLOR} onChange={event => onChange(event.target.value.toLowerCase())}/>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}
