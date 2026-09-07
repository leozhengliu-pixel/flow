import { cloneElement, forwardRef, isValidElement, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type KeyboardEvent as ReactKeyboardEvent, type ReactElement, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import * as Popover from '@radix-ui/react-popover'
import { Plus, X } from 'lucide-react'

import { PriorityIcon, ProjectIcon, ReviewStatusGlyph, ReviewStatusValueGlyph, WorkflowStatusPropertyGlyph } from '@/components/issue/issue-icons'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { useI18n } from '@/i18n/i18n'

import {
  appendInboxFilterValue,
  normalizeInboxFilters,
  removeInboxFilter,
  toggleInboxFilterConditionValue,
  updateInboxFilterOperator,
  INBOX_REVIEW_STATUS_OPTIONS,
  type InboxFilterCondition,
  type InboxFilterOption,
  type InboxFilterOptions,
  type InboxFilterOperator,
  type InboxFilterProperty,
} from './inbox-filter-types'
import styles from './inbox-filter-builder.module.css'

export type {
  InboxFilterCondition,
  InboxFilterOperator,
  InboxFilterOption,
  InboxFilterOptions,
  InboxFilterProperty,
  InboxFilterValue,
} from './inbox-filter-types'

export interface InboxFilterBuilderProps {
  /** Pass the existing compact Inbox toolbar button to retain exact shell geometry. */
  trigger?: ReactElement
  /** Controlled conditions, applied immediately when a value is selected. */
  filters: InboxFilterCondition[]
  options?: InboxFilterOptions
  onFiltersChange: (filters: InboxFilterCondition[]) => void
  /** Optional controlled open state for command-bar and keyboard integrations. */
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

const properties: Array<{ id: InboxFilterProperty; label: string }> = [
  { id: 'notificationType', label: 'Notification type' },
  { id: 'from', label: 'From' },
  { id: 'project', label: 'Project' },
  { id: 'initiative', label: 'Initiative' },
  { id: 'issuePriority', label: 'Issue priority' },
  { id: 'issueStatusType', label: 'Issue status type' },
  { id: 'reviewStatus', label: 'Review status' },
]

const operatorOptions: Array<{ id: InboxFilterOperator; label: string }> = [
  { id: 'is', label: 'is' },
  { id: 'isNot', label: 'is not' },
]

const standardOptions: InboxFilterOptions = {
  notificationType: [
    { id: 'assignment', label: 'Assignments', keywords: 'assigned assignment' },
    { id: 'comment', label: 'Comments and replies', keywords: 'commented reply comment' },
    { id: 'mention', label: 'Mentions', keywords: 'mentioned mention' },
    { id: 'status', label: 'Issue updates', keywords: 'status state issue' },
    { id: 'project', label: 'Project updates', keywords: 'project' },
  ],
  issuePriority: [
    { id: '0', label: 'No priority', keywords: 'none', icon: <PriorityIcon priority={0} /> },
    { id: '1', label: 'Urgent', icon: <PriorityIcon priority={1} /> },
    { id: '2', label: 'High', icon: <PriorityIcon priority={2} /> },
    { id: '3', label: 'Medium', icon: <PriorityIcon priority={3} /> },
    { id: '4', label: 'Low', icon: <PriorityIcon priority={4} /> },
  ],
  issueStatusType: [
    { id: 'backlog', label: 'Backlog' },
    { id: 'unstarted', label: 'Unstarted' },
    { id: 'started', label: 'Started', keywords: 'in progress' },
    { id: 'completed', label: 'Completed', keywords: 'done closed' },
    { id: 'canceled', label: 'Canceled', keywords: 'cancelled' },
  ],
  reviewStatus: [...INBOX_REVIEW_STATUS_OPTIONS],
}

/**
 * An Inbox-only filter menu. Its trigger is intentionally supplied by the
 * owning toolbar so it can inherit the measured 28px header action surface.
 */
export function InboxFilterBuilder({
  trigger,
  filters,
  options,
  onFiltersChange,
  open: controlledOpen,
  onOpenChange,
}: InboxFilterBuilderProps) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false)
  const [activeProperty, setActiveProperty] = useState<InboxFilterProperty>()
  const [keyboardMode, setKeyboardMode] = useState(false)
  const [filterBarHost, setFilterBarHost] = useState<HTMLElement | null>(null)
  const triggerAnchorRef = useRef<HTMLSpanElement>(null)
  const propertySearchRef = useRef<HTMLInputElement>(null)
  const open = controlledOpen ?? uncontrolledOpen
  const normalizedFilters = useMemo(() => normalizeInboxFilters(filters), [filters])
  const fieldOptions = useMemo<InboxFilterOptions>(() => ({ ...standardOptions, ...options }), [options])

  const setOpen = useCallback((next: boolean) => {
    if (!next) setActiveProperty(undefined)
    if (controlledOpen === undefined) setUncontrolledOpen(next)
    onOpenChange?.(next)
  }, [controlledOpen, onOpenChange])

  useLayoutEffect(() => {
    const nextHost = triggerAnchorRef.current?.closest<HTMLElement>('.flow-inbox__list-pane') ?? null
    setFilterBarHost(nextHost)
  }, [])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.metaKey || event.ctrlKey || event.altKey || event.key.toLowerCase() !== 'f') return
      const target = event.target as HTMLElement | null
      if (target?.closest('input, textarea, select, [contenteditable="true"], [role="textbox"]')) return
      event.preventDefault()
      setKeyboardMode(true)
      setOpen(true)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [setOpen])

  const toolbarTrigger = trigger && isValidElement(trigger)
    ? cloneElement(trigger, {
      'aria-expanded': open,
      'aria-haspopup': 'dialog',
      'aria-label': normalizedFilters.length ? 'Add another filter' : 'Add filter',
      label: normalizedFilters.length ? 'Add another filter' : 'Add filter',
    } as object)
    : <DefaultTrigger count={normalizedFilters.length} ariaLabel={normalizedFilters.length ? 'Add another filter' : 'Add filter'} />

  const returnToPropertyPicker = useCallback(() => {
    setActiveProperty(undefined)
    requestAnimationFrame(() => propertySearchRef.current?.focus())
  }, [])

  const addAnotherFilter = useCallback(() => {
    setActiveProperty(undefined)
    setKeyboardMode(false)
    setOpen(true)
    requestAnimationFrame(() => propertySearchRef.current?.focus())
  }, [setOpen])

  const updateFilters = useCallback((nextFilters: InboxFilterCondition[]) => {
    onFiltersChange(normalizeInboxFilters(nextFilters))
  }, [onFiltersChange])

  return (
    <>
      <span className={styles.triggerAnchor} ref={triggerAnchorRef} onPointerDown={() => setKeyboardMode(false)}>
        <Popover.Root open={open} onOpenChange={setOpen}>
          <Popover.Trigger asChild>{toolbarTrigger}</Popover.Trigger>
          <Popover.Portal>
            <Popover.Content data-flow-motion="floating"
              className={styles.propertyMenu}
              data-keyboard-mode={keyboardMode}
              side="bottom"
              align="start"
              sideOffset={3.5}
              collisionPadding={10}
              aria-label="Add filter"
              onOpenAutoFocus={event => {
                event.preventDefault()
                requestAnimationFrame(() => propertySearchRef.current?.focus())
              }}
              onEscapeKeyDown={event => {
                if (!activeProperty) return
                event.preventDefault()
                returnToPropertyPicker()
              }}
            >
              <PropertyPicker
                activeProperty={activeProperty}
                keyboardMode={keyboardMode}
                options={fieldOptions}
                searchRef={propertySearchRef}
                onActivate={setActiveProperty}
                onToggleValue={(property, option) => updateFilters(appendInboxFilterValue(normalizedFilters, property, option))}
                onValueSelected={() => setOpen(false)}
                onReturnToProperties={returnToPropertyPicker}
              />
            </Popover.Content>
          </Popover.Portal>
        </Popover.Root>
      </span>
      {filterBarHost && normalizedFilters.length ? createPortal(
        <AppliedFilterBar
          filters={normalizedFilters}
          options={fieldOptions}
          onAdd={addAnotherFilter}
          onClear={() => updateFilters([])}
          onOperatorChange={(id, operator) => updateFilters(updateInboxFilterOperator(normalizedFilters, id, operator))}
          onRemove={id => updateFilters(removeInboxFilter(normalizedFilters, id))}
          onToggleValue={(conditionId, option) => updateFilters(toggleInboxFilterConditionValue(normalizedFilters, conditionId, option))}
        />,
        filterBarHost,
      ) : null}
    </>
  )
}

