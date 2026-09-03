import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'

export interface PropertyCommandOption {
  id: string
  label: string
  keywords?: string
  shortcut?: string
  disabled?: boolean
}

export function usePropertyCommand<T extends PropertyCommandOption>({ autoFocus = true, closeOnSelect = true, keepSelectedVisible = false, onOpenChange, onSelect, open, options, resetKey, selectedIds = [] }: {
  autoFocus?: boolean
  closeOnSelect?: boolean
  keepSelectedVisible?: boolean
  onOpenChange: (open: boolean) => void
  onSelect: (option: T) => void | Promise<void>
  open: boolean
  options: T[]
  resetKey?: string
  selectedIds?: string[]
}) {
  const [query, setQuery] = useState('')
  const [activeId, setActiveId] = useState<string>()
  const inputRef = useRef<HTMLInputElement>(null)
  const optionsRef = useRef(options)
  optionsRef.current = options
  const selectedIdsRef = useRef(selectedIds)
  selectedIdsRef.current = selectedIds
  const normalizedQuery = query.trim().toLocaleLowerCase()
  const filteredOptions = useMemo(() => normalizedQuery ? options.filter(option => (keepSelectedVisible && selectedIds.includes(option.id)) || matchesQuery(`${option.label} ${option.keywords ?? ''}`, normalizedQuery)) : options, [keepSelectedVisible, normalizedQuery, options, selectedIds])

  useEffect(() => {
    if (!open) return
    setQuery('')
    const currentIds = selectedIdsRef.current
    setActiveId(optionsRef.current.find(option => currentIds.includes(option.id) && !option.disabled)?.id ?? optionsRef.current.find(option => !option.disabled)?.id)
    if (autoFocus) requestAnimationFrame(() => inputRef.current?.focus())
  }, [autoFocus, open, resetKey])

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
    if (!event.metaKey && !event.ctrlKey && !event.altKey && /^\d$/.test(event.key)) {
      const option = options.find(item => item.shortcut === event.key)
      if (option) { event.preventDefault(); choose(option) }
    }
  }
  const onQueryChange = (value: string) => {
    setQuery(value)
    const normalized = value.trim().toLocaleLowerCase()
    setActiveId(options.find(option => !option.disabled && matchesQuery(`${option.label} ${option.keywords ?? ''}`, normalized))?.id)
  }

  return {
    activeId,
    choose,
    filteredOptions,
    inputRef,
    isSelected: (id: string) => selectedIds.includes(id),
    onKeyDown,
    onQueryChange,
    query,
    setActiveId,
  }
}

function matchesQuery(value: string, query: string) {
  if (!query) return true
  const normalized = value.toLocaleLowerCase()
  if (normalized.includes(query)) return true
  let offset = 0
  for (const character of query) {
    offset = normalized.indexOf(character, offset)
    if (offset < 0) return false
    offset += 1
  }
  return true
}
