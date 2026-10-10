import { describe, expect, it } from 'vitest'

import { makeBootstrap, project, teammate, viewer } from '@/test/fixtures'
import type { BootstrapData, DocumentPermission, FlowDocument } from '@/types/flow'
import { createAdvancedFilter } from '@/components/issue-explorer/advanced-filter'
import type { MyIssuesAppliedFilter } from '@/components/my-issues/my-issues-filter-types'
import type { MyIssuesFilterKey } from '@/components/my-issues/my-issues-surface'
import {
  CURRENT_USER_FILTER, documentFilterOptions, documentMatchesFilters, groupDocuments, matchesDocumentFilter, persistDocumentDirectoryState, projectHidden, readDocumentDirectoryState, sortDocuments,
} from './team-documents.model'

const t = (source: string) => source
const permission = (documentId: string, subjectId: string, role: DocumentPermission['role']): DocumentPermission => ({ id: `${documentId}:${subjectId}`, documentId, subjectType: 'user', subjectId, role, createdAt: '', updatedAt: '' })
const doc = (id: string, overrides: Partial<FlowDocument> = {}): FlowDocument => ({
  id, slugId: id, title: id, content: '', creator: viewer, teamIds: ['team-1'], projectIds: [], subscriberIds: [], favorite: false, revisions: [],
  createdAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-02T00:00:00Z', ...overrides,
})
const chip = (field: MyIssuesFilterKey, values: string[], operator: MyIssuesAppliedFilter['operator'] = 'is'): MyIssuesAppliedFilter => ({
  id: `${field}-chip`, field, fieldLabel: field, operator, value: values[0], valueLabel: values[0], values: values.map(value => ({ value, valueLabel: value })),
})
const data = makeBootstrap({ cycles: [], initiatives: [] }) as BootstrapData
const team = data.teams[0]