function PropertyPicker({
  activeProperty,
  keyboardMode,
  options,
  searchRef,
  onActivate,
  onToggleValue,
  onValueSelected,
  onReturnToProperties,
}: {
  activeProperty?: InboxFilterProperty
  keyboardMode: boolean
  options: InboxFilterOptions
  searchRef: RefObject<HTMLInputElement | null>
  onActivate: (property: InboxFilterProperty | undefined) => void
  onToggleValue: (property: InboxFilterProperty, option: InboxFilterOption) => void
  onValueSelected: () => void
  onReturnToProperties: () => void
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(keyboardMode ? 0 : -1)
  const [focusValuePicker, setFocusValuePicker] = useState(false)
  const hoverTimerRef = useRef<number | undefined>(undefined)
  const visibleProperties = properties.filter(property => property.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))

  useEffect(() => () => window.clearTimeout(hoverTimerRef.current), [])

  const openProperty = (property: InboxFilterProperty, focus = true) => {
    window.clearTimeout(hoverTimerRef.current)
    setFocusValuePicker(focus)
    onActivate(property)
  }

  const previewProperty = (property: InboxFilterProperty, index: number) => {
    setActiveIndex(index)
    window.clearTimeout(hoverTimerRef.current)
    hoverTimerRef.current = window.setTimeout(() => openProperty(property, false), 0)
  }

  const onKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Escape') {
      if (query) {
        event.preventDefault()
        setQuery('')
      }
      return
    }
    if (!visibleProperties.length) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      const next = (activeIndex + 1) % visibleProperties.length
      setActiveIndex(next)
      openProperty(visibleProperties[next].id, false)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      const next = activeIndex < 0 ? visibleProperties.length - 1 : (activeIndex - 1 + visibleProperties.length) % visibleProperties.length
      setActiveIndex(next)
      openProperty(visibleProperties[next].id, false)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      openProperty(visibleProperties[Math.max(0, Math.min(activeIndex, visibleProperties.length - 1))].id)
    } else if (event.key === 'Home') {
      event.preventDefault()
      setActiveIndex(0)
      openProperty(visibleProperties[0].id, false)
    } else if (event.key === 'End') {
      event.preventDefault()
      setActiveIndex(visibleProperties.length - 1)
      openProperty(visibleProperties.at(-1)!.id, false)
    }
  }

  return <div className={styles.root}>
    <div className={styles.propertySearch}>
      <input
        ref={searchRef}
        type="search"
        aria-label="Add Filter"
        placeholder="Add Filter..."
        value={query}
        onChange={event => {
          setQuery(event.target.value)
          setActiveIndex(0)
        }}
        onKeyDown={onKeyDown}
      />
      {keyboardMode ? <kbd aria-hidden="true">F</kbd> : null}
    </div>
    <div className={styles.propertyList} role="listbox" aria-label="Filter property">
      {visibleProperties.map((property, index) => {
        const valueOptions = options[property.id] ?? []
        return (
          <Popover.Root key={property.id} open={activeProperty === property.id} onOpenChange={next => onActivate(next ? property.id : undefined)}>
            <Popover.Anchor asChild>
              <button
                className={styles.propertyItem}
                type="button"
                role="option"
                aria-selected={activeIndex === index}
                aria-expanded={activeProperty === property.id}
                aria-haspopup="dialog"
                onFocus={() => previewProperty(property.id, index)}
                onPointerEnter={() => previewProperty(property.id, index)}
                onPointerLeave={() => window.clearTimeout(hoverTimerRef.current)}
                onClick={() => openProperty(property.id)}
              >
                <PropertyIcon property={property.id} />
                <span>{t(property.label)}</span>
                <span className={styles.propertyArrow} aria-hidden="true"><span>▶</span></span>
              </button>
            </Popover.Anchor>
            {activeProperty === property.id ? (
              <ValuePicker
                property={property}
                options={valueOptions}
                autoFocus={focusValuePicker}
                onClose={() => onActivate(undefined)}
                onToggle={option => {
                  onToggleValue(property.id, option)
                  onValueSelected()
                }}
                onReturnToProperties={onReturnToProperties}
              />
            ) : null}
          </Popover.Root>
        )
      })}
      {!visibleProperties.length ? <div className={styles.empty}>No filters found.</div> : null}
    </div>
  </div>
}

