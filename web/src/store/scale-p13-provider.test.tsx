import { render } from '@testing-library/react'
import { useEffect } from 'react'
import { describe, expect, it } from 'vitest'
import { makeBootstrap, makeIssue } from '@/test/fixtures'
import { WorkspaceStoreProvider } from './application-store-context'
import { useWorkspaceStore } from './use-workspace-store'
import type { WorkspaceStore } from './workspace-store'

describe('scale-p13 application adapter', () => {
  it('keeps unaffected entity indexes when the provider receives a local record update', () => {
    let latest!: WorkspaceStore
    function Probe() {
      const store = useWorkspaceStore()
      useEffect(() => { latest = store }, [store])
      return null
    }
    const data = makeBootstrap()
    const view = render(<WorkspaceStoreProvider data={data} session={null} account={null}><Probe /></WorkspaceStoreProvider>)
    const previous = latest
    view.rerender(<WorkspaceStoreProvider data={{ ...data, issues: [makeIssue({ title: 'Updated' })] }} session={null} account={null}><Probe /></WorkspaceStoreProvider>)
    expect(latest.entities.project).toBe(previous.entities.project)
    expect(latest.entities.issue).not.toBe(previous.entities.issue)
    expect(latest.getById('issue', 'issue-1')).toMatchObject({ title: 'Updated' })
    expect(previous.getById('issue', 'issue-1')).toMatchObject({ title: 'Test issue' })
  })
})
