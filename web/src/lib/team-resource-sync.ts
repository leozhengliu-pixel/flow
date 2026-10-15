/**
 * Team overview resources (pinned documents and links) and their sections,
 * written optimistically: every change is applied to the open page and the
 * workspace snapshot at once through TEAM_RESOURCES_CHANGED, the request runs
 * in the background, and a failed request puts the previous state back.
 * Nothing waits for a refetch of the list or of workspace metadata.
 */
import {
  createTeamResourceSection, deleteTeamResource, deleteTeamResourceSection, fetchTeamResources, pinTeamResource, updateTeamResource, updateTeamResourceSection,
} from '@/lib/api'
import type { BootstrapData, RealtimeEvent, TeamPinnedResource, TeamResourceSection } from '@/types/flow'

export const TEAM_RESOURCES_CHANGED = 'flow:team-resources-changed'

export interface TeamResourcesChange {
  teamId: string
  /** Replaces the team's resources and sections (a refetch). */
  replace?: { resources: TeamPinnedResource[]; sections: TeamResourceSection[] }
  upsert?: TeamPinnedResource[]
  remove?: string[]
  upsertSections?: TeamResourceSection[]
  removeSections?: string[]
}

export interface TeamResourceState {
  resources: TeamPinnedResource[]
  sections: TeamResourceSection[]
}

function merge<T extends { id: string }>(items: T[], upsert: T[] = [], remove: string[] = []): T[] {
  if (!upsert.length && !remove.length) return items
  const removed = new Set(remove)
  const incoming = new Map(upsert.map(item => [item.id, item]))
  const kept = items.filter(item => !removed.has(item.id) || incoming.has(item.id)).map(item => {
    const next = incoming.get(item.id)
    if (next) incoming.delete(item.id)
    return next ?? item
  })
  return [...kept, ...incoming.values()]
}

/** Applies a change to a list that may hold several teams' rows (the workspace snapshot) or one team's. */
export function applyTeamResourcesChange(state: TeamResourceState, change: TeamResourcesChange): TeamResourceState {
  let { resources, sections } = state
  if (change.replace) {
    resources = [...resources.filter(item => item.teamId !== change.teamId), ...change.replace.resources]
    sections = [...sections.filter(item => item.teamId !== change.teamId), ...change.replace.sections]
  }
  resources = merge(resources, change.upsert, change.remove)
  sections = merge(sections, change.upsertSections, change.removeSections)
  return resources === state.resources && sections === state.sections ? state : { resources, sections }
}

export function publishTeamResourcesChange(change: TeamResourcesChange) {
  window.dispatchEvent(new CustomEvent<TeamResourcesChange>(TEAM_RESOURCES_CHANGED, { detail: change }))
}

export function onTeamResourcesChange(listener: (change: TeamResourcesChange) => void) {
  const handle = (event: Event) => listener((event as CustomEvent<TeamResourcesChange>).detail)
  window.addEventListener(TEAM_RESOURCES_CHANGED, handle)
  return () => window.removeEventListener(TEAM_RESOURCES_CHANGED, handle)
}

/** The team a team.resource_* realtime event belongs to, when this client can tell. */
export function teamResourceEventTeam(data: Pick<BootstrapData, 'teams' | 'teamPinnedResources' | 'teamResourceSections'>, event: Pick<RealtimeEvent, 'aggregateId' | 'payload'>): string | undefined {
  const id = event.aggregateId ?? ''
  // Pins and new sections name the team; other writes name the row.
  if (data.teams.some(team => team.id === id)) return id
  const entity = event.payload?.entity as { teamId?: unknown } | undefined
  if (entity && typeof entity.teamId === 'string') return entity.teamId
  return (data.teamPinnedResources ?? []).find(item => item.id === id)?.teamId ?? (data.teamResourceSections ?? []).find(item => item.id === id)?.teamId
}

/**
 * Another client changed a team's overview resources: refetch that team's
 * small list instead of the workspace. False when the event's team is unknown.
 */
export async function refreshTeamResourcesForEvent(data: Pick<BootstrapData, 'teams' | 'teamPinnedResources' | 'teamResourceSections'>, event: Pick<RealtimeEvent, 'type' | 'aggregateId' | 'payload'>): Promise<boolean> {
  if (!/^team\.resource_/.test(event.type)) return false
  const teamId = teamResourceEventTeam(data, event)
  if (!teamId) return false
  try {
    const result = await fetchTeamResources(teamId)
    publishTeamResourcesChange({ teamId, replace: { resources: result.resources, sections: result.sections } })
  } catch {
    // A team the viewer cannot read has nothing to show.
  }
  return true
}

const PENDING = 'pending:'
let sequence = 0
/** Pin requests by their temporary id. */
const pendingPins = new Map<string, Promise<TeamPinnedResource | undefined>>()
/** Temporary pins removed before their request finished. */
const cancelledPins = new Set<string>()
/** Latest local write per resource or section; an older failure must not undo a newer change. */
const latestWrite = new Map<string, number>()

export function isPendingTeamResource(id: string) {
  return id.startsWith(PENDING)
}

function nextPosition(items: { position: number }[]) {
  return items.reduce((value, item) => Math.max(value, item.position + 1), 0)
}