function ValuePicker({
  property,
  options,
  autoFocus,
  onClose,
  onToggle,
  onReturnToProperties,
}: {
  property: { id: InboxFilterProperty; label: string }
  options: InboxFilterOption[]
  autoFocus: boolean
  onClose: () => void
  onToggle: (option: InboxFilterOption) => void
  onReturnToProperties: () => void
}) {
  const { t } = useI18n()
  const [showUnmatched, setShowUnmatched] = useState(false)
  const command = usePropertyCommand({
    autoFocus,
    closeOnSelect: false,
    open: true,
    options,
    selectedIds: [],
    onOpenChange: open => { if (!open) onClose() },
    onSelect: onToggle,
  })
  const canCollapseUnmatched = property.id === 'notificationType' && !command.query.trim() && !showUnmatched
  const matchingOptions = canCollapseUnmatched ? command.filteredOptions.filter(option => (option.count ?? 0) > 0) : command.filteredOptions
  const unmatchedCount = canCollapseUnmatched ? command.filteredOptions.length - matchingOptions.length : 0
  const showSearch = options.length > 2
  const visibleSearch = showSearch && property.id !== 'notificationType'

  return (
    <Popover.Portal>
        <Popover.Content data-flow-motion="floating"
          className={`${styles.valueMenu} ${property.id === 'reviewStatus' ? styles.reviewStatusMenu : ''}`}
        data-property={property.id}
        side="right"
        align="start"
        // Menus with a visible search header align that header above the
        // property row; compact menus align directly with the row itself.
        alignOffset={visibleSearch ? -43 : -6.5}
        sideOffset={-1.5}
        collisionPadding={10}
        aria-label={`${t('Filter')} ${t(property.label)}`}
        onOpenAutoFocus={event => event.preventDefault()}
        onEscapeKeyDown={event => {
          event.preventDefault()
          onClose()
          onReturnToProperties()
        }}
        onKeyDown={command.onKeyDown}
      >
        <FilterValueList activeId={command.activeId} inputRef={command.inputRef} isSelected={command.isSelected} onActive={command.setActiveId} onChoose={command.choose} onQuery={command.onQueryChange} options={matchingOptions} placeholder={showUnmatched && property.id === 'notificationType' ? t(property.label) : t('Filter…')} property={property} query={command.query} showSearch={showSearch} hideSearch={property.id === 'notificationType'} footer={unmatchedCount ? <>
            <div className={styles.valueSeparator} role="separator" />
            <button className={styles.unmatchedItem} type="button" role="option" aria-selected="false" onClick={() => setShowUnmatched(true)}>
              <span>{unmatchedCount} {t('options not matching any notifications')}</span>
            </button>
          </> : undefined}/>
      </Popover.Content>
    </Popover.Portal>
  )
}

