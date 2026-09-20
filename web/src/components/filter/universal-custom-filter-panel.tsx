/**
 * LS-0618 UniversalCustomFilterPanel — shared advanced filter panel.
 * Consumes FilterBlockHelper + RegisterFilterValues + useFilterValidation.
 * Entity-specific chrome is lazy-loaded via UniversalCustomFilterPanelShouldBeLazyLoaded.
 */
import { Plus, X } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  blockSelectionToModelFilter,
  combineModelFilters,
  compareOptionsForValueType,
  createBlockSelection,
  findBlocks,
  resolveDefaultCompareOption,
} from './filter-block-helper'
import type {
  ActiveFilterBlock,
  FilterBlockDefinition,
  FilterBlockOption,
  FilterCombineOperator,
  FilterEntityType,
  FilterGroup,
  FilterModelNode,
  FilterValidationContext,
} from './filter-block-types'
import { getRegisteredFilterBlocks, hasRegisteredFilterValues } from './register-filter-values'
import { ensureFilterValuesRegistered } from './register-filter-values-lazy'
import { useFilterValidation } from './use-filter-validation'
import styles from './universal-custom-filter-panel.module.css'

export interface UniversalCustomFilterPanelProps {
  entityType: FilterEntityType
  filter?: FilterModelNode | null
  onChange: (filter: FilterModelNode) => void
  onClose?: () => void
  readOnly?: boolean
  /** Option lists keyed by block id (or key). */
  optionsFor?: (block: FilterBlockDefinition) => FilterBlockOption[]
  validationContext?: FilterValidationContext
  title?: string
  emptyLabel?: string
  className?: string
}

