import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, viewer } from '@/test/fixtures'
import type { FlowDocument } from '@/types/flow'
import { DocumentOwnerPicker } from './document-owner-picker'

it('builds the owner options on open and shows the current selection', async () => {
  const user = userEvent.setup()
  const data = makeBootstrap()
  const scan = vi.spyOn(data.users, 'filter')
  const ctx = { data, reload: vi.fn(), navigate: vi.fn(), t: (source: string) => source }
  const document = { id: 'document-1', creator: viewer } as FlowDocument
  render(<I18nProvider><DocumentOwnerPicker ctx={ctx} document={document} trigger="Viewer"/></I18nProvider>)
  const trigger = screen.getByRole('button', { name: 'Change owner' })
  expect(trigger).toHaveAttribute('aria-expanded', 'false')
  expect(scan).not.toHaveBeenCalled()

  await user.tab()
  expect(trigger).toHaveFocus()
  await user.keyboard('{Enter}')
  expect(scan).toHaveBeenCalled()
  expect(await screen.findByRole('option', { name: 'Viewer' })).toHaveAttribute('aria-checked', 'true')
  expect(screen.getByRole('option', { name: 'No owner' })).toBeVisible()
  expect(screen.getByRole('option', { name: 'Teammate' })).toBeVisible()
})
