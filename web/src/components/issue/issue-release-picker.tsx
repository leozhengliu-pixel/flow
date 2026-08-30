import * as Popover from '@radix-ui/react-popover'
import { Plus } from 'lucide-react'
import { useEffect, useMemo, useState, type KeyboardEvent } from 'react'
import { toast } from 'sonner'

import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { ReleasePipelineIcon, ReleaseStatusIcon } from '@/components/releases/release-icons'
import { useI18n } from '@/i18n/i18n'
import { createRelease, setIssueReleases } from '@/lib/api'
import { releasePath } from '@/lib/app-routes'
import type { BootstrapData, Issue, Release, ReleasePipeline } from '@/types/flow'

import './issue-release-picker.css'

const groupOrder: Release['status'][] = ['inProgress', 'planned', 'released', 'canceled']

export function IssueReleasePicker({ data, issue, grouped = false }: { data: BootstrapData; issue: Issue; grouped?: boolean }) {
  const { t, formatDate } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState('')
  const [pipelinesOpen, setPipelinesOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [releases, setReleases] = useState(data.releases)
  const [selected, setSelected] = useState(() => data.releases.filter(item => item.issueIds.includes(issue.id)).map(item => item.id))

  useEffect(() => setReleases(data.releases), [data.releases])
  useEffect(() => setSelected(data.releases.filter(item => item.issueIds.includes(issue.id)).map(item => item.id)), [data.releases, issue.id])
  useEffect(() => {
    const sync = (event: Event) => {
      const detail = (event as CustomEvent<{ issueId: string; releaseIds: string[] }>).detail
      if (detail?.issueId !== issue.id) return
      setSelected(detail.releaseIds)
      setReleases(current => current.map(item => ({ ...item, issueIds: detail.releaseIds.includes(item.id) ? [...new Set([...item.issueIds, issue.id])] : item.issueIds.filter(id => id !== issue.id) })))
    }
    addEventListener('flow:issue-releases', sync)
    return () => removeEventListener('flow:issue-releases', sync)
  }, [issue.id])

  const pipelines = useMemo(() => data.releasePipelines.filter(item => !item.teamIds.length || item.teamIds.includes(issue.team.id)), [data.releasePipelines, issue.team.id])
  const activeReleases = useMemo(() => releases.filter(item => !item.archivedAt), [releases])
  const selectedReleases = activeReleases.filter(item => selected.includes(item.id))
  const recent = useMemo(() => activeReleases
    .filter(item => selected.includes(item.id) || item.status === 'planned' || item.status === 'inProgress')
    .sort((left, right) => Number(selected.includes(right.id)) - Number(selected.includes(left.id)) || right.updatedAt.localeCompare(left.updatedAt)), [activeReleases, selected])
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const filtered = recent.filter(item => `${item.name} ${pipelines.find(pipeline => pipeline.id === item.pipelineId)?.name ?? ''}`.toLocaleLowerCase().includes(normalizedQuery))
  const selectedOptions = filtered.filter(item => selected.includes(item.id))
  const availableOptions = filtered.filter(item => !selected.includes(item.id))
  const defaultPipeline = pipelines.find(pipeline => recent.some(item => item.pipelineId === pipeline.id)) ?? pipelines[0]
  const canCreate = Boolean(normalizedQuery && defaultPipeline && !activeReleases.some(item => item.name.toLocaleLowerCase() === normalizedQuery))
  const itemIds = [...selectedOptions.map(item => item.id), ...availableOptions.map(item => item.id), '__pipelines', ...(canCreate ? ['__create'] : [])]
  const firstItemId = itemIds[0] ?? ''

  useEffect(() => {
    if (open) setActiveId(firstItemId)
  }, [firstItemId, open, query])

  const close = () => { setOpen(false); setQuery(''); setPipelinesOpen(false); setCreateOpen(false) }
  const toggle = async (id: string) => {
    if (saving) return
    const before = selected
    const next = before.includes(id) ? before.filter(value => value !== id) : [...before, id]
    setSelected(next)
    setReleases(current => current.map(item => item.id === id ? { ...item, issueIds: next.includes(id) ? [...new Set([...item.issueIds, issue.id])] : item.issueIds.filter(value => value !== issue.id) } : item))
    setSaving(true)
    try {
      await setIssueReleases(issue.id, next)
      dispatchEvent(new CustomEvent('flow:issue-releases', { detail: { issueId: issue.id, releaseIds: next } }))
    } catch (error) {
      setSelected(before)
      setReleases(current => current.map(item => item.id === id ? { ...item, issueIds: before.includes(id) ? [...new Set([...item.issueIds, issue.id])] : item.issueIds.filter(value => value !== issue.id) } : item))
      toast.error(error instanceof Error ? error.message : t('Could not update releases'))
    } finally { setSaving(false) }
  }
  const create = async (pipeline: ReleasePipeline, name: string, version: string) => {
    if (saving) return
    setSaving(true)
    try {
      const stage = pipeline.stages[0] ?? ''
      const release = await createRelease({ name, version, pipelineId: pipeline.id, stage, status: pipeline.stageStatuses[stage] ?? 'planned', issueIds: [issue.id] })
      setReleases(current => [...current, release])
      const next = [...selected, release.id]
      setSelected(next)
      dispatchEvent(new CustomEvent('flow:issue-releases', { detail: { issueId: issue.id, releaseIds: next } }))
      close()
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Could not create release'))
    } finally { setSaving(false) }
  }
  const chooseActive = () => {
    if (activeId === '__pipelines') { setPipelinesOpen(true); return }
    if (activeId === '__create') { setCreateOpen(true); return }
    if (activeId) void toggle(activeId)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const index = Math.max(0, itemIds.indexOf(activeId))
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      const offset = event.key === 'ArrowDown' ? 1 : -1
      setActiveId(itemIds[(index + offset + itemIds.length) % itemIds.length] ?? '')
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault(); setActiveId(event.key === 'Home' ? itemIds[0] ?? '' : itemIds.at(-1) ?? '')
    } else if (event.key === 'Enter') {
      event.preventDefault(); chooseActive()
    }
  }

  const trigger = selectedReleases.length
    ? <button type="button" className="issue-release-add" aria-label={t('Add to release')} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}><Plus/></button>
    : <button type="button" className="issue-release-empty-trigger" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(value => !value)}><ReleaseStatusIcon color="currentColor" status="planned"/><span>{t('Set release')}</span></button>

  const values = <div className="issue-release-values">{selectedReleases.map(item => {
    const pipeline = pipelines.find(value => value.id === item.pipelineId)
    const date = item.releasedAt || item.targetDate
    return <div className="issue-release-value" key={item.id}>
      <button type="button" className="issue-release-pill" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}>
        <ReleaseStatusIcon status={item.status}/><span data-i18n-ignore>{pipeline?.name}</span><strong data-i18n-ignore>{item.name}</strong>{date && <small>{formatDate(date, { month: 'short', day: 'numeric' })}</small>}
      </button>
      {pipeline && <a className="issue-release-open" href={releasePath(data.workspace.urlKey, pipeline.slugId, item.slugId)} aria-label={t('Open release')}><OpenReleaseChevron/></a>}
    </div>
  })}</div>

  const anchor = grouped
    ? <section className="property-group issue-release-group"><h4><span>{t('Releases')}</span>{selectedReleases.length ? trigger : null}</h4>{!selectedReleases.length && trigger}{values}</section>
    : <div className="issue-release-property">{trigger}{values}</div>

  return <Popover.Root open={open} onOpenChange={value => { if (value) setOpen(true); else close() }}>
    <Popover.Anchor asChild>{anchor}</Popover.Anchor>
    <Popover.Portal><Popover.Content className="issue-release-picker issue-release-picker--root" align="start" side="bottom" sideOffset={grouped ? -36 : 4} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onKeyDown={onKeyDown}>
      <ReleaseSearch autoFocus value={query} placeholder={t('Add to release…')} shortcut="⌥ R" activeId={activeId} onChange={value => { setQuery(value); setPipelinesOpen(false); setCreateOpen(false) }}/>
      <div className="issue-release-results" role="listbox" aria-multiselectable="true">
        {selectedOptions.map(item => <ReleaseOption active={activeId === item.id} checked disabled={saving} item={item} key={item.id} pipeline={pipelines.find(value => value.id === item.pipelineId)} onActive={() => { setActiveId(item.id); setPipelinesOpen(false) }} onChoose={() => void toggle(item.id)}/>)}
        {selectedOptions.length > 0 && availableOptions.length > 0 && <ReleaseSeparator/>}
        {availableOptions.map(item => <ReleaseOption active={activeId === item.id} checked={false} disabled={saving || Boolean(item.stageFrozenAt)} item={item} key={item.id} pipeline={pipelines.find(value => value.id === item.pipelineId)} onActive={() => { setActiveId(item.id); setPipelinesOpen(false) }} onChoose={() => void toggle(item.id)}/>)}
        {(selectedOptions.length > 0 || availableOptions.length > 0) && <ReleaseSeparator/>}
        <AllPipelinesOption active={activeId === '__pipelines'} open={pipelinesOpen} pipelines={pipelines} releases={activeReleases} selected={selected} saving={saving} onActive={() => setActiveId('__pipelines')} onCreate={create} onOpenChange={setPipelinesOpen} onToggle={toggle}/>
        {canCreate && defaultPipeline && <CreateReleaseOption active={activeId === '__create'} name={query.trim()} open={createOpen} pipeline={defaultPipeline} saving={saving} onActive={() => setActiveId('__create')} onCreate={create} onOpenChange={setCreateOpen}/>}
        {!filtered.length && !canCreate && !pipelines.length && <p>{t('No releases found')}</p>}
      </div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function ReleaseSearch({ value, placeholder, shortcut, activeId, onChange, autoFocus = false }: { value: string; placeholder: string; shortcut?: string; activeId?: string; onChange: (value: string) => void; autoFocus?: boolean }) {
  return <label className="issue-release-search"><input autoFocus={autoFocus} role="searchbox" aria-activedescendant={activeId || undefined} aria-label={placeholder} placeholder={placeholder} value={value} onChange={event => onChange(event.target.value)}/>{shortcut && <kbd>{shortcut}</kbd>}</label>
}