function AppliedFilterBar({
  filters,
  options,
  onAdd,
  onClear,
  onOperatorChange,
  onRemove,
  onToggleValue,
}: {
  filters: InboxFilterCondition[]
  options: InboxFilterOptions
  onAdd: () => void
  onClear: () => void
  onOperatorChange: (id: string, operator: InboxFilterOperator) => void
  onRemove: (id: string) => void
  onToggleValue: (conditionId: string, option: InboxFilterOption) => void
}) {
  const { t } = useI18n()
  const barRef = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const bar = barRef.current
    const host = bar?.parentElement
    if (!bar || !host) return
    const updateHeight = () => host.style.setProperty('--inbox-filter-bar-height', `${bar.getBoundingClientRect().height}px`)
    updateHeight()
    if (typeof ResizeObserver === 'undefined') return () => host.style.removeProperty('--inbox-filter-bar-height')
    const observer = new ResizeObserver(updateHeight)
    observer.observe(bar)
    return () => {
      observer.disconnect()
      host.style.removeProperty('--inbox-filter-bar-height')
    }
  }, [])
  return <section ref={barRef} className={styles.appliedBar} data-inbox-filter-bar aria-label="Applied filters">
    <div className={styles.appliedFilters}>
      {filters.map(filter => {
        const property = properties.find(item => item.id === filter.property)
        if (!property) return null
        return <AppliedCondition
          condition={filter}
          key={filter.id}
          options={options[filter.property] ?? []}
          property={property}
          onOperatorChange={onOperatorChange}
          onRemove={onRemove}
          onToggleValue={onToggleValue}
        />
      })}
      <button className={styles.addCondition} type="button" aria-label={t('Add another filter')} title={t('Add another filter')} onClick={onAdd}>
        <Plus aria-hidden="true" />
      </button>
    </div>
    {filters.length > 1 ? <button className={styles.clearAll} type="button" onClick={onClear}>{t('Clear')}</button> : null}
  </section>
}

