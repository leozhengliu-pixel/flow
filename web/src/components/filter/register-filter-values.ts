/**
 * LS-0518 RegisterFilterValues — central registry that discovers / wires FilterBlocks.
 * Stops ad-hoc per-page value maps from remaining the only source of truth.
 */
import type { FilterBlockDefinition, FilterEntityType } from './filter-block-types'

const registry = new Map<FilterEntityType, FilterBlockDefinition[]>()
const listeners = new Set<(type: FilterEntityType) => void>()

export function registerFilterValues(type: FilterEntityType, blocks: FilterBlockDefinition[]): void {
  const deduped = dedupeById(blocks.map(block => ({ ...block, entityType: type })))
  registry.set(type, deduped)
  listeners.forEach(listener => listener(type))
}

/** Merge additional blocks into an existing pack (idempotent by id). */
export function appendFilterValues(type: FilterEntityType, blocks: FilterBlockDefinition[]): void {
  const existing = registry.get(type) ?? []
  registerFilterValues(type, [...existing, ...blocks])
}

export function getRegisteredFilterBlocks(type: FilterEntityType): FilterBlockDefinition[] {
  return registry.get(type) ?? []
}

export function getRegisteredFilterBlock(type: FilterEntityType, blockId: string): FilterBlockDefinition | undefined {
  return getRegisteredFilterBlocks(type).find(block => block.id === blockId || block.key === blockId)
}

export function listRegisteredFilterEntityTypes(): FilterEntityType[] {
  return Array.from(registry.keys())
}

export function hasRegisteredFilterValues(type: FilterEntityType): boolean {
  return (registry.get(type)?.length ?? 0) > 0
}

export function clearFilterValueRegistry(type?: FilterEntityType): void {
  if (type) registry.delete(type)
  else registry.clear()
}

export function subscribeFilterValueRegistry(listener: (type: FilterEntityType) => void): () => void {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

function dedupeById(blocks: FilterBlockDefinition[]): FilterBlockDefinition[] {
  const seen = new Set<string>()
  const result: FilterBlockDefinition[] = []
  for (const block of blocks) {
    if (seen.has(block.id)) continue
    seen.add(block.id)
    result.push(block)
  }
  return result.sort((a, b) => (a.sortPriority ?? 100) - (b.sortPriority ?? 100) || a.name.localeCompare(b.name))
}

/** All entity types the lazy companion knows how to pack. */
export const FILTER_ENTITY_TYPES: FilterEntityType[] = [
  'base',
  'issue',
  'project',
  'initiative',
  'team',
  'pullRequest',
  'notification',
  'customer',
  'searchResult',
  'document',
  'member',
  'feedItem',
  'workflowDefinition',
]
