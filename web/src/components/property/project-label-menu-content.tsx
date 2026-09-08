import * as Popover from '@radix-ui/react-popover'
import { Check, Plus } from 'lucide-react'
import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import { LabelGroupIcon } from '@/components/issue/issue-icons'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { useI18n } from '@/i18n/i18n'
import type { PropertyOption } from './property-menu'
import { projectLabelEntries, type ProjectLabelEntry } from './project-label-menu-model'
import './project-label-picker.css'
import { LabelHoverPreview } from './label-hover-preview'

export function ProjectLabelMenuContent({ options, selectedIds, groupId, onChoose, onClose, onCreate, submenuPortalContainer }: {
  options: PropertyOption[]
  selectedIds: string[]
  groupId?: string
  onChoose: (id: string) => void
  onClose: () => void
  onCreate?: (name: string, groupId?: string) => void | Promise<unknown>
  submenuPortalContainer?: HTMLElement | null
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState<string>()
  const [submenuId, setSubmenuId] = useState<string>()
  const [submenuFocus, setSubmenuFocus] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createError, setCreateError] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const listId = useId()
  const choices = groupId ? options.filter(option => option.groupId === groupId).map(option => ({ ...option, groupId: undefined, groupLabel: undefined })) : options
  const entries = projectLabelEntries(choices, selectedIds, query)
  const active = entries.find(entry => entry.id === activeId) ?? entries[0]
  const activeEntryId = active?.id
  const canCreate = Boolean(onCreate && query.trim().length >= 2 && !options.some(option => option.label.toLocaleLowerCase() === query.trim().toLocaleLowerCase()))
  const create = async (name = query.trim(), parentId = groupId) => {
    if (!onCreate || creating) return
    setCreating(true)
    setCreateError(undefined)
    try { await onCreate(name, parentId); onClose() }
    catch (error) { setCreateError(error instanceof Error ? error.message : t('Could not create label')) }
    finally { setCreating(false) }
  }
  const placeholder = groupId ? options.find(option => option.groupId === groupId)?.groupLabel ?? t('Labels') : t(selectedIds.length ? 'Change or add labels…' : 'Add labels…')
  useEffect(() => { inputRef.current?.focus() }, [])
  useEffect(() => {
    if (activeEntryId) document.getElementById(`${listId}-${activeEntryId}`)?.scrollIntoView({ block: 'nearest' })
  }, [activeEntryId, listId])
  const choose = (option: PropertyOption) => {
    if (option.disabled) return
    onChoose(option.id)
    if (groupId || option.groupId) onClose()
  }
  const onKeyDown = (event: KeyboardEvent) => {
    event.stopPropagation()
    if (event.nativeEvent.isComposing) return
    if (event.key === 'Enter' && !entries.length && canCreate) { event.preventDefault(); create(); return }
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); return }
    const enabled = entries.filter(entry => !entry.option?.disabled)
    if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault()
      const index = enabled.findIndex(entry => entry.id === active?.id)
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? enabled.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + enabled.length) % enabled.length
      setActiveId(enabled[next]?.id)
    } else if (active && (event.key === 'Enter' || event.key === 'ArrowRight')) {
      if (active.children) { event.preventDefault(); setSubmenuFocus(true); setSubmenuId(active.id) }
      else if (event.key === 'Enter' && active.option) { event.preventDefault(); choose(active.option) }
    }
  }
  return <div onKeyDown={onKeyDown}>
    <div className={`project-label-search${groupId && !query ? ' is-hidden' : ''}`}>
      <input ref={inputRef} aria-label={placeholder} aria-controls={listId} aria-activedescendant={active ? `${listId}-${active.id}` : undefined} value={query} placeholder={placeholder} onChange={event => { setQuery(event.target.value); setActiveId(undefined); setSubmenuId(undefined) }} autoComplete="off" spellCheck={false}/>
      {!groupId && <kbd>P, then L</kbd>}
    </div>
    <div className="project-label-options" id={listId} ref={listRef} role="listbox" aria-label={placeholder} aria-multiselectable={!groupId}>
      {entries.map((entry, index) => <div key={entry.id}>
        {!query && index > 0 && entries[index - 1].checked && !entry.checked && <div className="project-label-separator" role="separator"/>}
        {entry.children ? <ProjectLabelGroup entry={entry} active={activeId === entry.id} id={`${listId}-${entry.id}`} open={submenuId === entry.id} focusOnOpen={submenuFocus} portalContainer={submenuPortalContainer} onOpenChange={(next, focus = true) => { setActiveId(entry.id); setSubmenuFocus(focus); setSubmenuId(next ? entry.id : undefined) }} onBack={() => { setSubmenuId(undefined); inputRef.current?.focus() }} selectedIds={selectedIds} onChoose={choose} onClear={option => { setSubmenuId(undefined); onChoose(option.id) }} onCreate={onCreate ? name => { void create(name, entry.children?.[0]?.groupId) } : undefined}/> : <ProjectLabelOption entry={entry} id={`${listId}-${entry.id}`} active={activeId === entry.id} multiple={!groupId} onActive={() => { setActiveId(entry.id); setSubmenuId(undefined) }} onChoose={() => entry.option && choose(entry.option)}/>}
      </div>)}
      {canCreate && <ProjectLabelCreateOption name={query.trim()} groupName={groupId ? placeholder : undefined} disabled={creating} onCreate={() => void create()}/>}
      {createError && <div className="core-property-empty" role="alert">{createError}</div>}
      {!entries.length && !canCreate && <div className="core-property-empty">{t(!query && onCreate ? 'Start typing to create a new label' : 'No results')}</div>}
    </div>
  </div>
}

