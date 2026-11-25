import { act, renderHook } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { mkdirSync, writeFileSync } from 'node:fs'
import { useWorkspaceRealtime } from '@/hooks/use-workspace-realtime'
import { clearNavigationCache, writeNavigationCache } from '@/lib/navigation-cache'
import { mergeIssueRecords } from '@/lib/issue-detail-cache'
import { makeBootstrap, makeIssue, project, viewer } from '@/test/fixtures'

vi.mock('@/lib/api', () => ({ realtimeClientId: () => 'self', updatePresence: async () => [] }))

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.unstubAllEnvs() })

it.each(['off', 'on'])('scale-activate-b1 %s preserves apply completion and openable cached records', async mode => {
  const enabled = mode === 'on'
  vi.useFakeTimers()
  vi.stubEnv('VITE_APPLIED_REALTIME_CURSOR', String(enabled))
  vi.stubEnv('VITE_BOUNDED_BROWSER_CACHE', String(enabled))
  localStorage.clear()
  clearNavigationCache()
  class Source {
    static all: Source[] = []
    constructor() { Source.all.push(this) }
    onmessage?: (event: { data: string; lastEventId: string }) => void
    close() {}
  }
  vi.stubGlobal('EventSource', Source)
  vi.stubGlobal('BroadcastChannel', undefined)
  Object.defineProperty(navigator, 'sendBeacon', { value: () => true, configurable: true })
  let applied = false
  const hook = renderHook(() => useWorkspaceRealtime({
    workspaceKey: 'workspace', viewerId: viewer.id, route: '/',
    onRemoteSync: async () => { await new Promise(resolve => setTimeout(resolve, 5000)); applied = true },
  }))
  Source.all[0].onmessage?.({ data: JSON.stringify({ id: 'evt_b1', type: 'issue.updated', createdAt: '2026-11-12' }), lastEventId: 'evt_b1' })
  const checkpoints = () => Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)!)
    .filter(key => key.startsWith('flow:realtime:')).map(key => localStorage.getItem(key)).filter(raw => raw?.includes('evt_b1')).length
  await act(async () => { await vi.advanceTimersByTimeAsync(400) })
  const beforeApply = checkpoints()
  expect(applied).toBe(false)
  expect(beforeApply).toBe(enabled ? 0 : 1)
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  expect(applied).toBe(true)
  expect(checkpoints()).toBe(1)
  hook.unmount()
  for (let i = 0; i < 20; i++) {
    const workspace = { ...makeBootstrap().workspace, id: `w-${i}`, urlKey: `w-${i}` }
    writeNavigationCache(viewer.id, workspace.urlKey, makeBootstrap({ workspace, projects: [{ ...project, description: 'x'.repeat(200 * 1024) }] }))
  }
  const navigationBytes = Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)!)
    .filter(key => key.startsWith('flow:navigation:')).reduce((total, key) => total + new TextEncoder().encode(localStorage.getItem(key)!).length, 0)
  const input = Array.from({ length: 100 }, (_, i) => makeIssue({ id: `i-${i}`, description: 'x'.repeat(512 * 1024), isSummary: false }))
  const retained = mergeIssueRecords([], input)
  const detailBytes = retained.filter(issue => !issue.isSummary).reduce((total, issue) => total + new TextEncoder().encode(JSON.stringify(issue)).length, 0)
  expect(input.every(issue => !issue.isSummary)).toBe(true)
  expect(retained).toHaveLength(input.length)
  if (enabled) {
    expect(navigationBytes).toBeLessThanOrEqual(2 * 1024 * 1024)
    expect(detailBytes).toBeLessThanOrEqual(32 * 1024 * 1024)
  } else {
    expect(retained.every(issue => !issue.isSummary)).toBe(true)
  }
  mkdirSync('/tmp/codex-scale-activate-b1', { recursive: true })
  writeFileSync(`/tmp/codex-scale-activate-b1/browser-cache-${mode}.json`, JSON.stringify({ beforeApply, afterApply: checkpoints(), navigationBytes, detailBytes, retainedDetails: retained.filter(issue => !issue.isSummary).length }, null, 2))
})
