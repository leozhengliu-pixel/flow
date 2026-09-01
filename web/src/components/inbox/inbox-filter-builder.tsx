import { cloneElement, isValidElement, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactElement, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import * as Popover from '@radix-ui/react-popover'
import { Check, ChevronRight, CircleDotDashed, Flag, GitPullRequest, Plus, UserRound, X } from 'lucide-react'

import { PriorityIcon, ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { usePropertyCommand } from '@/components/property/use-property-command'
import { CheckboxMark } from '@/components/ui/checkbox-mark'
import { useI18n } from '@/i18n/i18n'

import {
  normalizeInboxFilters,
  removeInboxFilter,
  toggleInboxFilterValue,
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
    setOpen(true)
    requestAnimationFrame(() => propertySearchRef.current?.focus())
  }, [setOpen])

  const updateFilters = useCallback((nextFilters: InboxFilterCondition[]) => {
    onFiltersChange(normalizeInboxFilters(nextFilters))
  }, [onFiltersChange])

  return (
    <>
      <span className={styles.triggerAnchor} ref={triggerAnchorRef}>
        <Popover.Root open={open} onOpenChange={setOpen}>
          <Popover.Trigger asChild>{toolbarTrigger}</Popover.Trigger>
          <Popover.Portal>
            <Popover.Content
              className={styles.propertyMenu}
              side="bottom"
              align="start"
              sideOffset={4}
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
                filters={normalizedFilters}
                options={fieldOptions}
                searchRef={propertySearchRef}
                onActivate={setActiveProperty}
                onToggleValue={(property, option) => updateFilters(toggleInboxFilterValue(normalizedFilters, property, option))}
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
          onToggleValue={(property, option) => updateFilters(toggleInboxFilterValue(normalizedFilters, property, option))}
        />,
        filterBarHost,
      ) : null}
    </>
  )
}

