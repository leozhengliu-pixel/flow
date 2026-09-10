import * as DropdownMenu from '@radix-ui/react-dropdown-menu'
import * as ContextMenu from '@radix-ui/react-context-menu'
import { Fragment, useEffect, useId, useMemo, useRef, useState, type ReactElement, type ReactNode } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
import { useI18n } from '@/i18n/i18n'
import './searchable-menu-items.css'

export interface SearchableMenuOption { id: string; label: string; keywords?: string; entity?: boolean }

// Radix owns menu dismissal and selection; only the visible rows join its
// collection. Keyboard traversal uses the complete filtered data set.
export function SearchableMenuItems<T extends SearchableMenuOption>({ options, renderOption, onSelect, searchLabel = 'Filter…', contextMenu = false, selectedId, className = '', searchClassName = 'li-menu-search', itemClassName, wrapOption, matches }: {
  options: T[]; renderOption: (option: T) => ReactNode; onSelect: (option: T) => void; searchLabel?: string; contextMenu?: boolean
  selectedId?: string; className?: string; searchClassName?: string; itemClassName?: string; wrapOption?: (option: T, row: ReactElement) => ReactElement; matches?: (option: T, query: string) => boolean
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(() => Math.max(0, options.findIndex(option => option.id === selectedId)))
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<VirtuosoHandle>(null)
  const prefix = useId()
  useEffect(() => { const frame = requestAnimationFrame(() => input.current?.focus()); return () => cancelAnimationFrame(frame) }, [])
  const filtered = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase()
    return options.filter(option => !needle || (matches ? matches(option, needle) : `${option.label} ${option.entity ? '' : t(option.label)} ${option.keywords ?? ''}`.toLocaleLowerCase().includes(needle)))
  }, [options, query, t, matches])
  const activeIndex = Math.min(active, Math.max(0, filtered.length - 1))
  const virtual = filtered.length > 60
  const Item = contextMenu ? ContextMenu.Item : DropdownMenu.Item
  const row = (index: number, option: T) => {
    const item = <Item className={itemClassName} id={`${prefix}-${option.id}`} textValue={option.label} data-menu-active={index === activeIndex || undefined} onPointerMove={event => { event.preventDefault(); setActive(index) }} onSelect={() => onSelect(option)}>{renderOption(option)}</Item>
    return wrapOption ? wrapOption(option, item) : item
  }
  return <div className={`searchable-menu-body ${className}`} onKeyDownCapture={event => {
    if (event.key === 'Escape' || event.key === 'ArrowLeft' || event.key === 'Tab') return
    event.stopPropagation()
    let next = activeIndex
    if (event.key === 'ArrowDown') next = (activeIndex + 1) % Math.max(1, filtered.length)
    else if (event.key === 'ArrowUp') next = (activeIndex + filtered.length - 1) % Math.max(1, filtered.length)
    else if (event.key === 'Home' && event.target !== input.current) next = 0
    else if (event.key === 'End' && event.target !== input.current) next = filtered.length - 1
    else if (event.key === 'PageDown') next = Math.min(filtered.length - 1, activeIndex + 8)
    else if (event.key === 'PageUp') next = Math.max(0, activeIndex - 8)
    else if (event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); if (filtered[activeIndex]) document.getElementById(`${prefix}-${filtered[activeIndex].id}`)?.click(); return }
    else return
    event.preventDefault()
    setActive(Math.max(0, next))
    if (virtual) list.current?.scrollIntoView({ index: Math.max(0, next), align: 'center' })
    else document.getElementById(`${prefix}-${filtered[Math.max(0, next)]?.id}`)?.scrollIntoView({ block: 'nearest' })
  }}>
    <div className={`${searchClassName} searchable-menu-search`}><input ref={input} autoFocus aria-label={t(searchLabel)} aria-controls={`${prefix}-list`} aria-activedescendant={filtered[activeIndex] ? `${prefix}-${filtered[activeIndex].id}` : undefined} placeholder={t(searchLabel)} value={query} onChange={event => { setQuery(event.target.value); setActive(0); list.current?.scrollTo({ top: 0 }) }}/></div>
    {filtered.length ? virtual ? <Virtuoso ref={list} id={`${prefix}-list`} className="searchable-menu-scroll" data={filtered} initialTopMostItemIndex={activeIndex} defaultItemHeight={32} fixedItemHeight={32} increaseViewportBy={64} computeItemKey={(_index, option) => option.id} itemContent={row} style={{ height: Math.min(320, filtered.length * 32 + 12) }}/>
      : <div id={`${prefix}-list`} className="searchable-menu-scroll">{filtered.map((option, index) => <Fragment key={option.id}>{row(index, option)}</Fragment>)}</div>
      : <div className="core-property-empty">{t('No results')}</div>}
  </div>
}