function ProjectLabelGroup({ entry, id, active, open, focusOnOpen, onOpenChange, onBack, onChoose, onClear, selectedIds, onCreate, portalContainer }: {
  entry: ProjectLabelEntry; id: string; active: boolean; open: boolean; focusOnOpen: boolean; onOpenChange: (open: boolean, focus?: boolean) => void; onBack: () => void; onChoose: (option: PropertyOption) => void; selectedIds: string[]
  onCreate?: (name: string) => void
  portalContainer?: HTMLElement | null
  onClear: (option: PropertyOption) => void
}) {
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listId = useId()
  const { t } = useI18n()
  const options = (entry.children ?? []).filter(option => option.label.toLocaleLowerCase().includes(query.toLocaleLowerCase()))
  const canCreate = Boolean(onCreate && query.trim().length >= 2 && !entry.children?.some(option => option.label.toLocaleLowerCase() === query.trim().toLocaleLowerCase()))
  const cancel = () => { clearTimeout(timer.current); timer.current = undefined }
  useEffect(() => cancel, [])
  useEffect(() => { if (open) { setQuery(''); setActiveIndex(0) } }, [open])
  useEffect(() => { if (open && focusOnOpen) inputRef.current?.focus() }, [open, focusOnOpen])
  const onKeyDown = (event: KeyboardEvent) => {
    event.stopPropagation()
    if (event.nativeEvent.isComposing) return
    if (event.key === 'Enter' && !options.length && canCreate) { event.preventDefault(); onCreate?.(query.trim()); return }
    if (event.key === 'Escape' || event.key === 'ArrowLeft') { event.preventDefault(); onBack() }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); setActiveIndex((activeIndex + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % Math.max(options.length, 1)) }
    if (event.key === 'Enter' && options[activeIndex] && !options[activeIndex].disabled) { event.preventDefault(); onChoose(options[activeIndex]) }
  }
  return <Popover.Root open={open} onOpenChange={onOpenChange}>
    <Popover.Anchor asChild><button id={id} type="button" role="option" className="project-label-option is-group" aria-label={[entry.label, entry.detail].filter(Boolean).join(' ')} aria-selected={entry.checked} aria-checked={entry.checked} data-active={active || open} aria-haspopup="listbox" aria-expanded={open} onPointerEnter={() => { cancel(); timer.current = setTimeout(() => onOpenChange(true, false), 200) }} onPointerLeave={cancel} onFocus={() => onOpenChange(true)} onClick={() => onOpenChange(true)}>
      <span className="project-label-option-bg"/>{entry.checked && <span className="project-label-checkbox" onPointerDown={event => event.preventDefault()} onClick={event => { event.stopPropagation(); cancel(); const selected = entry.children?.find(option => selectedIds.includes(option.id)); if (selected) onClear(selected) }}><CheckboxMark/></span>}
      <span className="project-label-option-icon"><LabelGroupIcon size={16} color={entry.color}/></span><span className="project-label-option-name" data-i18n-ignore>{entry.label}</span>{entry.detail && <span className="project-label-option-detail" data-i18n-ignore>{entry.detail}</span>}<span className="project-label-chevron" aria-hidden="true">▶</span>
    </button></Popover.Anchor>
    <Popover.Portal container={portalContainer}><Popover.Content data-flow-motion="floating" className="project-label-submenu" side="right" align="start" alignOffset={-6.5} sideOffset={-2} collisionPadding={10} onOpenAutoFocus={event => { event.preventDefault(); if (focusOnOpen) inputRef.current?.focus() }} onCloseAutoFocus={event => { event.preventDefault() }} onKeyDown={onKeyDown} onClick={event => event.stopPropagation()}>
      <div className={`project-label-search${!query ? ' is-hidden' : ''}`}><input ref={inputRef} aria-label={entry.label} placeholder={entry.label} value={query} onChange={event => { setQuery(event.target.value); setActiveIndex(0) }} aria-controls={listId} aria-activedescendant={options[activeIndex] ? `${listId}-${options[activeIndex].id}` : undefined}/></div>
      <div className="project-label-options" id={listId} role="listbox" aria-label={entry.label} aria-multiselectable={false}>{options.map((option, index) => <ProjectLabelOption key={option.id} id={`${listId}-${option.id}`} entry={{ id: option.id, label: option.label, color: option.color, checked: selectedIds.includes(option.id), option }} active={index === activeIndex} multiple={false} onActive={() => setActiveIndex(index)} onChoose={() => onChoose(option)}/>)}{canCreate && <ProjectLabelCreateOption name={query.trim()} groupName={entry.label} onCreate={() => onCreate?.(query.trim())}/>} {!options.length && !canCreate && <div className="core-property-empty">{t('No results')}</div>}</div>
    </Popover.Content></Popover.Portal>
  </Popover.Root>
}

