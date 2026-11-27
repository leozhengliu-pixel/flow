import { useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import { ApplicationStoreContext, EntityStoreContext } from '@/store/application-store-contexts'
import { queryWindowManifestMatches, projectWindowsEnabled } from '@/lib/query-window-surfaces'
import { ProjectQueryWindow, type ProjectWindowPage, type ProjectWindowQuery } from '@/lib/project-query-window'
import { ApiError } from '@/lib/api-client'
import { fetchSyncBatch, fetchSyncManifest, listProjectRecords, type ProjectQueryPage } from '@/lib/api'
import { compareSyncVersions, syncClientFor, SyncResetRequired } from '@/lib/sync-client'
import type { Project, SyncRecord } from '@/types/flow'
import type { ProjectFilterField, ProjectFilterOption } from './projects-filter-model'
import { releaseQueryWindowBytes, retainQueryWindowBytes, retainQueryWindowCore } from '@/lib/query-window-budget'
import { cacheStringBytes } from '@/lib/realtime-cache-limits'
import { projectWindowSummary } from '@/lib/project-window-summary'
import { readQueryWindow, QUERY_WINDOWS_CAPABILITY, type ProjectServerWindowPage } from '@/lib/query-window-api'

const recordsForPage = Symbol('query-window-records')
type VersionedPage = ProjectQueryPage & { [recordsForPage]?: SyncRecord[] }

export function useProjectQueryWindow(workspace: string, query: ProjectWindowQuery, active = true) {
  const context = useContext(ApplicationStoreContext)
  const entityStore = useContext(EntityStoreContext)
  const client = syncClientFor(context?.data)
  const enabled = active && Boolean(client) && projectWindowsEnabled(context?.data)
  const key = JSON.stringify(query)
  const stableQuery = useMemo<ProjectWindowQuery>(() => JSON.parse(key), [key])
  const window = useMemo(() => {
    const retained = new Map<string, { count: number; release: () => void }>()
    return new ProjectQueryWindow(workspace, async (query, signal, workspace) => {
      if (query.window) {
        if (!client?.manifest.capabilities.includes(QUERY_WINDOWS_CAPABILITY)) throw new ApiError('Query windows unavailable', 501)
        const page = await readQueryWindow<ProjectServerWindowPage>(workspace, 'projects', query.window, query.cursor, client.requestSignal(signal))
        if (page.authzEpoch !== client.manifest.authzEpoch) { client.requestReset(); throw new DOMException('Authorization changed', 'AbortError') }
        const manifest = await fetchSyncManifest(workspace, client.requestSignal(signal))
        if (!queryWindowManifestMatches(client.manifest, manifest)) { client.requestReset(); throw new DOMException('Authorization changed', 'AbortError') }
        const result: VersionedPage = { ...page, items: page.items.map(project => {
          const current = client.get({ kind: 'project', id: project.id })
          if (current?.completeness === 'deleted' && compareSyncVersions(current.version, page.versions[project.id]) >= 0) throw new ApiError('Project window changed while loading', 409)
          return projectWindowSummary(current?.value && compareSyncVersions(current.version, page.versions[project.id]) > 0 ? current.value as unknown as Project : project)
        }) }
        Object.defineProperty(result, recordsForPage, { value: page.items.map(value => ({ key: { kind: 'project', id: value.id }, version: page.versions[value.id], completeness: 'summary', value: projectWindowSummary(value) })) })
        return result
      }
      const page = await listProjectRecords(query, client?.requestSignal(signal) ?? signal, workspace)
      if (!client) return page
      const records: SyncRecord[] = []
      for (let offset = 0; offset < page.items.length; offset += 50) {
        const batch = await fetchSyncBatch(workspace, page.items.slice(offset, offset + 50).map(item => ({ kind: 'project', id: item.id })), 'summary', client.requestSignal(signal))
        if (batch.authzEpoch !== client.manifest.authzEpoch) { client.requestReset(); throw new DOMException('Authorization changed', 'AbortError') }
        for (const item of batch.items) {
          // The summary projection marks omitted detail while retaining the row value.
          if (item.key.kind !== 'project' || !page.items.some(project => project.id === item.key.id) || !item.value || typeof item.value.name !== 'string' || !Array.isArray(item.value.teamIds) || !item.value.status) throw new ApiError('Project summary unavailable', 501)
          records.push({ ...item, completeness: 'summary' })
        }
      }
      const manifest = await fetchSyncManifest(workspace, client.requestSignal(signal), undefined, 'projects-page')
      if (!queryWindowManifestMatches(client.manifest, manifest)) { client.requestReset(); throw new DOMException('Authorization changed', 'AbortError') }
      if (!manifest.capabilities.includes('sync-v2')) { client.requestReset(true); throw new DOMException('Sync unavailable', 'AbortError') }
      const byId = new Map(records.map(record => [record.key.id, record]))
      const result: VersionedPage = { ...page, items: page.items.map(item => {
        const record = byId.get(item.id), current = client.get({ kind: 'project', id: item.id })
        if (!record || current?.completeness === 'deleted') throw new ApiError('Project window changed while loading', 409)
        const value = current?.value && compareSyncVersions(current.version, record.version) > 0 ? current.value : record.value!
        return projectWindowSummary(value as unknown as Project)
      }) }
      Object.defineProperty(result, recordsForPage, { value: records })
      return result
    }, undefined, {
      commit(page) {
        if (!client) return
        for (const project of page.items) {
          const previous = retained.get(project.id)
          if (previous) previous.count++
          else retained.set(project.id, { count: 1, release: client.retainDetail({ kind: 'project', id: project.id }) })
        }
        client.install((page as VersionedPage)[recordsForPage] ?? [])
      },
      evict(page) {
        const keys = []
        for (const project of page.items) {
          const previous = retained.get(project.id)
          if (!previous || --previous.count > 0) continue
          previous.release(); retained.delete(project.id)
          keys.push({ kind: 'project', id: project.id })
        }
        client?.releaseSummaries(keys)
      },
    })
  }, [workspace, client])
  const [state, setState] = useState<{ owner?: ProjectQueryWindow; key?: string; page?: ProjectWindowPage; loading: boolean; error?: string }>({ loading: enabled })
  const mounted = useRef(false)
  const request = useRef<AbortController | undefined>(undefined)
  const position = useRef(0)
  const initialized = useRef<{ window: ProjectQueryWindow; key: string } | undefined>(undefined)
  const load = useCallback(async function readPage(index: number, retried = false): Promise<void> {
    if (client && !retainQueryWindowCore(client, client.manifest.core)) { client.requestReset(true); return }
    request.current?.abort()
    window.cancel()
    const operation = request.current = new AbortController()
    setState(current => ({ owner: window, key, page: current.owner === window && current.key === key ? current.page : undefined, loading: true }))
    try {
      if (client) {
        const manifest = await fetchSyncManifest(workspace, client.requestSignal(operation.signal))
        if (!queryWindowManifestMatches(client.manifest, manifest)) { client.requestReset(); return }
      }
      if (operation.signal.aborted || !mounted.current) return
      const page = await window.read(stableQuery, index)
      if (!operation.signal.aborted && mounted.current && !client?.disposed) { position.current = index; setState({ owner: window, key, page, loading: false }) }
    } catch (error) {
      if (operation.signal.aborted || !mounted.current || error instanceof DOMException && error.name === 'AbortError') return
      if (error instanceof SyncResetRequired) { client?.requestReset(true); return }
      if (error instanceof ApiError && error.status === 409 && !retried) { window.invalidate(); position.current = 0; await readPage(0, true); return }
      if (error instanceof ApiError && [400, 404, 405, 501].includes(error.status)) { client?.requestReset(true); return }
      if (error instanceof ApiError && [401, 403, 409].includes(error.status)) { client?.requestReset(); return }
      setState({ owner: window, key, loading: false, error: error instanceof Error ? error.message : 'Could not load projects' })
    }
  }, [client, key, stableQuery, window, workspace])
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; request.current?.abort(); window.invalidate(); window.select([], []) }
  }, [window])
  useEffect(() => {
    if (!enabled) return
    if (initialized.current?.window === window && initialized.current.key === key) window.refresh()
    else { window.invalidate(); position.current = 0 }
    initialized.current = { window, key }
    void load(position.current)
  }, [enabled, key, load, window, context?.data?.sync?.head])
  const favoriteIds = JSON.stringify(context?.data?.favorites?.filter(item => item.resourceType === 'project').map(item => item.resourceId) ?? [])
  useEffect(() => {
    if (!enabled || !client) return
    const releases = (JSON.parse(favoriteIds) as string[]).map(id => client.retainDetail({ kind: 'project', id }))
    return () => releases.forEach(release => release())
  }, [client, enabled, favoriteIds])
  const current = state.owner === window && state.key === key && !client?.disposed
  const metadata = current ? state.page as (ProjectWindowPage & Partial<ProjectServerWindowPage>) | undefined : undefined
  const page = useMemo(() => current && state.page ? { ...state.page, items: state.page.items.flatMap(project => {
    const record = client?.get({ kind: 'project', id: project.id })
    if (record?.completeness === 'deleted') return []
    const value = entityStore?.getById('project', project.id) as Project | undefined
    return [projectWindowSummary(value ?? (record?.value as unknown as Project | undefined) ?? project)]
  }) } : undefined, [client, current, entityStore, state.page])
  const selectedProjects = client?.disposed ? [] : window.selectedProjects.flatMap(project => {
    const record = client?.get({ kind: 'project', id: project.id })
    if (record?.completeness === 'deleted') return []
    const value = entityStore?.getById('project', project.id) as Project | undefined
    const name = value?.name ?? record?.value?.name
    return [{ id: project.id, name: typeof name === 'string' ? name : project.name }]
  })
  return { client, enabled, page, metadata, selectedProjects, error: current ? state.error : undefined, loading: enabled && (!current || state.loading), window, load, legacy: () => client?.requestReset(true), select: (ids: string[], projects: Project[]) => window.select(ids.filter(id => client?.get({ kind: 'project', id })?.completeness !== 'deleted'), [...selectedProjects, ...projects]) }
}

