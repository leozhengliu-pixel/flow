import { useContext, useEffect, useMemo, useRef, useState } from 'react'
import { ApplicationStoreContext } from '@/store/application-store-contexts'
import { fetchSyncManifest, listProjectRecords } from '@/lib/api'
import { ApiError } from '@/lib/api-client'
import { cacheStringBytes } from '@/lib/scale-p08'
import { releaseScaleP19Bytes, retainScaleP19Bytes } from '@/lib/scale-p19-budget'
import { scaleP19ManifestMatches, scaleP19ProjectsEnabled } from '@/lib/scale-p19-surfaces'
import { syncClientFor } from '@/lib/sync-client'
import type { Project } from '@/types/flow'
import type { ProjectDependencyOption, ProjectDependencyValue } from './project-dependency-picker'

const EMPTY_OPTIONS: ProjectDependencyOption[] = []

export function scaleP19DependencyOption(project: Project, viewerId?: string): ProjectDependencyOption {
  return {
    id: project.id, label: project.name, color: project.color, icon: project.icon,
    group: viewerId && (project.lead?.id === viewerId || project.memberIds?.includes(viewerId)) ? 'your' : 'other',
    keywords: `${project.slugId} ${project.summary}`,
    previewData: { summary: project.summary, status: project.status.name, lead: project.lead?.displayName, priority: project.priorityLabel, targetDate: project.targetDate, progress: Math.round(project.progress * 100), issueCount: project.issueCount },
  }
}

export function useScaleP19DependencyOptions(projects: ProjectDependencyOption[], value: ProjectDependencyValue[]) {
  const context = useContext(ApplicationStoreContext)
  const client = syncClientFor(context?.data)
  const enabled = Boolean(client) && scaleP19ProjectsEnabled(context?.data)
  const owner = useRef({})
  const [state, setState] = useState<{ client: typeof client; items: ProjectDependencyOption[] }>({ client, items: [] })
  const retained = state.client === client && !client?.disposed ? state.items : EMPTY_OPTIONS
  const options = useMemo(() => [...projects, ...retained.filter(item => value.some(selected => selected.projectId === item.id) && !projects.some(project => project.id === item.id))], [projects, retained, value])
  const key = JSON.stringify([value.map(item => item.projectId), projects.map(item => item.id)])
  const retain = (project: ProjectDependencyOption) => {
    if (!enabled) return
    const items = [...retained.filter(item => item.id !== project.id && value.some(selected => selected.projectId === item.id)), { id: project.id, label: project.label, icon: project.icon, color: project.color, group: project.group }]
    if (!retainScaleP19Bytes(owner.current, cacheStringBytes(JSON.stringify(items)))) { client?.requestReset(true); return }
    setState({ client, items })
  }
  useEffect(() => {
    if (!enabled || !client) return
    const controller = new AbortController(), signal = client.requestSignal(controller.signal)
    const ids = (JSON.parse(key)[0] as string[]).filter(id => !options.some(item => item.id === id))
    const load = async () => {
      const items = [...retained]
      for (let offset = 0; offset < ids.length; offset += 100) {
        const page = await listProjectRecords({ archived: 'all', limit: 100, filter: [{ field: 'project', operator: 'is', values: ids.slice(offset, offset + 100) }] }, signal, client.manifest.core.workspace.urlKey)
        if (page.hasMore) throw new ApiError('Dependency selection exceeds its window', 501)
        for (const project of page.items) {
          const option = scaleP19DependencyOption(project, client.manifest.viewerId)
          items.push({ id: option.id, label: option.label, color: option.color, icon: option.icon, group: option.group })
        }
        if (!retainScaleP19Bytes(owner.current, cacheStringBytes(JSON.stringify(items)))) throw new ApiError('Dependency selection exceeds its byte budget', 501)
      }
      const manifest = await fetchSyncManifest(client.manifest.core.workspace.urlKey, signal)
      if (!scaleP19ManifestMatches(client.manifest, manifest)) { client.requestReset(); return }
      if (!manifest.capabilities.includes('sync-v2')) { client.requestReset(true); return }
      if (!signal.aborted) setState({ client, items })
    }
    if (ids.length) void load().catch(error => { if (!signal.aborted) client.requestReset(!(error instanceof ApiError && [401, 403].includes(error.status))) })
    return () => controller.abort()
  }, [client, enabled, key, options, retained])
  useEffect(() => {
    const token = owner.current
    if (enabled && !retainScaleP19Bytes(token, cacheStringBytes(JSON.stringify(retained)))) client?.requestReset(true)
    return () => releaseScaleP19Bytes(token)
  }, [client, enabled, retained])
  return { enabled, options, retain, viewerId: context?.data?.viewer.id }
}
