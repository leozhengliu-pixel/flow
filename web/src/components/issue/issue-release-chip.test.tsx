import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { IssueReleaseChip } from './issue-release-chip'

const wrap = (node: React.ReactNode) => render(<I18nProvider>{node}</I18nProvider>)

it('shows one release as "Pipeline · Release" with its stage icon (Linear)', () => {
  localStorage.setItem('flow:locale', 'en-US')
  wrap(<IssueReleaseChip releases={[{ id: 'r1', name: 'v1', pipelineName: 'Web app', status: 'inProgress' }]}/>)
  expect(screen.getByText('Web app · v1')).toBeVisible()
  expect(document.querySelector('[data-icon="release-status"]')).toHaveAttribute('data-status', 'inProgress')
})

it('collapses several releases to a count behind the release glyph', () => {
  localStorage.setItem('flow:locale', 'en-US')
  wrap(<IssueReleaseChip releases={[{ id: 'r1', name: 'v1', status: 'planned' }, { id: 'r2', name: 'v2', status: 'released' }]}/>)
  expect(screen.getByText('2 releases')).toBeVisible()
  expect(document.querySelector('[data-icon="releases"]')).not.toBeNull()
})

it('renders nothing without releases', () => {
  const { container } = wrap(<IssueReleaseChip releases={[]}/>)
  expect(container).toBeEmptyDOMElement()
})
