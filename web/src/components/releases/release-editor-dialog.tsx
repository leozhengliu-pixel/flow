import * as Dialog from '@radix-ui/react-dialog'
import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import { CalendarDays, Check, ChevronDown, ChevronRight, CircleDashed, Lock, LockOpen, Search, Settings2, X } from 'lucide-react'
import { addMonths, eachDayOfInterval, endOfMonth, endOfWeek, isSameDay, isSameMonth, startOfMonth, startOfWeek, subMonths } from 'date-fns'
import { useMemo, useState } from 'react'
import { toast } from 'sonner'

import { CalendarIcon } from '@/components/issue/issue-icons'
import { createRelease, updateRelease } from '@/lib/api'
import { useI18n } from '@/i18n/i18n'
import type { BootstrapData, Release, ReleasePipeline } from '@/types/flow'

import { releaseStatusForStage } from './release-view-model'

type Props = {
  data: BootstrapData
  pipeline: ReleasePipeline
  release?: Release
  onClose: () => void
  onSaved: () => Promise<void>
}

export function ReleaseEditorDialog({ data, pipeline, release, onClose, onSaved }: Props) {
  const { t, formatDate } = useI18n()
  const [name, setName] = useState(release?.name ?? '')
  const [version, setVersion] = useState(release?.version ?? '')
  const [description, setDescription] = useState(release?.description ?? '')
  const [stage, setStage] = useState(release?.stage || pipeline.stages[0] || '')
  const [targetDate, setTargetDate] = useState(release?.targetDate ?? '')
  const [projectIds, setProjectIds] = useState(release?.projectIds ?? [])
  const [issueIds, setIssueIds] = useState(release?.issueIds ?? [])
  const [scopeOpen, setScopeOpen] = useState(false)
  const [scopeQuery, setScopeQuery] = useState('')
  const [saving, setSaving] = useState(false)
  const [stageQuery, setStageQuery] = useState('')
  const [stageMenuOpen, setStageMenuOpen] = useState(false)
  const [dateMenuOpen, setDateMenuOpen] = useState(false)
  const [frozen, setFrozen] = useState(Boolean(release?.stageFrozenAt))
  const [unfreezing, setUnfreezing] = useState(false)
  const projects = useMemo(() => data.projects.filter(item => item.name.toLowerCase().includes(scopeQuery.toLowerCase())), [data.projects, scopeQuery])
  const issues = useMemo(() => data.issues.filter(item => `${item.identifier} ${item.title}`.toLowerCase().includes(scopeQuery.toLowerCase())), [data.issues, scopeQuery])
  const stageOptions = useMemo(() => pipeline.stages.filter(item => item.toLowerCase().includes(stageQuery.trim().toLowerCase())), [pipeline.stages, stageQuery])
  const toggle = (values: string[], id: string, setter: (value: string[]) => void) => setter(values.includes(id) ? values.filter(value => value !== id) : [...values, id])
  const save = async () => {
    setSaving(true)
    try {
      const stageChanged = !release || release.stage !== stage
      const input = { name: name.trim(), version: version.trim(), description, pipelineId: pipeline.id, stage, ...(stageChanged ? { status: releaseStatusForStage(pipeline, stage, release?.status) } : {}), targetDate, projectIds, issueIds }
      if (release) await updateRelease(release.id, input)
      else await createRelease(input)
      await onSaved()
      onClose()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not save release'))
    } finally { setSaving(false) }
  }
  const setQuickDate = (days: number) => {
    const value = new Date()
    value.setDate(value.getDate() + days)
    setTargetDate(value.toISOString().slice(0, 10))
  }
  const unfreeze = async () => {
    if (!release) return
    setUnfreezing(true)
    try {
      await updateRelease(release.id, { stageFrozen: false })
      setFrozen(false)
      await onSaved()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not unfreeze release scope'))
    } finally {
      setUnfreezing(false)
    }
  }
  return <Dialog.Root open onOpenChange={open => { if (!open && !saving) onClose() }}>
    <Dialog.Portal>
      <Dialog.Overlay className="flow-release-dialog-overlay"/>
      <Dialog.Content aria-describedby={undefined} className={`flow-release-editor${scopeOpen ? ' is-scope-open' : ''}`}>
        <Dialog.Title className="flow-release-editor__title"><span data-i18n-ignore>{pipeline.name}</span><ChevronRight/><strong>{t(release ? 'Edit release' : 'New release')}</strong></Dialog.Title>
        <Dialog.Close className="flow-release-editor__close" aria-label={t('Close')}><X/></Dialog.Close>
        <div className="flow-release-editor__copy">
          <input aria-label={t('Release name')} autoFocus className="flow-release-editor__name" value={name} onChange={event => setName(event.target.value)} placeholder={t('Release name')}/>
          <input aria-label={t('Release version')} className="flow-release-editor__version" value={version} onChange={event => setVersion(event.target.value)} placeholder={t('Version')}/>
          <textarea aria-label={t('Release description')} className="flow-release-editor__description" value={description} onChange={event => setDescription(event.target.value)} placeholder={t('Add description…')}/>
        </div>
        <div className="flow-release-editor__properties">
          <DropdownMenu.Root open={stageMenuOpen} onOpenChange={setStageMenuOpen}>
            <DropdownMenu.Trigger asChild><button className="flow-release-pill" aria-label={t('Change release stage')}><CircleDashed/><span data-i18n-ignore={stage ? true : undefined}>{stage || t('Stage')}</span><ChevronDown/></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><DropdownMenu.Content className="flow-release-stage-menu" align="start" onCloseAutoFocus={() => setStageQuery('')} sideOffset={5}><div className="flow-release-stage-search"><Search/><input aria-label={t('Search stages')} autoFocus value={stageQuery} onChange={event => setStageQuery(event.target.value)} onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setStageMenuOpen(false); return } if (event.key === 'ArrowDown') { event.preventDefault(); event.currentTarget.closest('[role=menu]')?.querySelector<HTMLElement>('[role=menuitem]')?.focus(); return } event.stopPropagation() }} placeholder={t('Search stages…')}/></div>{stageOptions.map(option => <DropdownMenu.Item className="flow-release-menu-item" key={option} onSelect={() => setStage(option)}><span data-i18n-ignore>{option}</span>{option === stage && <Check/>}</DropdownMenu.Item>)}{!stageOptions.length&&<div className="flow-release-menu-empty">{t('No stages found')}</div>}</DropdownMenu.Content></DropdownMenu.Portal>
          </DropdownMenu.Root>
          <DropdownMenu.Root open={dateMenuOpen} onOpenChange={setDateMenuOpen}>
            <DropdownMenu.Trigger asChild><button className="flow-release-pill flow-release-date-trigger" aria-label={t('Target date')}><CalendarIcon/><span>{targetDate ? formatDate(targetDate, { month: 'short', day: 'numeric' }) : t('Target date')}</span><ChevronDown/></button></DropdownMenu.Trigger>
            <DropdownMenu.Portal><DropdownMenu.Content className="flow-release-date-menu" align="start" sideOffset={5}>
              <DropdownMenu.Sub>
                <DropdownMenu.SubTrigger className="flow-release-menu-item"><CalendarDays/><span>{t('Custom')}</span><ChevronRight/></DropdownMenu.SubTrigger>
                <DropdownMenu.Portal><DropdownMenu.SubContent className="flow-release-calendar-surface" sideOffset={8} alignOffset={-5}><ReleaseCalendar value={targetDate} onCancel={() => setDateMenuOpen(false)} onSave={value => { setTargetDate(value); setDateMenuOpen(false) }}/></DropdownMenu.SubContent></DropdownMenu.Portal>
              </DropdownMenu.Sub>
              <DropdownMenu.Item className="flow-release-menu-item" onSelect={() => setQuickDate(1)}>{t('Tomorrow')}</DropdownMenu.Item>
              <DropdownMenu.Item className="flow-release-menu-item" onSelect={() => setQuickDate(7)}>{t('1 week')}</DropdownMenu.Item>
              <DropdownMenu.Item className="flow-release-menu-item" onSelect={() => setQuickDate(14)}>{t('2 weeks')}</DropdownMenu.Item>
              <DropdownMenu.Item className="flow-release-menu-item" onSelect={() => setQuickDate(30)}>{t('1 month')}</DropdownMenu.Item>
              {targetDate && <DropdownMenu.Item className="flow-release-menu-item danger" onSelect={() => setTargetDate('')}>{t('No target date')}</DropdownMenu.Item>}
            </DropdownMenu.Content></DropdownMenu.Portal>
          </DropdownMenu.Root>
          <button aria-expanded={scopeOpen} className="flow-release-pill" onClick={() => setScopeOpen(value => !value)} type="button"><Settings2/><span>{t('Scope')}</span>{projectIds.length + issueIds.length > 0 && <b>{projectIds.length + issueIds.length}</b>}<ChevronDown/></button>
        </div>
        {scopeOpen && <div className="flow-release-scope">
          {frozen && <div className="flow-release-scope__frozen"><Lock/><span>{t('Release scope is frozen.')}</span><button disabled={unfreezing} onClick={() => void unfreeze()} type="button"><LockOpen/>{t(unfreezing ? 'Unfreezing…' : 'Unfreeze')}</button></div>}
          <label className="flow-release-scope__search"><Search/><input aria-label={t('Search scope')} placeholder={t('Search projects and issues…')} value={scopeQuery} onChange={event => setScopeQuery(event.target.value)}/></label>
          <div className="flow-release-scope__columns">
            <fieldset><legend>{t('Projects')}</legend>{projects.length ? projects.map(project => <label key={project.id} data-i18n-ignore><input type="checkbox" checked={projectIds.includes(project.id)} onChange={() => toggle(projectIds, project.id, setProjectIds)}/><i style={{ background: project.color }}/><span>{project.name}</span></label>) : <small>{t('No projects')}</small>}</fieldset>
            <fieldset><legend>{t('Issues')}</legend>{issues.length ? issues.map(issue => <label key={issue.id} data-i18n-ignore><input type="checkbox" checked={issueIds.includes(issue.id)} disabled={frozen && !issueIds.includes(issue.id)} onChange={() => toggle(issueIds, issue.id, setIssueIds)}/><span><b>{issue.identifier}</b>{issue.title}</span></label>) : <small>{t('No issues')}</small>}</fieldset>
          </div>
        </div>}
        <footer><button disabled={saving} onClick={onClose} type="button">{t('Cancel')}</button><button className="primary" disabled={!name.trim() || !stage || saving} onClick={() => void save()} type="button">{t(saving ? 'Saving…' : release ? 'Save changes' : 'Create release')}</button></footer>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}