function PropertyPicker({
  activeProperty,
  filters,
  options,
  searchRef,
  onActivate,
  onToggleValue,
  onValueSelected,
  onReturnToProperties,
}: {
  activeProperty?: InboxFilterProperty
  filters: InboxFilterCondition[]
  options: InboxFilterOptions
  searchRef: RefObject<HTMLInputElement | null>
  onActivate: (property: InboxFilterProperty | undefined) => void
  onToggleValue: (property: InboxFilterProperty, option: InboxFilterOption) => void
  onValueSelected: () => void
  onReturnToProperties: () => void
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [focusValuePicker, setFocusValuePicker] = useState(false)
  const hoverTimerRef = useRef<number | undefined>(undefined)
  const visibleProperties = properties.filter(property => property.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))

  useEffect(() => setActiveIndex(0), [query])
  useEffect(() => () => window.clearTimeout(hoverTimerRef.current), [])

  const openProperty = (property: InboxFilterProperty, focus = true) => {
    window.clearTimeout(hoverTimerRef.current)
    setQuery('')
    setFocusValuePicker(focus)
    onActivate(property)
  }

  const previewProperty = (property: InboxFilterProperty, index: number) => {
    setActiveIndex(index)
    window.clearTimeout(hoverTimerRef.current)
    hoverTimerRef.current = window.setTimeout(() => openProperty(property, false), 90)
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
      const next = (activeIndex - 1 + visibleProperties.length) % visibleProperties.length
      setActiveIndex(next)
      openProperty(visibleProperties[next].id, false)
    } else if (event.key === 'Enter') {
      event.preventDefault()
      openProperty(visibleProperties[Math.min(activeIndex, visibleProperties.length - 1)].id)
    } else if (event.key === 'Home') {
      event.preventDefault()
      setActiveIndex(0)
    } else if (event.key === 'End') {
      event.preventDefault()
      setActiveIndex(visibleProperties.length - 1)
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
        onChange={event => setQuery(event.target.value)}
        onKeyDown={onKeyDown}
      />
      <kbd aria-hidden="true">F</kbd>
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
                aria-haspopup="dialog"
                onFocus={() => previewProperty(property.id, index)}
                onPointerEnter={() => previewProperty(property.id, index)}
                onClick={() => openProperty(property.id)}
              >
                <PropertyIcon property={property.id} />
                <span>{t(property.label)}</span>
                <ChevronRight aria-hidden="true" />
              </button>
            </Popover.Anchor>
            {activeProperty === property.id ? (
              <ValuePicker
                property={property}
                filters={filters}
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
  filters,
  options,
  autoFocus,
  onClose,
  onToggle,
  onReturnToProperties,
}: {
  property: { id: InboxFilterProperty; label: string }
  filters: InboxFilterCondition[]
  options: InboxFilterOption[]
  autoFocus: boolean
  onClose: () => void
  onToggle: (option: InboxFilterOption) => void
  onReturnToProperties: () => void
}) {
  const { t } = useI18n()
  const [showUnmatched, setShowUnmatched] = useState(false)
  const condition = filters.find(filter => filter.property === property.id)
  const selectedIds = condition?.values.map(value => value.value) ?? []
  const command = usePropertyCommand({
    autoFocus,
    closeOnSelect: false,
    open: true,
    options,
    selectedIds,
    onOpenChange: open => { if (!open) onClose() },
    onSelect: onToggle,
  })
  const canCollapseUnmatched = property.id === 'notificationType' && !command.query.trim() && !showUnmatched
  const matchingOptions = canCollapseUnmatched ? command.filteredOptions.filter(option => (option.count ?? 0) > 0) : command.filteredOptions
  const unmatchedCount = canCollapseUnmatched ? command.filteredOptions.length - matchingOptions.length : 0
  const showSearch = options.length > 2

  return (
    <Popover.Portal>
      <Popover.Content
        className={styles.valueMenu}
        data-property={property.id}
        side="right"
        align="start"
        alignOffset={-40}
        sideOffset={-2}
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
        <FilterValueList activeId={command.activeId} inputRef={command.inputRef} isSelected={command.isSelected} onActive={command.setActiveId} onChoose={command.choose} onQuery={command.onQueryChange} options={matchingOptions} placeholder={showUnmatched && property.id === 'notificationType' ? t(property.label) : t('Filter…')} property={property} query={command.query} showSearch={showSearch} footer={unmatchedCount ? <>
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
  onToggleValue: (property: InboxFilterProperty, option: InboxFilterOption) => void
}) {
  const { t } = useI18n()
  return <section className={styles.appliedBar} data-inbox-filter-bar aria-label="Applied filters">
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
  onToggleValue: (property: InboxFilterProperty, option: InboxFilterOption) => void
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
    onSelect: option => onToggleValue(property.id, option),
  })

  return <div className={styles.condition}>
      <span className={styles.conditionField}>
        <PropertyIcon property={property.id} />
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
          <FilterValueSummary values={condition.values} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          className={styles.conditionValueMenu}
          side="bottom"
          align="start"
          sideOffset={4}
          collisionPadding={10}
          aria-label={`${t('Filter')} ${t(property.label)}`}
          onOpenAutoFocus={event => event.preventDefault()}
          onKeyDown={command.onKeyDown}
        >
          <FilterValueList activeId={command.activeId} inputRef={command.inputRef} isSelected={command.isSelected} onActive={command.setActiveId} onChoose={command.choose} onQuery={command.onQueryChange} options={command.filteredOptions} placeholder="Filter..." property={property} query={command.query}/>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
    <button className={styles.removeCondition} type="button" aria-label={`${t('Remove')} ${t(property.label)} ${t('filter')}`} title={`${t('Remove')} ${t(property.label)} ${t('filter')}`} onClick={() => onRemove(condition.id)}>
      <X aria-hidden="true" />
    </button>
  </div>
}

function FilterValueList({ activeId, footer, inputRef, isSelected, onActive, onChoose, onQuery, options, placeholder, property, query, showSearch = true }: { activeId?: string; footer?: ReactElement; inputRef: RefObject<HTMLInputElement | null>; isSelected: (id:string)=>boolean; onActive:(id:string)=>void; onChoose:(option:InboxFilterOption)=>void; onQuery:(value:string)=>void; options:InboxFilterOption[]; placeholder:string; property:{id:InboxFilterProperty;label:string}; query:string; showSearch?:boolean }) {
  const { t } = useI18n()
  const label = t(property.label)
  return <>{showSearch&&<div className={styles.valueSearch}><input ref={inputRef} role="searchbox" aria-label={label} placeholder={placeholder} value={query} onChange={event=>onQuery(event.target.value)}/></div>}<div className={styles.valueList} role="listbox" aria-label={label} aria-multiselectable="true">{!options.length&&!footer?<div className={styles.empty}>{t('No results')}</div>:null}{options.map(option=><FilterValueOption active={activeId===option.id} checked={isSelected(option.id)} key={option.id} onActive={()=>onActive(option.id)} onChoose={()=>onChoose(option)} option={option} property={property.id}/>)}{footer}</div></>
}

function FilterValueOption({ active, checked, option, property, onActive, onChoose }: { active: boolean; checked: boolean; option: InboxFilterOption; property: InboxFilterProperty; onActive: () => void; onChoose: () => void }) {
  return <button className={styles.valueItem} type="button" role="option" aria-selected={active} aria-checked={checked} disabled={option.disabled} onMouseMove={onActive} onClick={onChoose}>
    <span className={styles.checkbox}>{checked ? <CheckboxMark/> : null}</span>
    <OptionVisual option={option} property={property}/>
    <span className={styles.valueLabel} data-i18n-ignore={option.i18nIgnore || undefined}>{option.label}</span>
    {option.count != null ? <span className={styles.valueCount}>{option.count} {option.count === 1 ? 'notification' : 'notifications'}</span> : null}
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
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const visibleOperators = operatorOptions.filter(option => option.label.includes(query.trim().toLocaleLowerCase()))

  useEffect(() => {
    if (!open) return
    setQuery('')
    setActiveIndex(Math.max(0, operatorOptions.findIndex(option => option.id === condition.operator)))
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
      const option = visibleOperators[Math.min(activeIndex, visibleOperators.length - 1)]
      if (option) choose(option.id)
    }
  }

  const operatorLabel = condition.operator === 'is' ? t('is') : t('is not')
  return <Popover.Root open={open} onOpenChange={setOpen}>
    <Popover.Trigger asChild>
      <button className={styles.conditionOperator} type="button" aria-label={`${t(label)} ${t('operator')}`} aria-expanded={open}>{operatorLabel}</button>
    </Popover.Trigger>
    <Popover.Portal>
      <Popover.Content
        className={styles.operatorMenu}
        side="bottom"
        align="start"
        sideOffset={4}
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
            aria-checked={condition.operator === operator.id}
            onMouseMove={() => setActiveIndex(index)}
            onClick={() => choose(operator.id)}
          >
            <span>{operator.label}</span>
            {condition.operator === operator.id ? <Check aria-hidden="true" /> : null}
          </button>)}
          {!visibleOperators.length ? <div className={styles.empty}>No results</div> : null}
        </div>
      </Popover.Content>
    </Popover.Portal>
  </Popover.Root>
}