function ReleaseOption({ active, checked, disabled, item, pipeline, onActive, onChoose }: { active: boolean; checked: boolean; disabled: boolean; item: Release; pipeline?: ReleasePipeline; onActive: () => void; onChoose: () => void }) {
  const { formatDate } = useI18n()
  const date = item.releasedAt || item.targetDate
  return <button type="button" className="issue-release-option" role="option" aria-selected={active} aria-checked={checked} aria-disabled={disabled} disabled={disabled} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="issue-release-option-bg"/><span className="issue-release-checkbox" role="checkbox" aria-checked={checked}>{checked && <CheckboxMark/>}</span><ReleaseStatusIcon className="issue-release-status" status={item.status}/><span className="issue-release-option-copy"><strong data-i18n-ignore>{item.name}</strong>{pipeline && <small data-i18n-ignore>{pipeline.name}</small>}{date && <small>{formatDate(date, { month: 'short', day: 'numeric' })}</small>}</span>
  </button>
}

function AllPipelinesOption({ active, open, pipelines, releases, selected, saving, onActive, onCreate, onOpenChange, onToggle }: { active: boolean; open: boolean; pipelines: ReleasePipeline[]; releases: Release[]; selected: string[]; saving: boolean; onActive: () => void; onCreate: (pipeline: ReleasePipeline, name: string, version: string) => Promise<void>; onOpenChange: (open: boolean) => void; onToggle: (id: string) => Promise<void> }) {
  const { t } = useI18n()
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild><button type="button" className="issue-release-option issue-release-submenu-trigger" role="option" aria-selected={active} aria-expanded={open} onPointerEnter={() => { onActive(); onOpenChange(true) }} onFocus={onActive} onClick={event => { event.preventDefault(); onOpenChange(true) }}>
      <span className="issue-release-option-bg"/><ReleasePipelineIcon/><span>{t('All pipelines…')}</span><MenuChevron/>
    </button></Popover.Trigger>
    <Popover.Portal><Popover.Content className="issue-release-picker issue-release-submenu issue-release-pipelines-menu" side="right" align="end" sideOffset={-2} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { event.preventDefault(); onOpenChange(false) }}>
      <div className="issue-release-results" role="listbox">{pipelines.map(pipeline => <PipelineOption key={pipeline.id} pipeline={pipeline} releases={releases.filter(item => item.pipelineId === pipeline.id)} selected={selected} saving={saving} onCreate={onCreate} onToggle={onToggle}/>)}</div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function PipelineOption({ pipeline, releases, selected, saving, onCreate, onToggle }: { pipeline: ReleasePipeline; releases: Release[]; selected: string[]; saving: boolean; onCreate: (pipeline: ReleasePipeline, name: string, version: string) => Promise<void>; onToggle: (id: string) => Promise<void> }) {
  const [open, setOpen] = useState(false)
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild><button type="button" className="issue-release-option issue-release-submenu-trigger" role="option" aria-expanded={open} onPointerEnter={() => setOpen(true)} onClick={event => { event.preventDefault(); setOpen(true) }}>
      <span className="issue-release-option-bg"/><ReleasePipelineIcon/><span data-i18n-ignore>{pipeline.name}</span><MenuChevron/>
    </button></Popover.Trigger>
    <Popover.Portal><PipelineReleaseMenu open={open} pipeline={pipeline} releases={releases} selected={selected} saving={saving} onCreate={onCreate} onOpenChange={setOpen} onToggle={onToggle}/></Popover.Portal>
  </Popover.Root>
}