function AppliedCondition({
  condition,
  options,
  property,
  onOperatorChange,
  onRemove,
  onToggleValue,
}: {
  condition: InboxFilterCondition
  options: InboxFilterOption[]
  property: { id: InboxFilterProperty; label: string }
  onOperatorChange: (id: string, operator: InboxFilterOperator) => void
  onRemove: (id: string) => void
  onToggleValue: (conditionId: string, option: InboxFilterOption) => void
}) {
  const { t } = useI18n()
  const [valuesOpen, setValuesOpen] = useState(false)
  const selectedIds = useMemo(() => condition.values.map(value => value.value), [condition.values])
  const command = usePropertyCommand({
    closeOnSelect: true,
    open: valuesOpen,
    options,
    selectedIds,
    onOpenChange: setValuesOpen,
    onSelect: option => onToggleValue(condition.id, option),
  })

  return <div className={styles.condition}>
      <span className={styles.conditionField}>
        <span>{t(property.label)}</span>
    </span>
    <OperatorMenu
      condition={condition}
      label={property.label}
      onChange={operator => onOperatorChange(condition.id, operator)}
    />
    <Popover.Root open={valuesOpen} onOpenChange={setValuesOpen}>
      <Popover.Trigger asChild>
        <button className={styles.conditionValue} type="button" aria-label={property.label}>
          <FilterValueSummary property={property.id} values={condition.values} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating"
          className={styles.conditionValueMenu}
          data-property={property.id}
          side="bottom"
          align="start"
          sideOffset={4.5}
          collisionPadding={10}
          aria-label={`${t('Filter')} ${t(property.label)}`}
          onOpenAutoFocus={event => event.preventDefault()}
          onKeyDown={command.onKeyDown}
        >
          <FilterValueList activeId={command.activeId} groupSelected inputRef={command.inputRef} isSelected={command.isSelected} onActive={command.setActiveId} onChoose={command.choose} onQuery={command.onQueryChange} options={command.filteredOptions} placeholder="Filter..." property={property} query={command.query}/>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
    <button className={styles.removeCondition} type="button" aria-label={`${t('Remove')} ${t(property.label)} ${t('filter')}`} title={`${t('Remove')} ${t(property.label)} ${t('filter')}`} onClick={() => onRemove(condition.id)}>
      <X aria-hidden="true" />
    </button>
  </div>
}

function FilterValueList({ activeId, footer, groupSelected = false, hideSearch = false, inputRef, isSelected, onActive, onChoose, onQuery, options, placeholder, property, query, showSearch = true }: { activeId?: string; footer?: ReactElement; groupSelected?: boolean; hideSearch?: boolean; inputRef: RefObject<HTMLInputElement | null>; isSelected: (id:string)=>boolean; onActive:(id:string)=>void; onChoose:(option:InboxFilterOption)=>void; onQuery:(value:string)=>void; options:InboxFilterOption[]; placeholder:string; property:{id:InboxFilterProperty;label:string}; query:string; showSearch?:boolean }) {
  const { t } = useI18n()
  const label = t(property.label)
  const selectedOptions = groupSelected ? options.filter(option => isSelected(option.id)) : []
  const remainingOptions = groupSelected ? options.filter(option => !isSelected(option.id)) : options
  const renderOption = (option: InboxFilterOption) => <FilterValueOption active={activeId===option.id} checked={isSelected(option.id)} key={option.id} onActive={()=>onActive(option.id)} onChoose={()=>onChoose(option)} option={option} property={property.id}/>
  return <>{showSearch&&<div className={`${styles.valueSearch} ${hideSearch ? styles.valueSearchHidden : ''}`}><input ref={inputRef} role="searchbox" aria-label={label} placeholder={placeholder} value={query} onChange={event=>onQuery(event.target.value)}/></div>}<div className={styles.valueList} role="listbox" aria-label={label} aria-multiselectable="true">{!options.length&&!footer?<div className={styles.empty}>{t('No results')}</div>:null}{selectedOptions.map(renderOption)}{selectedOptions.length > 0 && remainingOptions.length > 0 ? <div className={styles.valueSeparator} role="separator" /> : null}{remainingOptions.map(renderOption)}{footer}</div></>
}

