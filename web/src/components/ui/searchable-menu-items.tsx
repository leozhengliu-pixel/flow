import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Plus } from 'lucide-react'
import { Fragment, useEffect, useId, useMemo, useRef, useState, type ReactElement, type ReactNode } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { useI18n } from '@/i18n/i18n'
import './searchable-menu-items.css'

export interface SearchableMenuOption { id: string; label: string; keywords?: string; entity?: boolean }

// Radix owns menu dismissal and selection; only the visible rows join its
// collection. Keyboard traversal uses the complete filtered data set.
export function SearchableMenuItems<T extends SearchableMenuOption>({ options, renderOption, onSelect, onCreate, createLabel = 'Create new workspace label', searchLabel = 'Filter…', contextMenu = false, selectedId, className = '', searchClassName = 'li-menu-search', itemClassName, wrapOption, matches }: {
  options: T[]; renderOption: (option: T) => ReactNode; onSelect: (option: T) => void; onCreate?: (name: string) => void | Promise<void>; createLabel?: string; searchLabel?: string; contextMenu?: boolean
  selectedId?: string; className?: string; searchClassName?: string; itemClassName?: string; wrapOption?: (option: T, row: ReactElement) => ReactElement; matches?: (option: T, query: string) => boolean
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(() => Math.max(0, options.findIndex(option => option.id === selectedId)))
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<VirtuosoHandle>(null)
  const prefix = useId()
  useEffect(() => { const frame = requestAnimationFrame(() => input.current?.focus()); return () => cancelAnimationFrame(frame) }, [])
  const createName = query.trim()
  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    return options.filter(option => !needle || (matches ? matches(option, needle) : `${option.label} ${option.entity ? '' : t(option.label)} ${option.keywords ?? ''}`.toLocaleLowerCase().includes(needle)))
  }, [options, query, t, matches])
  const canCreate = Boolean(onCreate && createName && !options.some(option => option.label.toLocaleLowerCase() === createName.toLocaleLowerCase()))
  const itemCount = filtered.length + (canCreate ? 1 : 0)
  const activeIndex = itemCount ? Math.min(active, itemCount - 1) : 0
  const createActive = canCreate && activeIndex === filtered.length
  const virtual = filtered.length > 60
  const Item = contextMenu ? ContextMenu.Item : DropdownMenu.Item
  const row = (index: number, option: T) => {
    const item = <Item className={itemClassName} id={`${prefix}-${option.id}`} textValue={option.label} data-menu-active={index === activeIndex || undefined} onPointerMove={event => { event.preventDefault(); setActive(index) }} onSelect={() => onSelect(option)}>{renderOption(option)}</Item>
    return wrapOption ? wrapOption(option, item) : item
  }
  const createRow = canCreate ? <Item className={`${itemClassName ?? ''} searchable-menu-create`.trim()} id={`${prefix}-create`} textValue={`${createLabel} ${createName}`} data-menu-active={createActive || undefined} onPointerMove={event => { event.preventDefault(); setActive(filtered.length) }} onSelect={() => { void onCreate?.(createName) }}><Plus size={16}/><span data-i18n-ignore>{createLabel}: <strong data-i18n-ignore>"{createName}"</strong></span></Item> : null
  return <div className={`searchable-menu-body ${className}`} onKeyDownCapture={event => {
    if (event.key === 'Escape' || event.key === 'ArrowLeft' || event.key === 'Tab') return
    event.stopPropagation()
    let next = activeIndex
    if (event.key === 'ArrowDown') next = (activeIndex + 1) % Math.max(1, itemCount)
    else if (event.key === 'ArrowUp') next = (activeIndex + itemCount - 1) % Math.max(1, itemCount)
    else if (event.key === 'Home' && event.target !== input.current) next = 0
    else if (event.key === 'End' && event.target !== input.current) next = itemCount - 1
    else if (event.key === 'PageDown') next = Math.min(itemCount - 1, activeIndex + 8)
    else if (event.key === 'PageUp') next = Math.max(0, activeIndex - 8)
    else if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
      event.preventDefault()
      if (createActive) document.getElementById(`${prefix}-create`)?.click()
      else if (filtered[activeIndex]) document.getElementById(`${prefix}-${filtered[activeIndex].id}`)?.click()
      return
    }
    else return
    event.preventDefault()
    setActive(Math.max(0, next))
    if (next < filtered.length && virtual) list.current?.scrollIntoView({ index: Math.max(0, next), align: 'center' })
    else document.getElementById(next === filtered.length ? `${prefix}-create` : `${prefix}-${filtered[Math.max(0, next)]?.id}`)?.scrollIntoView({ block: 'nearest' })
  }}>
    <div className={`${searchClassName} searchable-menu-search`}><input ref={input} autoFocus aria-label={t(searchLabel)} aria-controls={`${prefix}-list`} aria-activedescendant={createActive ? `${prefix}-create` : filtered[activeIndex] ? `${prefix}-${filtered[activeIndex].id}` : undefined} placeholder={t(searchLabel)} value={query} onChange={event => { setQuery(event.target.value); setActive(0); list.current?.scrollTo({ top: 0 }) }}/></div>
    {filtered.length || canCreate ? virtual ? <><Virtuoso ref={list} id={`${prefix}-list`} className="searchable-menu-scroll" data={filtered} initialTopMostItemIndex={Math.min(activeIndex, Math.max(0, filtered.length - 1))} defaultItemHeight={32} fixedItemHeight={32} increaseViewportBy={64} computeItemKey={(_index, option) => option.id} itemContent={row} style={{ height: Math.min(320, filtered.length * 32 + 12) }}/>{createRow}</>
      : <div id={`${prefix}-list`} className="searchable-menu-scroll">{filtered.map((option, index) => <Fragment key={option.id}>{row(index, option)}</Fragment>)}{createRow}</div>
      : <div className="core-property-empty">{t('No results')}</div>}
  </div>
}
