import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData } from '@/types/flow'

import { ViewsPage } from './views-page'

function renderViews(dashboards: boolean, resource: 'issues' | 'projects' = 'issues') {
  const data = makeBootstrap({ workspaceSettings: { featureFlags: { dashboards } } as unknown as BootstrapData['workspaceSettings'] })
  return render(<I18nProvider><ViewsPage
    data={data}
    onCreate={vi.fn()}
    onDelete={vi.fn()}
    onDuplicate={vi.fn()}
    onEdit={vi.fn()}
    onOpen={vi.fn()}
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
  it('shows only the Issues and Projects tabs and no find field, like Linear', () => {
    renderViews(true)
    expect(screen.getByRole('link', { name: 'Issues' })).toBeVisible()
    expect(screen.getByRole('link', { name: 'Projects' })).toBeVisible()
    // Dashboards is its own page (sidebar More menu), not a Views tab.
    expect(screen.queryByRole('link', { name: 'Dashboards' })).not.toBeInTheDocument()
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  })

  it('labels the empty state for the selected resource', () => {
    renderViews(false, 'projects')
    expect(screen.getByText('Projects', { selector: 'span' })).toBeVisible()
  })
})