function FilterValueOption({ active, checked, option, property, onActive, onChoose }: { active: boolean; checked: boolean; option: InboxFilterOption; property: InboxFilterProperty; onActive: () => void; onChoose: () => void }) {
  return <button className={styles.valueItem} type="button" role="option" aria-selected={active} aria-checked={checked} disabled={option.disabled} onMouseMove={onActive} onClick={onChoose}>
    <span className={styles.checkbox}>{checked ? <CheckboxMark/> : null}</span>
    <OptionVisual option={option} property={property}/>
    <span className={styles.valueLabel} data-i18n-ignore={option.i18nIgnore || undefined}>{option.label}</span>
    {option.count ? <span className={styles.valueCount}>{option.count} {option.count === 1 ? 'notification' : 'notifications'}</span> : null}
  </button>
}

function OperatorMenu({
  condition,
  label,
  onChange,
}: {
  condition: InboxFilterCondition
  label: string
  onChange: (operator: InboxFilterOperator) => void
}) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(-1)
  const inputRef = useRef<HTMLInputElement>(null)
  const contextualOperators = condition.values.length > 1
    ? operatorOptions.map(option => option.id === 'is' ? { ...option, label: 'is any of' } : option)
    : operatorOptions
  const visibleOperators = contextualOperators.filter(option => option.label.includes(query.trim().toLocaleLowerCase()))

  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveIndex(-1)
    requestAnimationFrame(() => inputRef.current?.focus())
  }, [condition.operator, open])

  const choose = (operator: InboxFilterOperator) => {
    onChange(operator)
    setOpen(false)
  }

  const onKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault()
      if (visibleOperators.length) setActiveIndex(index => (index + (event.key === 'ArrowDown' ? 1 : -1) + visibleOperators.length) % visibleOperators.length)
      return
    }
    if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      setActiveIndex(event.key === 'Home' ? 0 : Math.max(0, visibleOperators.length - 1))
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      const option = visibleOperators[Math.max(0, Math.min(activeIndex, visibleOperators.length - 1))]
      if (option) choose(option.id)
    }
  }

  const operatorLabel = condition.operator === 'is'
    ? t(condition.values.length > 1 ? 'is any of' : 'is')
    : t('is not')
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>
      <button className={styles.conditionOperator} type="button" aria-label={`${t(label)} ${t('operator')}`} aria-expanded={open}>{operatorLabel}</button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content data-flow-motion="floating"
        className={styles.operatorMenu}
        side="bottom"
        align="start"
        sideOffset={4.5}
        collisionPadding={10}
        aria-label={`${t(label)} ${t('operator')}`}
        onOpenAutoFocus={event => event.preventDefault()}
      >
        <div className={styles.operatorSearch}>
          <input
            ref={inputRef}
            role="searchbox"
            aria-label={`${t(label)} ${t('operator')}`}
            placeholder={t('Filter…')}
            value={query}
            onChange={event => {
              setQuery(event.target.value)
              setActiveIndex(0)
            }}
            onKeyDown={onKeyDown}
          />
        </div>
        <div className={styles.operatorList} role="listbox" aria-label={`${t(label)} ${t('operators')}`}>
          {visibleOperators.map((operator, index) => <button
            className={styles.operatorItem}
            type="button"
            key={operator.id}
            role="option"
            aria-selected={activeIndex === index}
            onMouseMove={() => setActiveIndex(index)}
            onClick={() => choose(operator.id)}
          >
            <span>{t(operator.label)}</span>
          </button>)}
          {!visibleOperators.length ? <div className={styles.empty}>No results</div> : null}
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function FilterValueSummary({ property, values }: { property?: InboxFilterProperty; values: InboxFilterCondition['values'] }) {
  const { t } = useI18n()
  if (values.length === 1) return <span>{values[0].valueLabel}</span>
  return <span>{t(`${values.length} ${filterValueNoun(property)}`)}</span>
}