function ReleaseCalendar({ value, onCancel, onSave }: { value: string; onCancel: () => void; onSave: (value: string) => void }) {
  const { t, formatDate } = useI18n()
  const initial = value ? new Date(`${value}T12:00:00`) : new Date()
  const [month, setMonth] = useState(startOfMonth(initial))
  const [draft, setDraft] = useState(initial)
  const months = [month, addMonths(month, 1)]
  const dateValue = (date: Date) => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
  const weekdays = eachDayOfInterval({ start: startOfWeek(new Date(2026, 7, 2)), end: endOfWeek(new Date(2026, 7, 2)) })
  return <div className="flow-release-calendar-dialog">
    <header><button aria-label={t('Previous month')} onClick={() => setMonth(current => subMonths(current, 1))} type="button"><ChevronRight/></button><strong>{t('Select target date')}</strong><button aria-label={t('Next month')} onClick={() => setMonth(current => addMonths(current, 1))} type="button"><ChevronRight/></button></header>
    <div className="flow-release-calendar-months">{months.map(current => {
      const days = eachDayOfInterval({ start: startOfWeek(startOfMonth(current)), end: endOfWeek(endOfMonth(current)) })
      return <section key={current.toISOString()}><h3>{formatDate(current.toISOString(), { month: 'long', year: 'numeric' })}</h3><div className="flow-release-calendar-weekdays">{weekdays.map(day => <span key={day.getDay()}>{formatDate(day.toISOString(), { weekday: 'narrow' })}</span>)}</div><div className="flow-release-calendar-grid">{days.map(day => <button aria-label={formatDate(day.toISOString(), { month: 'long', day: 'numeric', year: 'numeric' })} aria-pressed={isSameDay(day, draft)} className={!isSameMonth(day, current) ? 'outside' : ''} key={day.toISOString()} onClick={() => setDraft(day)} type="button">{day.getDate()}</button>)}</div></section>
    })}</div>
    <footer><button onClick={onCancel} type="button">{t('Cancel')}</button><button className="primary" onClick={() => onSave(dateValue(draft))} type="button">{t('Save target date')}</button></footer>
  </div>
}
