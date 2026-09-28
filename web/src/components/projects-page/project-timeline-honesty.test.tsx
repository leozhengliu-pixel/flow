import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { ProjectsDataView, type ProjectDataGroup, type ProjectPageItem } from './projects-data-view'

const dated = {
  id: 'p1',
  name: 'Dated',
  health: 'on-track',
  priority: 'none',
  issueCount: 0,
  progress: 0,
  status: 'In Progress',
  rawStartDate: '2026-09-01',
  rawTargetDate: '2026-09-20',
} as ProjectPageItem

const undated = {
  id: 'p2',
  name: 'Undated',
  health: 'on-track',
  priority: 'none',
  issueCount: 0,
  progress: 0,
  status: 'In Progress',
} as ProjectPageItem

const groups: ProjectDataGroup[] = [{ id: 'g1', name: 'In Progress', projects: [dated, undated] }]

describe('LS-0609 Timeline honesty', () => {
  it('renders real bars and Add dates empty instead of fake undated bars', () => {
    render(
      <I18nProvider>
        <ProjectsDataView groups={groups} layout="timeline" onOpenProject={vi.fn()} />
      </I18nProvider>,
    )
    expect(screen.getByRole('button', { name: 'Center timeline on today' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Dated timeline bar' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Add dates for Undated' })).toBeTruthy()
    expect(screen.getByText('Add dates')).toBeTruthy()
  })
})