function PipelineReleaseMenu({ open, pipeline, releases, selected, saving, onCreate, onOpenChange, onToggle }: { open: boolean; pipeline: ReleasePipeline; releases: Release[]; selected: string[]; saving: boolean; onCreate: (pipeline: ReleasePipeline, name: string, version: string) => Promise<void>; onOpenChange: (open: boolean) => void; onToggle: (id: string) => Promise<void> }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const filtered = releases.filter(item => !item.archivedAt && item.name.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))
  const groups = groupOrder.map(status => ({ status, items: filtered.filter(item => item.status === status) })).filter(group => group.items.length)
  const canCreate = Boolean(query.trim() && !releases.some(item => item.name.toLocaleLowerCase() === query.trim().toLocaleLowerCase()))
  const ids = [...groups.flatMap(group => group.items.map(item => item.id)), ...(canCreate ? ['__create'] : [])]
  const firstId = ids[0] ?? ''
  useEffect(() => { if (open) setActiveId(firstId) }, [firstId, open, query])
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const index = Math.max(0, ids.indexOf(activeId))
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); const offset = event.key === 'ArrowDown' ? 1 : -1; setActiveId(ids[(index + offset + ids.length) % ids.length] ?? '') }
    else if (event.key === 'Enter') { event.preventDefault(); if (activeId === '__create') setCreateOpen(true); else if (activeId) void onToggle(activeId) }
  }
  return <Popover.Content className="issue-release-picker issue-release-submenu issue-release-pipeline-releases" side="right" align="start" sideOffset={-2} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { event.preventDefault(); onOpenChange(false) }} onKeyDown={onKeyDown}>
    <ReleaseSearch autoFocus value={query} placeholder={t('Add to {pipeline} releases…').replace('{pipeline}', pipeline.name)} activeId={activeId} onChange={value => { setQuery(value); setCreateOpen(false) }}/>
    <div className="issue-release-results" role="listbox" aria-multiselectable="true">{groups.map(group => <div className="issue-release-section" key={group.status}><div className="issue-release-group-label" role="group">{t(groupLabel(group.status))}</div>{group.items.map(item => <ReleaseOption active={activeId === item.id} checked={selected.includes(item.id)} disabled={saving || Boolean(item.stageFrozenAt && !selected.includes(item.id))} item={item} key={item.id} onActive={() => setActiveId(item.id)} onChoose={() => void onToggle(item.id)}/>)}</div>)}
      {canCreate && <CreateReleaseOption active={activeId === '__create'} name={query.trim()} open={createOpen} pipeline={pipeline} saving={saving} onActive={() => setActiveId('__create')} onCreate={onCreate} onOpenChange={setCreateOpen}/>} {!groups.length && !canCreate && <p>{t('No releases found')}</p>}
    </div>
  </Popover.Content>
}

