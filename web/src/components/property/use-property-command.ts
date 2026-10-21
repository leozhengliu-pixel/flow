import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { directoryPerson, isPeopleProperty, personMatchesQuery, type PersonIdentity } from '@/lib/people'
import { matchesSearch } from '@/lib/search-match'
import { useOptionalTranslate } from '@/i18n/i18n'
import { usePeopleDirectory } from './people-context'

export interface PropertyCommandOption {
  id: string
  label: string
  keywords?: string
  shortcut?: string
  disabled?: boolean
  kind?: string
  person?: PersonIdentity
  searchOnly?: boolean
}

export function usePropertyCommand<T extends PropertyCommandOption>({ autoFocus = true, closeOnSelect = true, keepSelectedVisible = false, pinnedIds, onOpenChange, onSelect, open, options, resetKey, selectedIds = [], personOptions = false }: {
  autoFocus?: boolean
  closeOnSelect?: boolean
  keepSelectedVisible?: boolean
  /** Options that stay listed whatever the search says (a "Custom…" row); they are highlighted when nothing else matches. */
  pinnedIds?: string[]
  onOpenChange: (open: boolean) => void
  onSelect: (option: T) => void | Promise<void>
  open: boolean
  options: T[]
  resetKey?: string
  selectedIds?: string[]
  personOptions?: boolean
}) {
  const directory = usePeopleDirectory()
  const t = useOptionalTranslate()
  // Rows show `t(label)` for system options, so a query matches the English and the displayed text.
  const matchesOption = useCallback((option: T, query: string) => {
    const person = option.person ?? (personOptions || isPeopleProperty(option.kind ?? '') ? directoryPerson(directory.users, option.id) : undefined)
    if (person) return personMatchesQuery({ ...person, ...directoryPerson(directory.users, person.id), label: option.label }, query) || matchesSearch(query, [t(option.label)])
    return matchesSearch(query, [option.label, option.keywords], t, { fuzzy: true })
  }, [directory.users, personOptions, t])
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  const optionsRef = useRef(options)
  optionsRef.current = options
  const selectedIdsRef = useRef(selectedIds)
  selectedIdsRef.current = selectedIds
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const selectionKey = JSON.stringify(selectedIds)
  const selectedSet = useMemo(() => new Set<string>(JSON.parse(selectionKey)), [selectionKey])
  const pinnedKey = (pinnedIds ?? []).join('\n')
  const pinnedSet = useMemo(() => new Set<string>(pinnedKey ? pinnedKey.split('\n') : []), [pinnedKey])
  const filteredOptions = useMemo(() => options.filter(option => pinnedSet.has(option.id) || ((!option.searchOnly || normalizedQuery.length > 1 || selectedSet.has(option.id)) && (!normalizedQuery || (keepSelectedVisible && selectedSet.has(option.id)) || matchesOption(option, normalizedQuery)))), [keepSelectedVisible, normalizedQuery, options, pinnedSet, selectedSet, matchesOption])

  useEffect(() => {
    if (!open) return
    setQuery('')
    const currentIds = selectedIdsRef.current
    setActiveId(optionsRef.current.find(option => currentIds.includes(option.id) && !option.disabled)?.id ?? optionsRef.current.find(option => !option.disabled && !option.searchOnly)?.id)
    if (autoFocus) requestAnimationFrame(() => inputRef.current?.focus())
  }, [autoFocus, open, resetKey])

  // Options can arrive after the picker opens (fetched lists); highlight the first one once they do.
  useEffect(() => {
    if (!open || activeId !== undefined) return
    const first = filteredOptions.find(option => !option.disabled && !option.searchOnly)
    if (first) setActiveId(first.id)
  }, [activeId, filteredOptions, open])

  const choose = (option: T) => {
    if (option.disabled) return
    if (closeOnSelect) onOpenChange(false)
    void onSelect(option)
  }
  const move = (offset: number) => {
    const enabled = filteredOptions.filter(option => !option.disabled)
    if (!enabled.length) return
    const current = enabled.findIndex(option => option.id === activeId)
    const next = current < 0 ? (offset > 0 ? 0 : enabled.length - 1) : (current + offset + enabled.length) % enabled.length
    setActiveId(enabled[next].id)
  }
  const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.nativeEvent.isComposing) return
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      move(event.key === 'ArrowDown' ? 1 : -1)
      return
    }
    if (event.key === 'PageDown' || event.key === 'PageUp') {
      event.preventDefault()
      move(event.key === 'PageDown' ? 8 : -8)
      return
    }
    if (event.key === 'Home' || event.key === 'End') {
      const enabled = filteredOptions.filter(option => !option.disabled)
      if (enabled.length) { event.preventDefault(); setActiveId(enabled[event.key === 'Home' ? 0 : enabled.length - 1].id) }
      return
    }
    if (event.key === 'Enter') {
      const option = filteredOptions.find(item => item.id === activeId) ?? filteredOptions.find(item => !item.disabled)
      if (option) { event.preventDefault(); choose(option) }
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      onOpenChange(false)
      return
    }
    // A digit picks the numbered row only while the search is empty; otherwise it is part of the query ("EMP-1001").
    if (!query && !event.metaKey && !event.ctrlKey && !event.altKey && /^\d$/.test(event.key)) {
      const option = options.find(item => item.shortcut === event.key)
      if (option) { event.preventDefault(); choose(option) }
    }
  }
  const onQueryChange = (value: string) => {
    setQuery(value)
    const normalized = value.trim().toLocaleLowerCase()
    const matched = options.find(option => !option.disabled && (!option.searchOnly || normalized.length > 1 || selectedSet.has(option.id)) && matchesOption(option, normalized))
    setActiveId((matched ?? options.find(option => !option.disabled && pinnedSet.has(option.id)))?.id)
  }

  return {
    activeId,
    choose,
    filteredOptions,
    inputRef,
    isSelected: (id: string) => selectedSet.has(id),
    onKeyDown,
    onQueryChange,
    query,
    setActiveId,
  }
}
