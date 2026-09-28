/**
 * LS-0213 DocumentFilterBlocks — Creator + Project + Dates first.
 * Named registry for documents index filter depth (team/archived remain on the page toolbar).
 */
import { isThisMonth, isThisWeek, isToday } from 'date-fns'
import type { BootstrapData, FlowDocument } from '@/types/flow'

export type DocumentFilterBlockId = 'creator' | 'project' | 'dates' | 'created' | 'updated'

export interface DocumentFilterBlockDefinition {
  id: DocumentFilterBlockId
  key: string
  name: string
  valueType: 'equalValue' | 'date'
  sortPriority: number
  defaultCompareOption: string
}

/** Linear DocumentFilterBlocks: Creator, Project, Dates (Created/Updated) first. */
export const documentFilterBlocks: DocumentFilterBlockDefinition[] = [
  { id: 'creator', key: 'creatorId', name: 'Creator', valueType: 'equalValue', sortPriority: 10, defaultCompareOption: 'is' },
  { id: 'project', key: 'projectId', name: 'Project', valueType: 'equalValue', sortPriority: 20, defaultCompareOption: 'is' },
  { id: 'dates', key: 'updatedAt', name: 'Dates', valueType: 'date', sortPriority: 30, defaultCompareOption: 'within' },
  { id: 'created', key: 'createdAt', name: 'Created', valueType: 'date', sortPriority: 40, defaultCompareOption: 'within' },
  { id: 'updated', key: 'updatedAt', name: 'Updated', valueType: 'date', sortPriority: 50, defaultCompareOption: 'within' },
]

export const groupedDocumentFilterBlocks = [
  { id: 'people', name: 'People', blocks: documentFilterBlocks.filter(block => block.id === 'creator') },
  { id: 'relations', name: 'Relations', blocks: documentFilterBlocks.filter(block => block.id === 'project') },
  { id: 'dates', name: 'Dates', blocks: documentFilterBlocks.filter(block => block.id === 'dates' || block.id === 'created' || block.id === 'updated') },
]

export type DocumentDatePreset = 'today' | 'week' | 'month' | 'older'

export const documentDatePresets: { id: DocumentDatePreset; label: string }[] = [
  { id: 'today', label: 'Edited today' },
  { id: 'week', label: 'Edited this week' },
  { id: 'month', label: 'Edited this month' },
  { id: 'older', label: 'Older' },
]

export interface DocumentIndexFilters {
  creatorId: string
  projectId: string
  dates: DocumentDatePreset | ''
}

export function emptyDocumentIndexFilters(): DocumentIndexFilters {
  return { creatorId: '', projectId: '', dates: '' }
}

export function parseDocumentIndexFilters(search: string): DocumentIndexFilters {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const dates = params.get('dates') ?? ''
  return {
    creatorId: params.get('creatorId') ?? '',
    projectId: params.get('projectId') ?? '',
    dates: documentDatePresets.some(preset => preset.id === dates) ? dates as DocumentDatePreset : '',
  }
}

export function documentMatchesDatePreset(document: FlowDocument, preset: DocumentDatePreset, field: 'createdAt' | 'updatedAt' = 'updatedAt') {
  const date = new Date(document[field])
  if (preset === 'today') return isToday(date)
  if (preset === 'week') return isThisWeek(date)
  if (preset === 'month') return isThisMonth(date)
  return !isThisMonth(date)
}

export function matchDocumentFilters(document: FlowDocument, filters: DocumentIndexFilters) {
  if (filters.creatorId && document.creator.id !== filters.creatorId) return false
  if (filters.projectId && !document.projectIds.includes(filters.projectId)) return false
  if (filters.dates && !documentMatchesDatePreset(document, filters.dates)) return false
  return true
}

export function documentFilterCreatorOptions(data: BootstrapData, documents: FlowDocument[]) {
  const seen = new Map<string, { id: string; label: string }>()
  for (const document of documents) {
    if (!seen.has(document.creator.id)) seen.set(document.creator.id, { id: document.creator.id, label: document.creator.displayName })
  }
  return [...seen.values()].sort((a, b) => a.label.localeCompare(b.label))
}

export function documentFilterProjectOptions(data: BootstrapData, documents: FlowDocument[]) {
  return data.projects
    .filter(project => documents.some(document => document.projectIds.includes(project.id)))
    .map(project => ({ id: project.id, label: project.name, color: project.color }))
    .sort((a, b) => a.label.localeCompare(b.label))
}