export function useProjectWindowFilterCounts(enabled: boolean, workspace: string, query: ProjectWindowQuery, options: Partial<Record<ProjectFilterField, ProjectFilterOption[]>>, active = true, serverCounts?: Record<string, Record<string, number>>) {
  const context = useContext(ApplicationStoreContext)
  const client = syncClientFor(context?.data)
  const definitions = Object.entries(options).flatMap(([field, values]) => field === 'project' ? [] : values!.map(value => ({ field, id: value.id })))
  const key = JSON.stringify([workspace, query, definitions, context?.data?.sync?.authzEpoch, context?.data?.sync?.head])
  const [state, setState] = useState<{ key: string; counts: Map<string, number> }>()
  const owner = useRef({})
  useEffect(() => {
    const token = owner.current
    if (enabled && state && !retainQueryWindowBytes(token, cacheStringBytes(JSON.stringify([...state.counts])) + state.counts.size * 64)) client?.requestReset(true)
    return () => releaseQueryWindowBytes(token)
  }, [client, enabled, state])
  useEffect(() => {
    if (!enabled || !active || serverCounts || state?.key === key) return
    const [scope, query, definitions] = JSON.parse(key) as [string, ProjectWindowQuery, Array<{ field: string; id: string }>]
    const controller = new AbortController()
    const signal = client?.requestSignal(controller.signal) ?? controller.signal
    let index = 0
    const counts = new Map<string, number>()
    const run = async () => {
      while (!signal.aborted && index < definitions.length) {
        const { field, id } = definitions[index++]
        const page = await listProjectRecords({ ...query, limit: 1, includeTotal: true, filter: [...query.filter ?? [], { field, operator: 'is', values: [id] }] }, signal, scope)
        if (!Number.isSafeInteger(page.total) || page.total < 0) throw new ApiError('Project counts unavailable', 501)
        counts.set(JSON.stringify([field, id]), page.total)
      }
    }
    void Promise.all(Array.from({ length: Math.min(4, definitions.length) }, run)).then(async () => {
      if (signal.aborted) return
      if (client) {
        const manifest = await fetchSyncManifest(scope, signal, undefined, 'projects-page')
        if (!queryWindowManifestMatches(client.manifest, manifest)) { client.requestReset(); return }
        if (!manifest.capabilities.includes('sync-v2')) { client.requestReset(true); return }
      }
      if (!signal.aborted) setState({ key, counts })
    }).catch(error => {
      if (signal.aborted) return
      if (error instanceof ApiError && [401, 403].includes(error.status)) client?.requestReset()
      else client?.requestReset(true)
    })
    return () => controller.abort()
  }, [active, client, enabled, key, state?.key, serverCounts])
  if (!enabled) return options
  return Object.fromEntries(Object.entries(options).map(([field, values]) => [field, values!.map(option => ({ ...option, count: field === 'project' ? 1 : serverCounts ? serverCounts[field]?.[option.id] ?? 0 : state?.key === key ? state.counts.get(JSON.stringify([field, option.id])) : undefined }))])) as typeof options
}