/**
 * Runs `write` against the saved row: at once for a saved row, after the pin
 * request for a temporary one (skipped when that pin failed).
 */
function withSaved(resource: TeamPinnedResource, write: (item: TeamPinnedResource) => Promise<unknown>): Promise<TeamPinnedResource | undefined> {
  const pending = pendingPins.get(resource.id)
  if (!pending) return write(resource).then(() => resource)
  return pending.then(item => item ? write(item).then(() => item) : undefined)
}

export type TeamResourceInput = Pick<TeamPinnedResource, 'resourceType' | 'title'> & Partial<Pick<TeamPinnedResource, 'resourceId' | 'url' | 'sectionId'>>

/** Adds a resource at the end of the team's list; resolves with the saved row. */
export function pinResource(teamId: string, current: TeamPinnedResource[], input: TeamResourceInput): Promise<TeamPinnedResource> {
  const now = new Date().toISOString()
  const temporary: TeamPinnedResource = {
    id: `${PENDING}${++sequence}`, teamId, sectionId: input.sectionId ?? '', resourceType: input.resourceType, resourceId: input.resourceId ?? '',
    url: input.url, title: input.title, position: nextPosition(current.filter(item => item.teamId === teamId)), createdAt: now, updatedAt: now,
  }
  const request = pinTeamResource(teamId, input).then(item => {
    const cancelled = cancelledPins.delete(temporary.id)
    publishTeamResourcesChange({ teamId, remove: [temporary.id], upsert: cancelled || !item?.id ? [] : [item] })
    return item
  }, (error: unknown) => {
    cancelledPins.delete(temporary.id)
    publishTeamResourcesChange({ teamId, remove: [temporary.id] })
    throw error
  })
  // Kept after it settles: a row rendered before the swap can still name the temporary id.
  pendingPins.set(temporary.id, request.then(item => item, () => undefined))
  publishTeamResourcesChange({ teamId, upsert: [temporary] })
  return request
}

/** Removes a resource from the team's list (also one whose pin is still saving). */
export async function unpinResource(teamId: string, resource: TeamPinnedResource): Promise<void> {
  const write = (latestWrite.get(resource.id) ?? 0) + 1
  latestWrite.set(resource.id, write)
  if (isPendingTeamResource(resource.id)) cancelledPins.add(resource.id)
  // The request starts before the change renders.
  let target = resource
  const request = withSaved(resource, item => {
    target = item
    // A pin that finished saving first already swapped in its saved row.
    if (item.id !== resource.id) publishTeamResourcesChange({ teamId, remove: [item.id] })
    return deleteTeamResource(teamId, item.id)
  })
  publishTeamResourcesChange({ teamId, remove: [resource.id] })
  try {
    await request
  } catch (error) {
    if (latestWrite.get(resource.id) === write) publishTeamResourcesChange({ teamId, upsert: [target] })
    throw error
  }
}

/** Moves, renames or re-links a resource. */
export async function updateResource(teamId: string, resource: TeamPinnedResource, patch: { sectionId?: string; position?: number; title?: string; url?: string }): Promise<void> {
  const write = (latestWrite.get(resource.id) ?? 0) + 1
  latestWrite.set(resource.id, write)
  let target = resource
  const request = withSaved(resource, item => {
    target = item
    // The saved row replaced the temporary one without this patch.
    if (item.id !== resource.id) publishTeamResourcesChange({ teamId, upsert: [{ ...item, ...patch }] })
    return updateTeamResource(teamId, item.id, patch)
  })
  publishTeamResourcesChange({ teamId, upsert: [{ ...resource, ...patch }] })
  try {
    await request
  } catch (error) {
    if (latestWrite.get(resource.id) === write) publishTeamResourcesChange({ teamId, upsert: [target] })
    throw error
  }
}

/** Creates a section at the end of the team's sections. */
export async function createSection(teamId: string, name: string): Promise<TeamResourceSection> {
  const section = await createTeamResourceSection(teamId, name)
  if (section?.id) publishTeamResourcesChange({ teamId, upsertSections: [section] })
  return section
}

/** Renames or reorders a section. */
export async function updateSection(teamId: string, section: TeamResourceSection, patch: { name?: string; position?: number }): Promise<void> {
  const write = (latestWrite.get(section.id) ?? 0) + 1
  latestWrite.set(section.id, write)
  const request = updateTeamResourceSection(teamId, section.id, patch)
  publishTeamResourcesChange({ teamId, upsertSections: [{ ...section, ...patch }] })
  try {
    await request
  } catch (error) {
    if (latestWrite.get(section.id) === write) publishTeamResourcesChange({ teamId, upsertSections: [section] })
    throw error
  }
}

/** Deletes a section; its resources stay pinned, outside any section. */
export async function deleteSection(teamId: string, section: TeamResourceSection, resources: TeamPinnedResource[]): Promise<void> {
  const contained = resources.filter(item => item.teamId === teamId && item.sectionId === section.id)
  const request = deleteTeamResourceSection(teamId, section.id)
  publishTeamResourcesChange({ teamId, removeSections: [section.id], upsert: contained.map(item => ({ ...item, sectionId: '' })) })
  try {
    await request
  } catch (error) {
    publishTeamResourcesChange({ teamId, upsertSections: [section], upsert: contained })
    throw error
  }
}