function CreateReleaseOption({ active, name, open, pipeline, saving, onActive, onCreate, onOpenChange }: { active: boolean; name: string; open: boolean; pipeline: ReleasePipeline; saving: boolean; onActive: () => void; onCreate: (pipeline: ReleasePipeline, name: string, version: string) => Promise<void>; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Trigger asChild><button type="button" className="issue-release-option issue-release-create" role="option" aria-selected={active} aria-expanded={open} disabled={saving} onPointerMove={onActive} onFocus={onActive}>
      <span className="issue-release-option-bg"/><Plus/><span>{t('Create new release')} <b data-i18n-ignore>"{name}"</b><small data-i18n-ignore>{pipeline.name}</small></span><MenuChevron/>
    </button></Popover.Trigger>
    <Popover.Portal><VersionMenu name={name} pipeline={pipeline} saving={saving} onCreate={onCreate} onOpenChange={onOpenChange}/></Popover.Portal>
  </Popover.Root>
}

function VersionMenu({ name, pipeline, saving, onCreate, onOpenChange }: { name: string; pipeline: ReleasePipeline; saving: boolean; onCreate: (pipeline: ReleasePipeline, name: string, version: string) => Promise<void>; onOpenChange: (open: boolean) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const version = query.trim() || name
  return <Popover.Content className="issue-release-picker issue-release-submenu issue-release-version-menu" side="right" align="end" sideOffset={-2} collisionPadding={8} onOpenAutoFocus={event => event.preventDefault()} onEscapeKeyDown={event => { event.preventDefault(); onOpenChange(false) }}>
    <ReleaseSearch autoFocus value={query} placeholder={t('Type to set version…')} onChange={setQuery}/>
    <div className="issue-release-results" role="listbox" onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); void onCreate(pipeline, name, version) } }}>
      <button type="button" className="issue-release-option issue-release-version-option" role="option" disabled={saving} onClick={() => void onCreate(pipeline, name, version)}><span className="issue-release-option-bg"/><span>{t('Use "{version}" as version').replace('{version}', version)}</span></button>
      <button type="button" className="issue-release-option issue-release-version-option" role="option" disabled={saving} onClick={() => void onCreate(pipeline, name, '')}><span className="issue-release-option-bg"/><span>{t('No version')}</span></button>
    </div>
  </Popover.Content>
}

function ReleaseSeparator() { return <div className="issue-release-separator" role="separator"/> }
function MenuChevron() { return <span aria-hidden="true" className="issue-release-menu-chevron">▶</span> }
function OpenReleaseChevron() { return <svg aria-hidden="true" viewBox="0 0 9 5"><path d="M1.915.557a.667.667 0 0 0-.943.943l2.862 2.862a.942.942 0 0 0 1.333 0L8.028 1.5a.667.667 0 0 0-.943-.943L4.5 3.14 1.915.557Z" fill="currentColor"/></svg> }
function groupLabel(status: Release['status']) { return status === 'inProgress' ? 'In progress' : status === 'planned' ? 'Planned' : status === 'released' ? 'Completed' : 'Canceled' }
