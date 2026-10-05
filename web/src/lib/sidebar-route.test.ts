import { describe, expect, it } from 'vitest'

import { sidebarRoutePath } from './sidebar-route'

describe('sidebarRoutePath', () => {
  it('highlights Views on the Dashboards tab of the Views page', () => {
    expect(sidebarRoutePath({ kind: 'dashboards', workspaceSlug: 'acme' })).toBe(sidebarRoutePath({ kind: 'workspace-views', workspaceSlug: 'acme' } as Parameters<typeof sidebarRoutePath>[0]))
    expect(sidebarRoutePath({ kind: 'dashboards', workspaceSlug: 'acme', dashboardId: 'd1' })).toBe('/acme/views/issues')
    expect(sidebarRoutePath({ kind: 'dashboards', workspaceSlug: 'acme', teamKey: 'ENG' })).toBe(sidebarRoutePath({ kind: 'team-views', workspaceSlug: 'acme', teamKey: 'ENG' } as Parameters<typeof sidebarRoutePath>[0]))
  })
})
