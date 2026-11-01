import { render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

import { ViewsPage } from './views-page'
const layout=vi.hoisted(()=>({desktop:false}))
vi.mock('@/hooks/use-desktop-layout',()=>({useDesktopLayout:()=>layout.desktop}))

function renderViews(dashboards: boolean, resource: 'issues' | 'projects' = 'issues') {
  const data = makeBootstrap({ workspaceSettings: { featureFlags: { dashboards } } as unknown as BootstrapData['workspaceSettings'] })
  return render(<I18nProvider><ViewsPage
    dashboardsHref="/workspace/dashboards"
    data={data}
    onCreate={vi.fn()}
    onDelete={vi.fn()}
    onDuplicate={vi.fn()}
    onEdit={vi.fn()}
    onOpen={vi.fn()}
    onOpenDashboards={vi.fn()}
    onResourceChange={vi.fn()}
    onSetSubscriptionEvents={vi.fn()}
    onToggleFavorite={vi.fn()}
    onUpdate={vi.fn()}
    resource={resource}
    resourceHref={resource => `/workspace/views/${resource}`}
    scope={{ kind: 'workspace' }}
    viewHref={view => `/workspace/view/${view.id}`}
    views={[]}
  /></I18nProvider>)
}

describe('ViewsPage directory tabs', () => {
  beforeEach(()=>{layout.desktop=false})
  it('keeps the desktop view directory to Issues and Projects', () => {
    layout.desktop=true
    renderViews(true)
    expect(screen.getByRole('link',{name:'Issues'})).toBeVisible()
    expect(screen.getByRole('link',{name:'Projects'})).toBeVisible()
    expect(screen.queryByRole('link',{name:'Dashboards'})).toBeNull()
  })
  it('shows Issues, Projects and the enterprise Dashboards tab, with no find field', () => {
    renderViews(true)
    expect(screen.getByRole('link', { name: 'Issues' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Projects' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Dashboards' })).toHaveAttribute('href', '/workspace/dashboards')
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  })

  it('hides the Dashboards tab when the workspace feature is disabled', () => {
    renderViews(false)
    expect(screen.getByRole('link', { name: 'Projects' })).toBeVisible()
    expect(screen.queryByRole('link', { name: 'Dashboards' })).not.toBeInTheDocument()
  })

  it('labels the empty state for the selected resource', () => {
    renderViews(false, 'projects')
    expect(screen.getByText('Projects', { selector: 'span' })).toBeVisible()
  })
})