function newId(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`
}

function groupsFromFilter(
  filter: FilterModelNode | null | undefined,
  blocks: FilterBlockDefinition[],
): FilterGroup[] {
  if (!filter || Object.keys(filter).length === 0) return []
  const toActive = (node: FilterModelNode): ActiveFilterBlock[] =>
    findBlocks(node, blocks).map(([block, selection]) => ({
      block,
      selection,
      instanceId: newId(block.id),
    }))

  if (Array.isArray(filter.or) && filter.or.length) {
    // Top-level OR of groups (or flat OR of leaves).
    const looksLikeGroups = filter.or.some(child => Array.isArray(child.and) || Array.isArray(child.or))
    if (looksLikeGroups) {
      return filter.or.map(child => ({
        id: newId('group'),
        operator: (Array.isArray(child.or) ? 'or' : 'and') as FilterCombineOperator,
        blocks: toActive(child),
      }))
    }
    return [{ id: newId('group'), operator: 'or', blocks: toActive(filter) }]
  }
  if (Array.isArray(filter.and) && filter.and.length) {
    return [{ id: newId('group'), operator: 'and', blocks: toActive(filter) }]
  }
  return [{ id: newId('group'), operator: 'and', blocks: toActive(filter) }]
}

function groupsToFilter(groups: FilterGroup[]): FilterModelNode {
  if (!groups.length) return {}
  const groupNodes = groups.map(group => {
    const leaves = group.blocks.map(active => blockSelectionToModelFilter(active.block, active.selection))
    return combineModelFilters(group.operator, leaves)
  })
  if (groupNodes.length === 1) return groupNodes[0] ?? {}
  return combineModelFilters('or', groupNodes)
}

export function UniversalCustomFilterPanel({
  entityType,
  filter = null,
  onChange,
  onClose,
  readOnly = false,
  optionsFor,
  validationContext,
  title = 'Advanced filter',
  emptyLabel,
  className,
}: UniversalCustomFilterPanelProps) {
  const [ready, setReady] = useState(() => hasRegisteredFilterValues(entityType))
  const [blocks, setBlocks] = useState<FilterBlockDefinition[]>(() => getRegisteredFilterBlocks(entityType))
  const [groups, setGroups] = useState<FilterGroup[]>(() => groupsFromFilter(filter, getRegisteredFilterBlocks(entityType)))
  const [catalogOpen, setCatalogOpen] = useState(false)
  const [catalogGroupId, setCatalogGroupId] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    ensureFilterValuesRegistered(entityType).then(() => {
      if (cancelled) return
      const next = getRegisteredFilterBlocks(entityType)
      setBlocks(next)
      setReady(true)
      setGroups(current => (current.length ? current : groupsFromFilter(filter, next)))
    })
    return () => { cancelled = true }
  }, [entityType, filter])

  const model = useMemo(() => groupsToFilter(groups), [groups])
  const validation = useFilterValidation({
    filter: model,
    blocks,
    context: validationContext,
  })

  const commit = useCallback((next: FilterGroup[]) => {
    setGroups(next)
    onChange(groupsToFilter(next))
  }, [onChange])

  const addGroup = () => {
    commit([...groups, { id: newId('group'), operator: 'and', blocks: [] }])
    setCatalogOpen(true)
    setCatalogGroupId(null)
  }

  const addBlock = (groupId: string, block: FilterBlockDefinition) => {
    const selection = createBlockSelection([], resolveDefaultCompareOption(block))
    commit(groups.map(group => group.id === groupId
      ? { ...group, blocks: [...group.blocks, { block, selection, instanceId: newId(block.id) }] }
      : group))
    setCatalogOpen(false)
    setCatalogGroupId(null)
  }

  const ensureGroupForCatalog = (): string => {
    if (catalogGroupId && groups.some(group => group.id === catalogGroupId)) return catalogGroupId
    if (groups[0]) return groups[0].id
    const id = newId('group')
    commit([{ id, operator: 'and', blocks: [] }])
    return id
  }

  if (!ready) {
    return <div className={[styles.panel, className].filter(Boolean).join(' ')} data-state="loading"><div className={styles.loading}>Loading filters…</div></div>
  }

  if (readOnly) {
    return (
      <div className={[styles.panel, className].filter(Boolean).join(' ')} data-readonly="true">
        <div className={styles.header}><h3 className={styles.title}>{title}</h3>{onClose && <button aria-label="Close filter panel" className={styles.close} onClick={onClose} type="button"><X size={14} /></button>}</div>
        <div className={styles.readonly}>This filter is read-only.</div>
      </div>
    )
  }

  const empty = groups.every(group => group.blocks.length === 0)

  return (
    <div className={[styles.panel, className].filter(Boolean).join(' ')} data-entity={entityType} data-valid={validation.isValid ? 'true' : 'false'}>
      <div className={styles.header}>
        <h3 className={styles.title}>{title}</h3>
        {onClose && <button aria-label="Close filter panel" className={styles.close} onClick={onClose} type="button"><X size={14} /></button>}
      </div>

      {empty ? (
        <EmptyAddFilterButton
          entityType={entityType}
          label={emptyLabel}
          onAdd={() => {
            const id = groups[0]?.id ?? newId('group')
            if (!groups.length) commit([{ id, operator: 'and', blocks: [] }])
            setCatalogGroupId(id)
            setCatalogOpen(true)
          }}
        />
      ) : (
        <div className={styles.groups}>
          {groups.map(group => (
            <div className={styles.group} key={group.id}>
              <div className={styles.groupHeader}>
                <div aria-label={`Toggle filter operator, currently ${group.operator === 'and' ? 'And' : 'Or'}`} className={styles.operatorToggle} role="group">
                  {(['and', 'or'] as const).map(operator => (
                    <button
                      aria-pressed={group.operator === operator}
                      key={operator}
                      onClick={() => commit(groups.map(item => item.id === group.id ? { ...item, operator } : item))}
                      type="button"
                    >
                      {operator === 'and' ? 'And' : 'Or'}
                    </button>
                  ))}
                </div>
                <div className={styles.groupActions}>
                  <button onClick={() => { setCatalogGroupId(group.id); setCatalogOpen(true) }} type="button">Add another filter</button>
                  {groups.length > 1 && (
                    <button onClick={() => commit(groups.filter(item => item.id !== group.id))} type="button">Delete group</button>
                  )}
                </div>
              </div>
              {group.blocks.map(active => {
                const invalid = validation.highlightInvalidBlocks && validation.validateBlock(active.block, active.selection) === false
                const compareChoices = compareOptionsForValueType(active.block.valueType, active.selection.values.length || 1)
                const options = optionsFor?.(active.block) ?? []
                return (
                  <div className={styles.row} data-invalid={invalid ? 'true' : 'false'} key={active.instanceId}>
                    <select
                      aria-label="Filter field"
                      onChange={event => {
                        const nextBlock = blocks.find(block => block.id === event.target.value)
                        if (!nextBlock) return
                        commit(groups.map(item => item.id !== group.id ? item : {
                          ...item,
                          blocks: item.blocks.map(row => row.instanceId === active.instanceId
                            ? { ...row, block: nextBlock, selection: createBlockSelection([], resolveDefaultCompareOption(nextBlock)) }
                            : row),
                        }))
                      }}
                      value={active.block.id}
                    >
                      {blocks.map(block => <option key={block.id} value={block.id}>{block.name}</option>)}
                    </select>
                    <select
                      aria-label={`${active.block.name} operator`}
                      className={styles.compare}
                      onChange={event => {
                        const compareOption = event.target.value as ActiveFilterBlock['selection']['compareOption']
                        commit(groups.map(item => item.id !== group.id ? item : {
                          ...item,
                          blocks: item.blocks.map(row => row.instanceId === active.instanceId
                            ? { ...row, selection: { ...row.selection, compareOption } }
                            : row),
                        }))
                      }}
                      value={active.selection.compareOption}
                    >
                      {compareChoices.map(choice => <option key={choice.value} value={choice.value}>{choice.label}</option>)}
                    </select>
                    {active.block.inputType === 'freeForm' || options.length === 0 ? (
                      <input
                        aria-label={`${active.block.name} values`}
                        onChange={event => {
                          const values = event.target.value ? [event.target.value] : []
                          commit(groups.map(item => item.id !== group.id ? item : {
                            ...item,
                            blocks: item.blocks.map(row => row.instanceId === active.instanceId
                              ? { ...row, selection: { ...row.selection, values } }
                              : row),
                          }))
                        }}
                        placeholder="Filter…"
                        value={active.selection.values[0] ?? ''}
                      />
                    ) : (
                      <select
                        aria-label={`${active.block.name} values`}
                        multiple={false}
                        onChange={event => {
                          const values = event.target.value ? [event.target.value] : []
                          commit(groups.map(item => item.id !== group.id ? item : {
                            ...item,
                            blocks: item.blocks.map(row => row.instanceId === active.instanceId
                              ? { ...row, selection: { ...row.selection, values } }
                              : row),
                          }))
                        }}
                        value={active.selection.values[0] ?? ''}
                      >
                        <option value="">Select…</option>
                        {options.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}
                      </select>
                    )}
                    <button
                      aria-label={`Remove ${active.block.name} filter`}
                      className={styles.remove}
                      onClick={() => commit(groups.map(item => item.id !== group.id ? item : {
                        ...item,
                        blocks: item.blocks.filter(row => row.instanceId !== active.instanceId),
                      }))}
                      type="button"
                    >
                      <X size={13} />
                    </button>
                  </div>
                )
              })}
            </div>
          ))}
        </div>
      )}

      {catalogOpen && (
        <div className={styles.catalog} role="listbox" aria-label="Filter fields">
          {blocks.filter(block => !block.hideInMenuRoot).map(block => (
            <button
              key={block.id}
              onClick={() => addBlock(ensureGroupForCatalog(), block)}
              role="option"
              type="button"
            >
              {block.name}
            </button>
          ))}
        </div>
      )}

      <div className={styles.footer}>
        {!empty && (
          <button onClick={() => { setCatalogGroupId(groups[0]?.id ?? null); setCatalogOpen(true) }} type="button">
            <Plus size={12} style={{ marginRight: 4, verticalAlign: '-1px' }} />
            Add filter
          </button>
        )}
        <button onClick={addGroup} type="button">Add filter group</button>
        {!empty && (
          <button onClick={() => commit([])} type="button">Remove advanced filter</button>
        )}
      </div>
    </div>
  )
}

/** EmptyAdd* catalog entry — Linear EmptyAdd*FilterButtonShouldBeLazyLoaded parity. */
export function EmptyAddFilterButton({
  entityType,
  label,
  onAdd,
}: {
  entityType: FilterEntityType
  label?: string
  onAdd: () => void
}) {
  const copy = label ?? emptyCopyFor(entityType)
  return (
    <div className={styles.empty}>
      <strong>{copy}</strong>
      <p>Add a filter to narrow this view. Incomplete blocks are highlighted before save.</p>
      <button className={styles.emptyAdd} onClick={onAdd} type="button">
        <Plus size={12} style={{ marginRight: 4, verticalAlign: '-1px' }} />
        Add filter
      </button>
    </div>
  )
}

function emptyCopyFor(entityType: FilterEntityType): string {
  switch (entityType) {
    case 'notification': return 'Filter notifications by…'
    case 'issue': return 'Filter issues by…'
    case 'project': return 'Filter projects by…'
    case 'initiative': return 'Filter initiatives by…'
    case 'team': return 'Filter teams by…'
    case 'pullRequest': return 'Filter pull requests by…'
    case 'customer': return 'Filter customers by…'
    case 'document': return 'Filter documents by…'
    case 'member': return 'Filter members by…'
    case 'feedItem': return 'Filter feed items by…'
    case 'searchResult': return 'Filter search results by…'
    case 'workflowDefinition': return 'Filter workflow conditions by…'
    default: return 'Filter by…'
  }
}

export { groupsToFilter, groupsFromFilter }
