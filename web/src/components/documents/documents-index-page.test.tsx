import { fireEvent, render, screen } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { DocumentsIndexPage } from './documents-index-page'

it('restores document filters from the route and preserves unrelated query parameters', () => {
  const onFiltersChange = vi.fn()
  render(<I18nProvider><DocumentsIndexPage data={makeBootstrap({ documents: [] })} onReload={vi.fn()} search="?q=notes&teamId=team-1&archived=true&sort=updated" onFiltersChange={onFiltersChange}/></I18nProvider>)
  expect(screen.getByRole('textbox', { name: 'Search documents' })).toHaveValue('notes')
  expect(screen.getByRole('checkbox')).toBeChecked()
  fireEvent.change(screen.getByRole('textbox', { name: 'Search documents' }), { target: { value: 'spec' } })
  expect(new URLSearchParams(onFiltersChange.mock.calls[0][0]).get('q')).toBe('spec')
  expect(new URLSearchParams(onFiltersChange.mock.calls[0][0]).get('teamId')).toBe('team-1')
  expect(new URLSearchParams(onFiltersChange.mock.calls[0][0]).get('sort')).toBe('updated')
})