function filterValueNoun(property?: InboxFilterProperty) {
  if (property === 'issueStatusType') return 'status types'
  if (property === 'issuePriority') return 'priorities'
  if (property === 'reviewStatus') return 'review statuses'
  if (property === 'notificationType') return 'notification types'
  if (property === 'from') return 'people'
  if (property === 'project') return 'projects'
  if (property === 'initiative') return 'initiatives'
  return 'values'
}

const DefaultTrigger = forwardRef<HTMLButtonElement, ComponentProps<'button'> & { count: number; ariaLabel: string }>(function DefaultTrigger({ count, ariaLabel, ...props }, ref) {
  return <button {...props} ref={ref} className={styles.defaultTrigger} type="button" aria-label={props['aria-label'] ?? ariaLabel}>
    <InboxFilterGlyph />
    {count ? <span>{count}</span> : null}
  </button>
})

function PropertyIcon({ property }: { property: InboxFilterProperty }) {
  if (property === 'notificationType') return <InboxFilterPropertyGlyph property={property} />
  if (property === 'from') return <InboxFilterPropertyGlyph property={property} />
  if (property === 'project') return <ProjectIcon aria-hidden="true" />
  if (property === 'initiative') return <InboxFilterPropertyGlyph property={property} />
  if (property === 'issuePriority') return <PriorityIcon priority={2} aria-hidden="true" />
  if (property === 'reviewStatus') return <ReviewStatusGlyph />
  return <span className={styles.statusPropertyIcon} aria-hidden="true"><WorkflowStatusPropertyGlyph color="var(--inbox-filter-muted)" size={14} /></span>
}

/**
 * The category picker uses the product glyph set rather than the generic
 * outline icon library. Keep the paths inline so the 16px geometry remains
 * byte-for-byte stable across themes and browsers.
 */