function ProjectLabelCreateOption({ name, onCreate, groupName, disabled }: { name: string; onCreate: () => void; groupName?: string; disabled?: boolean }) {
  const { t } = useI18n()
  const text = groupName ? `${t('Create label in group')} "${groupName}": "${name}"` : `${t('Create new workspace label')}: "${name}"`
  return <><div className="project-label-separator" role="separator"/><button className="project-label-option" type="button" role="option" aria-label={text} disabled={disabled} onClick={onCreate}><span className="project-label-option-bg"/><Plus className="project-label-option-icon" size={16}/><span className="project-label-option-name" data-i18n-ignore>{text}</span></button></>
}

function ProjectLabelOption({ entry, id, active, multiple, onChoose, onActive }: { entry: ProjectLabelEntry; id: string; active: boolean; multiple: boolean; onChoose: () => void; onActive: () => void }) {
  const { t } = useI18n()
  return <LabelHoverPreview label={{ name: entry.label, color: entry.color ?? '', description: entry.option?.description, issueCount: entry.option?.issueCount, scope: entry.option?.scope, resourceType: 'project' }} side="right"><button id={id} className={`project-label-option${multiple ? ' is-multiple' : ''}`} role="option" type="button" aria-selected={entry.checked} aria-checked={entry.checked} data-active={active} disabled={entry.option?.disabled} onPointerMove={onActive} onFocus={onActive} onClick={onChoose}>
    <span className="project-label-option-bg"/>{multiple && <span className="project-label-checkbox">{entry.checked && <CheckboxMark/>}</span>}
    <span className="project-label-option-icon"><i style={{ background: entry.color }}/></span><span className="project-label-option-name" data-i18n-ignore>{entry.label}</span>{entry.option?.archived && <span className="project-label-option-detail">{t('Archived')}</span>}{entry.detail && <span className="project-label-option-detail" data-i18n-ignore>{entry.detail}</span>}{!multiple && entry.checked && <Check className="project-label-check" size={14}/>}
  </button></LabelHoverPreview>
}
