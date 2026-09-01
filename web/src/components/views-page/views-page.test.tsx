import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

import { ViewsPage } from './views-page'

function renderViews(dashboards: boolean) {
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
    resource="issues"
    resourceHref={resource => `/workspace/views/${resource}`}
    scope={{ kind: 'workspace' }}
    viewHref={view => `/workspace/view/${view.id}`}
    views={[]}
  /></I18nProvider>)
}

describe('ViewsPage directory tabs', () => {
  it('uses the shared Issues, Projects, and Dashboards navigation when enabled', () => {
    renderViews(true)
    expect(screen.getByRole('link', { name: 'Issues' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Projects' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Dashboards' })).toHaveAttribute('href', '/workspace/dashboards')
  })

  it('hides dashboards when the workspace feature is disabled', () => {
    renderViews(false)
    expect(screen.queryByRole('link', { name: 'Dashboards' })).not.toBeInTheDocument()
  })
})
