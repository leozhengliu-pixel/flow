import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { ChevronRight, X } from 'lucide-react'
import { useState } from 'react'
import { toast } from 'sonner'

import { MentionTextField } from '@/components/editor/mention-text-field'
import { LinearGlyph } from '@/components/ui/menu-glyphs'
import { LinearDropdownMenuContent } from '@/components/ui/row-context-menu'
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

/** Linear's New release / Edit release composer (pipeline › New release, name, version, description, stage and target date chips). */
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
          description: <span data-i18n-ignore>{created?.name ?? input.name}</span>,
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
      <Dialog.Overlay data-flow-motion="backdrop" className="flow-release-dialog-overlay"/>
      <Dialog.Content data-flow-motion="dialog" aria-describedby={undefined} className="flow-release-editor" onKeyDown={event => { if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) { event.preventDefault(); void save() } }}>
        <Dialog.Title className="flow-release-editor__title"><span data-i18n-ignore>{pipeline.name}</span><ChevronRight aria-hidden="true"/><strong>{t(release ? 'Edit release' : 'New release')}</strong></Dialog.Title>
        <Dialog.Close className="flow-release-editor__close" aria-label={t('Discard')}><X/></Dialog.Close>
        <div className="flow-release-editor__copy">
          <input aria-label={t('Release name')} autoFocus className="flow-release-editor__name" value={name} onChange={event => setName(event.target.value)} onKeyDown={event => { if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey) { event.preventDefault(); const next = event.currentTarget.nextElementSibling; if (next instanceof HTMLElement) next.focus() } }} placeholder={t('Release name')}/>
          <input aria-label={t('Release version')} className="flow-release-editor__version" value={version} onChange={event => setVersion(event.target.value)} placeholder={t('Version')}/>
          <MentionTextField ariaLabel={t('Release description')} className="flow-release-editor__description" onChange={setDescription} onSubmit={() => void save()} placeholder={t('Add description…')} value={description}/>
        </div>
        <div className="flow-release-editor__properties">
          {scheduled && <DropdownMenu.Root open={stageOpen} onOpenChange={setStageOpen}>
            <DropdownMenu.Trigger asChild><button className="flow-release-pill" aria-label={t('Change release stage')} type="button"><ReleaseStatusIcon status={releaseStatusForStage(pipeline, stage)} size={14}/><span className="is-value" data-i18n-ignore={stage ? '' : undefined}>{stage || t('Stage')}</span></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><LinearDropdownMenuContent label={t('Change stage…')} className="flow-release-stage-menu"><ReleaseStageOptions pipeline={pipeline} value={stage} onChoose={next => { setStage(next); setStageOpen(false) }}/></LinearDropdownMenuContent></DropdownMenu.Portal>
          </DropdownMenu.Root>}
          <ReleaseDateMenu value={targetDate} onChange={setTargetDate} trigger={<button className="flow-release-pill" aria-label={t(targetDate ? 'Change target date' : 'Add target date')} type="button"><LinearGlyph name="dateAdd" width={14} height={14}/><span className={targetDate ? 'is-value' : undefined}>{targetDate ? formatDate(targetDate, { month: 'short', day: 'numeric', year: 'numeric' }) : t('Target date')}</span></button>}/>
        </div>
        <footer><button className="flow-release-editor__cancel" disabled={saving} onClick={onClose} type="button">{t('Cancel')}</button><button className="flow-release-editor__submit" disabled={!valid || saving} onClick={() => void save()} type="button">{t(release ? 'Save' : 'Create release')}</button></footer>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