function FilterValueSummary({ values }: { values: InboxFilterCondition['values'] }) {
  if (values.length === 1) {
    const value = values[0]
    return <>
      {value.avatarUrl ? <img className={styles.summaryAvatar} src={value.avatarUrl} alt="" /> : value.color ? <i className={styles.summaryDot} style={{ backgroundColor: value.color }} /> : null}
      <span>{value.valueLabel}</span>
    </>
  }

  return <>
    <span className={styles.summaryStack} aria-hidden="true">
      {values.slice(0, 3).map((value, index) => value.avatarUrl
        ? <img key={value.value} src={value.avatarUrl} alt="" style={{ zIndex: 3 - index }} />
        : <i key={value.value} style={{ backgroundColor: value.color ?? `lch(${62 - index * 8}% 1.2 272)`, zIndex: 3 - index }} />)}
    </span>
    <span>{values.map(value => value.valueLabel).join(', ')}</span>
  </>
}

function DefaultTrigger({ count, ariaLabel }: { count: number; ariaLabel: string }) {
  return <button className={styles.defaultTrigger} type="button" aria-label={ariaLabel}>
    <InboxFilterGlyph />
    {count ? <span>{count}</span> : null}
  </button>
}

function PropertyIcon({ property }: { property: InboxFilterProperty }) {
  if (property === 'notificationType') return <CircleDotDashed aria-hidden="true" />
  if (property === 'from') return <UserRound aria-hidden="true" />
  if (property === 'project') return <ProjectIcon aria-hidden="true" />
  if (property === 'initiative') return <Flag aria-hidden="true" />
  if (property === 'issuePriority') return <PriorityIcon priority={2} aria-hidden="true" />
  if (property === 'reviewStatus') return <GitPullRequest aria-hidden="true" />
  return <StatusIcon state={{ id: 'unstarted', name: 'Unstarted', type: 'unstarted', color: 'var(--theme-text-secondary)' }} size={15} />
}

function OptionVisual({ option, property }: { option: InboxFilterOption; property: InboxFilterProperty }) {
  if (option.avatarUrl) return <img className={styles.avatar} src={option.avatarUrl} alt="" />
  if (option.icon) return <span className={styles.optionIcon}>{option.icon}</span>
  if (option.color) return <i className={styles.colorDot} style={{ backgroundColor: option.color }} />
  return <span className={styles.optionIcon}><PropertyIcon property={property} /></span>
}

function InboxFilterGlyph() {
  return <svg viewBox="0 0 16 16" aria-hidden="true"><path fillRule="evenodd" clipRule="evenodd" d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z" /></svg>
}