function InboxFilterPropertyGlyph({ property }: { property: InboxFilterProperty }) {
  const common = { width: 16, height: 16, viewBox: '0 0 16 16', fill: 'currentColor', 'aria-hidden': true as const }
  if (property === 'notificationType') return <svg {...common}><path fillRule="evenodd" clipRule="evenodd" d="M1 5.25C1 2.90279 2.90279 1 5.25 1H7.25C7.66421 1 8 1.33579 8 1.75C8 2.16421 7.66421 2.5 7.25 2.5H5.25C3.73122 2.5 2.5 3.73122 2.5 5.25V10.75C2.5 12.2688 3.73122 13.5 5.25 13.5H10.75C12.2688 13.5 13.5 12.2688 13.5 10.75V8.75287C13.5 8.33865 13.8358 8.00287 14.25 8.00287C14.6642 8.00287 15 8.33865 15 8.75287V10.75C15 13.0972 13.0972 15 10.75 15H5.25C2.90279 15 1 13.0972 1 10.75V5.25Z" /><path d="M15 3.5C15 4.88071 13.8807 6 12.5 6C11.1193 6 10 4.88071 10 3.5C10 2.11929 11.1193 1 12.5 1C13.8807 1 15 2.11929 15 3.5Z" /></svg>
  if (property === 'from') return <svg {...common}><path fillRule="evenodd" clipRule="evenodd" d="M12.9998 9C13.2759 9 13.4998 9.22386 13.4998 9.5V10C13.7759 10 13.9998 10.2239 13.9998 10.5H14.4998C14.7759 10.5 14.9998 10.7239 14.9998 11C14.9998 11.2761 14.7759 11.5 14.4998 11.5H13.9998V12.5H14.4998C14.7759 12.5 14.9998 12.7239 14.9998 13C14.9998 13.2761 14.7759 13.5 14.4998 13.5H13.9998C13.9998 13.7761 13.7759 14 13.4998 14V14.5C13.4998 14.7761 13.2759 15 12.9998 15C12.7238 14.9998 12.4998 14.776 12.4998 14.5V14H11.4998V14.5C11.4998 14.7761 11.2759 15 10.9998 15C10.7238 15 10.4998 14.776 10.4998 14.5V14C10.2238 13.9998 9.99978 13.776 9.99978 13.5H9.49978C9.22377 13.4998 8.99978 13.276 8.99978 13C8.99978 12.724 9.22377 12.5002 9.49978 12.5H9.99978V11.5H9.49978C9.22377 11.4998 8.99978 11.276 8.99978 11C8.99978 10.724 9.22377 10.5002 9.49978 10.5H9.99978C9.99978 10.224 10.2238 10.0002 10.4998 10V9.5C10.4998 9.22395 10.7238 9.00015 10.9998 9C11.2759 9 11.4998 9.22386 11.4998 9.5V10H12.4998V9.5C12.4998 9.22395 12.7238 9.00015 12.9998 9Z" /><path fillRule="evenodd" clipRule="evenodd" d="M7.42459 8C7.8388 8 8.17459 8.33579 8.17459 8.75C8.17459 9.16421 7.8388 9.5 7.42459 9.5C6.00949 9.50021 4.75699 10.4161 4.32791 11.7646L3.46463 14.4775C3.33896 14.8721 2.91689 15.0904 2.52224 14.9648C2.12786 14.8391 1.90948 14.417 2.03494 14.0225L2.89822 11.3096C3.52541 9.33868 5.35633 8.00021 7.42459 8Z" /><path fillRule="evenodd" clipRule="evenodd" d="M7.99978 1C9.51857 1 10.7498 2.23122 10.7498 3.75C10.7498 5.26878 9.51857 6.5 7.99978 6.5C6.48126 6.4997 5.24978 5.2686 5.24978 3.75C5.24978 2.2314 6.48126 1.0003 7.99978 1Z" /></svg>
  if (property === 'initiative') return <svg {...common}><path fillRule="evenodd" clipRule="evenodd" d="M7.4145 8.3381C7.68162 7.8873 8.31838 7.8873 8.5855 8.3381L11.896 13.925C12.2589 14.5374 11.6035 15.2506 10.9879 14.9132L8.10753 13.3343C8.04032 13.2975 7.95967 13.2975 7.89247 13.3343L5.0121 14.9132C4.39652 15.2506 3.74112 14.5374 4.10401 13.925L7.4145 8.3381Z" /><path fillRule="evenodd" clipRule="evenodd" d="M13.5 8C13.5 4.96243 11.0376 2.5 8 2.5C4.96243 2.5 2.5 4.96243 2.5 8C2.5 8.96927 2.75037 9.87822 3.18945 10.668L3.38867 10.999L3.42773 11.0654C3.60231 11.4033 3.495 11.825 3.16992 12.0371C2.84468 12.249 2.41642 12.1766 2.17773 11.8809L2.13281 11.8184L2.00195 11.6104C1.36597 10.5558 1 9.31963 1 8C1 4.13401 4.13401 1 8 1C11.866 1 15 4.13401 15 8C15 9.40749 14.5834 10.7198 13.8672 11.8184L13.8223 11.8809C13.5836 12.1766 13.1553 12.249 12.8301 12.0371C12.4831 11.8109 12.3854 11.346 12.6113 10.999L12.8105 10.668C13.2496 9.87822 13.5 8.96927 13.5 8Z" /></svg>
  return null
}

function OptionVisual({ option, property }: { option: InboxFilterOption; property: InboxFilterProperty }) {
  if (option.avatarUrl) return <img className={styles.avatar} src={option.avatarUrl} alt="" />
  if (option.icon) return <span className={styles.optionIcon} aria-hidden="true">{option.icon}</span>
  // Review status values use the filled branch glyph from the review bundle,
  // not a generic color dot. Keep the exact shape at the 16px picker size.
  if (property === 'reviewStatus') return <span className={styles.optionIcon} aria-hidden="true"><ReviewStatusValueGlyph status={option.id} className={styles.reviewStatusGlyph} color={reviewStatusColor(option.id)} /></span>
  if (option.color) return <i className={styles.colorDot} style={{ backgroundColor: option.color }} />
  return <span className={styles.optionIcon} aria-hidden="true"><PropertyIcon property={property} /></span>
}

function reviewStatusColor(status: string) {
  if (status === 'draft') return 'lch(64.714% 1.425 272)'
  if (status === 'merged') return 'lch(48% 59.31 288.43)'
  if (status === 'closed') return 'lch(58% 73 29)'
  return 'lch(60% 64.37 141.95)'
}

function InboxFilterGlyph() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z" /></svg>
}