describe('team documents model', () => {
  it('lists project documents under their project and the rest under "Team documents"', () => {
    const groups = groupDocuments([doc('a'), doc('b', { projectIds: [project.id] })], 'project', data, team)
    expect(groups.map(group => [group.name, group.items.map(item => item.id)])).toEqual([['Team documents', ['a']], [project.name, ['b']]])
    expect(groups[0].icon?.kind).toBe('team')
    expect(groups[1].icon?.kind).toBe('project')
  })

  it('treats a document with an explicit permission list and no owner as ownerless', () => {
    const ownerless = doc('a', { permissions: [permission('a', teammate.id, 'editor')] })
    const owned = doc('b', { permissions: [permission('b', teammate.id, 'owner')] })
    const legacy = doc('c')
    const groups = groupDocuments([ownerless, owned, legacy], 'owner', data, team)
    expect(groups.map(group => group.name)).toEqual([teammate.displayName, viewer.displayName, 'No owner'])
    expect(documentMatchesFilters(ownerless, [chip('owner', [''])], data)).toBe(true)
    expect(documentMatchesFilters(owned, [chip('owner', [''])], data)).toBe(false)
    expect(documentMatchesFilters(owned, [chip('owner', [teammate.id])], data)).toBe(true)
  })

  it('sorts by owner name with ownerless documents first and never throws', () => {
    const sorted = sortDocuments([doc('a', { permissions: [permission('a', teammate.id, 'owner')] }), doc('b', { permissions: [permission('b', teammate.id, 'editor')] }), doc('c')], 'owner', false, data, t)
    expect(sorted.map(item => item.id)).toEqual(['b', 'a', 'c'])
  })

  it('sorts untitled documents by their display title', () => {
    const sorted = sortDocuments([doc('a', { title: 'Zebra' }), doc('b', { title: '' })], 'name', false, data, t)
    expect(sorted.map(item => item.id)).toEqual(['b', 'a'])
  })

  it('combines chips with AND and negates "is not"', () => {
    const document = doc('a')
    expect(documentMatchesFilters(document, [chip('creator', [viewer.id]), chip('project', ['other'])], data)).toBe(false)
    expect(documentMatchesFilters(document, [chip('creator', [viewer.id]), chip('project', [''])], data)).toBe(true)
    expect(documentMatchesFilters(document, [chip('creator', [viewer.id], 'isNot')], data)).toBe(false)
    expect(documentMatchesFilters(doc('b', { projectIds: [project.id] }), [chip('project', [project.id])], data)).toBe(true)
  })

  it('resolves "Current user" to the viewer for creator and owner', () => {
    const mine = doc('a', { permissions: [permission('a', viewer.id, 'owner')] })
    const theirs = doc('b', { creator: teammate, permissions: [permission('b', teammate.id, 'owner')] })
    expect(matchesDocumentFilter(mine, chip('owner', [CURRENT_USER_FILTER]), data)).toBe(true)
    expect(matchesDocumentFilter(theirs, chip('owner', [CURRENT_USER_FILTER]), data)).toBe(false)
    expect(matchesDocumentFilter(theirs, chip('creator', [CURRENT_USER_FILTER], 'isNot'), data)).toBe(true)
  })

  it('compares created and updated dates with before / after presets', () => {
    const now = Date.parse('2026-10-09T00:00:00Z')
    const old = doc('a', { createdAt: '2026-08-01T00:00:00Z', updatedAt: '2026-10-08T00:00:00Z' })
    expect(matchesDocumentFilter(old, chip('dates', ['created:-1m'], 'before'), data, now)).toBe(true)
    expect(matchesDocumentFilter(old, chip('dates', ['created:-1m'], 'after'), data, now)).toBe(false)
    expect(matchesDocumentFilter(old, chip('dates', ['updated:-1w']), data, now)).toBe(true)
    expect(matchesDocumentFilter(old, chip('dates', ['due:+1w']), data, now)).toBe(false)
  })

  it('evaluates the advanced filter tree with and / or', () => {
    const owned = doc('a', { projectIds: [project.id] })
    const tree = (conjunction: 'and' | 'or') => createAdvancedFilter({ id: 't', conjunction, items: [
      { id: 'c1', field: 'creator', fieldLabel: 'Creator', operator: 'is', values: [{ value: teammate.id, valueLabel: 'x' }] },
      { id: 'c2', field: 'project', fieldLabel: 'Project', operator: 'is', values: [{ value: project.id, valueLabel: 'y' }] },
    ] })
    expect(matchesDocumentFilter(owned, tree('and'), data)).toBe(false)
    expect(matchesDocumentFilter(owned, tree('or'), data)).toBe(true)
    expect(matchesDocumentFilter(owned, createAdvancedFilter(), data)).toBe(true)
  })

  it('offers Linear\'s value lists: people with the agent last, No owner / No project, created and updated dates', () => {
    const owners = documentFilterOptions('owner', data)!
    expect(owners.slice(0, 2).map(option => option.label)).toEqual(['No owner', 'Current user'])
    expect(owners[0]).toMatchObject({ id: '', kind: 'owner' })
    expect(owners[1]).toMatchObject({ id: CURRENT_USER_FILTER, kind: 'currentUser' })
    expect(documentFilterOptions('creator', data)!.map(option => option.label)[0]).toBe('Current user')
    const agent = { ...teammate, id: 'agent-1', displayName: 'Flow', app: true }
    const withAgent = { ...data, users: [agent, ...data.users] } as BootstrapData
    const people = documentFilterOptions('creator', withAgent)!
    expect(people.at(-1)).toMatchObject({ id: 'agent-1', agent: true })
    expect(documentFilterOptions('project', data)![0]).toMatchObject({ id: '', label: 'No project' })
    expect(documentFilterOptions('dates', data)!.map(option => option.id)).toEqual(['created-date', 'updated-date'])
    expect(documentFilterOptions('status', data)).toBeUndefined()
  })

  it('hides documents of inactive or other people\'s projects', () => {
    const done = { ...project, status: { ...project.status, type: 'completed' as const } }
    expect(projectHidden(done, data, false, false)).toBe(true)
    expect(projectHidden(done, data, true, false)).toBe(false)
    expect(projectHidden({ ...project, lead: teammate, memberIds: [] }, data, true, true)).toBe(true)
    expect(projectHidden(undefined, data, false, true)).toBe(false)
  })

  it('groups by recency into only the buckets that have documents', () => {
    const groups = groupDocuments([doc('a', { updatedAt: new Date().toISOString() }), doc('b', { updatedAt: '2020-01-01T00:00:00Z' })], 'recency', data, team)
    expect(groups.map(group => group.name)).toEqual(['Today', 'Older'])
  })

  it('reads display options from the address bar with Linear defaults', () => {
    const defaults = readDocumentDirectoryState('')
    expect([defaults.grouping, defaults.ordering, defaults.descending, [...defaults.properties].sort()]).toEqual(['project', 'name', false, ['created', 'owner', 'updated']])
    const custom = readDocumentDirectoryState('?doc-group=owner&doc-order=project&doc-direction=desc&doc-columns=owner')
    expect([custom.grouping, custom.ordering, custom.descending, [...custom.properties], custom.filters]).toEqual(['owner', 'project', true, ['owner'], []])
  })

  it('round-trips applied filters through the address bar', () => {
    const state = readDocumentDirectoryState('')
    persistDocumentDirectoryState({ ...state, filters: [chip('owner', [CURRENT_USER_FILTER])], ordering: 'project' })
    const restored = readDocumentDirectoryState()
    expect(restored.filters.map(filter => [filter.field, filter.values?.map(value => value.value)])).toEqual([['owner', [CURRENT_USER_FILTER]]])
    expect(restored.ordering).toBe('project')
    persistDocumentDirectoryState({ ...state, filters: [] })
    expect(location.search).toBe('')
  })

  it('sorts by project name', () => {
    const sorted = sortDocuments([doc('a', { projectIds: [project.id] }), doc('b')], 'project', false, data, t)
    expect(sorted.map(item => item.id)).toEqual(['b', 'a'])
  })
})
