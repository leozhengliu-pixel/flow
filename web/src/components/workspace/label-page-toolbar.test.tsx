import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { filterLabelItems, LabelPageToolbar, triageOptionAvailable } from './label-page-toolbar'
import type { BootstrapData, IssueLabel } from '@/types/flow'

vi.mock('@/lib/favorites', () => ({
  findFavorite: () => undefined,
  toggleFavoriteFor: vi.fn(),
}))
vi.mock('@/lib/api', () => ({
  updateWorkspaceLabel: vi.fn(),
  updateTeamLabel: vi.fn(),
}))

const label: IssueLabel = { id: 'l1', name: 'Bug', color: '#f00', resourceType: 'issue' }

function data(triage = true): BootstrapData {
  return {
    viewer: { id: 'u1', name: 'Ada', displayName: 'Ada', email: 'a@b.c', active: true },
    favorites: [],
    teamSettings: { t1: { triageEnabled: triage } },
    teams: [{ id: 't1', name: 'Eng', key: 'ENG', color: '#5e6ad2' }],
  } as unknown as BootstrapData
}

describe('LabelPageToolbar', () => {
  it('renders icon title favorite and options', () => {
    render(
      <I18nProvider>
        <LabelPageToolbar
          data={data()}
          label={label}
          resourceType="issue"
          search=""
          triageOnly={false}
          onSearchChange={() => undefined}
          onTriageOnlyChange={() => undefined}
        />
      </I18nProvider>,
    )
    expect(screen.getByText('Bug')).toBeTruthy()
    expect(screen.getByLabelText('Add to favorites')).toBeTruthy()
    expect(screen.getByLabelText('Label options')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Triage' })).toBeTruthy()
  })

  it('hides triage option for non-issue labels', () => {
    render(
      <I18nProvider>
        <LabelPageToolbar
          data={data()}
          label={{ ...label, resourceType: 'project' }}
          resourceType="project"
          search=""
          triageOnly={false}
          onSearchChange={() => undefined}
          onTriageOnlyChange={() => undefined}
        />
      </I18nProvider>,
    )
    expect(screen.queryByRole('button', { name: 'Triage' })).toBeNull()
  })
})

describe('filterLabelItems / triageOptionAvailable', () => {
  it('filters by search and triage', () => {
    const items = [
      { identifier: 'ENG-1', title: 'Login bug', state: { type: 'backlog' }, team: { id: 't1' } },
      { identifier: 'ENG-2', title: 'Done', state: { type: 'completed' }, team: { id: 't1' }, triagedAt: '2026-01-01' },
    ]
    expect(filterLabelItems(items, { search: 'login', triageOnly: false, resourceType: 'issue' })).toHaveLength(1)
    expect(filterLabelItems(items, {
      search: '',
      triageOnly: true,
      resourceType: 'issue',
      teamSettings: { t1: { triageEnabled: true } } as unknown as BootstrapData['teamSettings'],
    })).toHaveLength(1)
  })

  it('detects triage availability', () => {
    expect(triageOptionAvailable(data(true))).toBe(true)
    expect(triageOptionAvailable(data(false))).toBe(false)
  })
})
