import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { useState } from 'react'
import { toast } from '@/lib/toast'

import { MentionTextField } from '@/components/editor/mention-text-field'
import { FlowGlyph } from '@/components/ui/menu-glyphs'
import { FlowDropdownMenuContent } from '@/components/ui/row-context-menu'
import { createRelease, updateRelease } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

import { ReleaseDateMenu } from './release-date-menu'
import { ReleaseStatusIcon } from './release-icons'
import { ReleaseStageOptions } from './release-menus'
import { releaseStatusForStage } from './release-view-model'

type Props = {
  data: BootstrapData
  pipeline: ReleasePipeline
  release?: Release
  onClose: () => void
  onSaved: () => Promise<void>
  /** "View release" in the "Release created" toast. */
  onOpenRelease?: (release: Release) => void
}

/** The reference app's New release / Edit release composer (pipeline › New release, name, version, description, stage and target date chips). */
export function ReleaseEditorDialog({ pipeline, release, onClose, onSaved, onOpenRelease }: Props) {
  const { t, formatDate } = useI18n()
  const [name, setName] = useState(release?.name ?? '')
  const [version, setVersion] = useState(release?.version ?? '')
  const [description, setDescription] = useState(release?.description ?? '')
  const [stage, setStage] = useState(release?.stage || pipeline.stages[0] || '')
  const [targetDate, setTargetDate] = useState(release?.targetDate ?? '')
  const [saving, setSaving] = useState(false)
  const [stageOpen, setStageOpen] = useState(false)
  const scheduled = pipeline.type === 'scheduled'
  const valid = Boolean(name.trim()) && (!scheduled || Boolean(stage))
  const save = async () => {
    if (!valid || saving) return
    setSaving(true)
    try {
      const stageChanged = !release || release.stage !== stage
      const input = { name: name.trim(), version: version.trim(), description, pipelineId: pipeline.id, stage, ...(stageChanged ? { status: releaseStatusForStage(pipeline, stage, release?.status) } : {}), targetDate }
      if (release) await updateRelease(release.id, input)
      else {
        const created = await createRelease(input)
        toast.success(t('Release created'), {
          entity: { icon: <ReleaseStatusIcon size={16} status={releaseStatusForStage(pipeline, input.stage, 'planned')}/>, title: [created?.name ?? input.name, input.version].filter(Boolean).join(' – '), onClick: onOpenRelease && created ? () => onOpenRelease(created) : undefined },
          action: onOpenRelease && created ? { label: t('View release'), onClick: () => onOpenRelease(created) } : undefined,
        })
      }
      await onSaved()
      onClose()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save release'))
    } finally { setSaving(false) }
  }
  return <Dialog.Root open onOpenChange={open => { if (!open && !saving) onClose() }}>
    <Dialog.Portal>
      <Dialog.Overlay data-flow-motion="backdrop" className="flow-release-dialog-overlay is-clear"/>
      <Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="flow-release-editor" onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void save() } }}>
        <Dialog.Title className="flow-release-editor__title"><span className="flow-release-editor__crumbs"><span data-i18n-ignore>{pipeline.name}</span><span className="flow-release-editor__separator" aria-hidden="true">›</span><span>{t(release ? 'Edit release' : 'New release')}</span></span></Dialog.Title>
        <Dialog.Close className="flow-release-editor__close" aria-label={t('Discard')}><FlowGlyph name="closeChat" width={14} height={14}/></Dialog.Close>
        <div className="flow-release-editor__copy">
          <div className="flow-release-editor__name-slot"><input aria-label={t('Release name')} autoFocus className="flow-release-editor__name" value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) { event.preventDefault(); const next = event.currentTarget.closest('.flow-release-editor__copy')?.querySelector<HTMLElement>('.flow-release-editor__version'); next?.focus() } }} placeholder={t('Release name')}/></div>
          <div className="flow-release-editor__version-slot"><input aria-label={t('Release version')} className="flow-release-editor__version" value={version} onChange={event => setVersion(event.target.value)} placeholder={t('Version')}/></div>
          <MentionTextField ariaLabel={t('Release description')} className="flow-release-editor__description" onChange={setDescription} onSubmit={() => void save()} placeholder={t('Add description…')} value={description}/>
        </div>
        <div className="flow-release-editor__properties">
          {scheduled && <DropdownMenu.Root open={stageOpen} onOpenChange={setStageOpen}>
            <DropdownMenu.Trigger asChild><button className="flow-release-pill" aria-label={t('Change release stage')} type="button"><span className="flow-release-pill__icon"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, stage)} size={14}/></span><span className="is-value" data-i18n-ignore={stage ? '' : undefined}>{stage || t('Stage')}</span></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><FlowDropdownMenuContent label={t('Change stage…')} className="flow-release-stage-menu"><ReleaseStageOptions pipeline={pipeline} value={stage} onChoose={next => { setStage(next); setStageOpen(false) }}/></FlowDropdownMenuContent></DropdownMenu.Portal>
          </DropdownMenu.Root>}
          <ReleaseDateMenu value={targetDate} onChange={setTargetDate} trigger={<button className="flow-release-pill" aria-label={t(targetDate ? 'Change target date' : 'Add target date')} type="button"><span className="flow-release-pill__icon"><FlowGlyph name="dateAdd" width={14} height={14}/></span><span className={targetDate ? 'is-value' : undefined}>{targetDate ? formatDate(targetDate, { month: 'short', day: 'numeric', year: 'numeric' }) : t('Target date')}</span></button>}/>
        </div>
        <footer><button className="flow-releases-secondary flow-release-editor__cancel" disabled={saving} onClick={onClose} type="button">{t('Cancel')}</button><button className="flow-releases-primary flow-release-editor__submit" disabled={!valid || saving} onClick={() => void save()} type="button">{t(release ? 'Save' : 'Create release')}</button></footer>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
