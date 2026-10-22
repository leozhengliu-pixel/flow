import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap, teammate, viewer } from '@/test/fixtures'
import type { DocumentPermission, FlowDocument } from '@/types/flow'

import { DocumentAccessDialog, type DocumentAccessChange } from './document-access-dialog'

const data = makeBootstrap({ members: [{ user: viewer, role: 'owner' }, { user: teammate, role: 'member' }] } as never)
const flowDoc = { id: 'doc-1', creator: viewer } as unknown as FlowDocument
const permissions: DocumentPermission[] = [{ subjectType: 'user', subjectId: teammate.id, role: 'viewer' } as DocumentPermission]

function Harness({ onSave, onOpenChange }: { onSave: (changes: DocumentAccessChange[]) => Promise<void>; onOpenChange?: (open: boolean) => void }) {
  const [open, setOpen] = useState(false)
  return <I18nProvider>
    <button data-i18n-ignore onClick={() => setOpen(true)} type="button">Open access</button>
    <DocumentAccessDialog data={data} document={flowDoc} onOpenChange={next => { setOpen(next); onOpenChange?.(next) }} onSave={onSave} open={open} permissions={permissions}/>
  </I18nProvider>
}

const select = (name: string) => screen.getByRole('combobox', { name: new RegExp(`Access for ${name}`) })
async function pick(user: ReturnType<typeof userEvent.setup>, name: string, option: string) {
  await user.click(select(name))
  await user.click(screen.getByRole('option', { name: option }))
}

beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

describe('DocumentAccessDialog', () => {
  it('stages role changes, disables Save until something changed and applies them in one call', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockResolvedValue(undefined)
    const onOpenChange = vi.fn()
    render(<Harness onOpenChange={onOpenChange} onSave={onSave}/>)
    await user.click(screen.getByRole('button', { name: 'Open access' }))
    const dialog = screen.getByRole('dialog')
    const save = within(dialog).getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    await pick(user, 'Teammate', 'Can edit')
    await pick(user, 'Everyone in workspace', 'Can view')
    expect(onSave).not.toHaveBeenCalled()
    expect(within(dialog).getAllByRole('img', { name: 'Unsaved change' })).toHaveLength(2)
    expect(save).toBeEnabled()
    await user.click(save)
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave.mock.calls[0][0]).toEqual([
      { subjectType: 'workspace', subjectId: data.workspace.id, role: 'viewer' },
      { subjectType: 'user', subjectId: teammate.id, role: 'editor' },
    ])
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('drops a staged change that returns to the saved role', async () => {
    const user = userEvent.setup()
    render(<Harness onSave={vi.fn()}/>)
    await user.click(screen.getByRole('button', { name: 'Open access' }))
    await pick(user, 'Teammate', 'Can edit')
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
    await pick(user, 'Teammate', 'Can view')
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.queryByRole('img', { name: 'Unsaved change' })).toBeNull()
  })

  it('Cancel, Esc and the close button discard staged changes without saving', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn()
    render(<Harness onSave={onSave}/>)
    for (const close of ['cancel', 'escape', 'x'] as const) {
      await user.click(screen.getByRole('button', { name: 'Open access' }))
      await pick(user, 'Teammate', 'Can edit')
      if (close === 'cancel') await user.click(screen.getByRole('button', { name: 'Cancel' }))
      else if (close === 'escape') await user.keyboard('{Escape}')
      else await user.click(screen.getByRole('button', { name: 'Close' }))
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      await user.click(screen.getByRole('button', { name: 'Open access' }))
      expect(select('Teammate')).toHaveTextContent('Can view')
      expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
      await user.keyboard('{Escape}')
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    }
    expect(onSave).not.toHaveBeenCalled()
  })

  it('keeps the dialog open with the staged choices and flags the failed rows when saving fails', async () => {
    const user = userEvent.setup()
    const onSave = vi.fn().mockRejectedValueOnce(new Error('forbidden')).mockResolvedValueOnce(undefined)
    render(<Harness onSave={onSave}/>)
    await user.click(screen.getByRole('button', { name: 'Open access' }))
    await pick(user, 'Teammate', 'Can edit')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not update document access: forbidden')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(select('Teammate')).toHaveTextContent('Can edit')
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled()
    expect(screen.getByRole('img', { name: 'Unsaved change' }).closest('.document-access-row')).toHaveAttribute('data-error')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(onSave).toHaveBeenCalledTimes(2)
  })

  it('locks the controls and refuses to close while a save is in flight', async () => {
    const user = userEvent.setup()
    let finish: () => void = () => undefined
    const onSave = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    render(<Harness onSave={onSave}/>)
    await user.click(screen.getByRole('button', { name: 'Open access' }))
    await pick(user, 'Teammate', 'Can edit')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(select('Teammate')).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    finish()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('focuses the first enabled control on open, tabs every select, Cancel, Close and returns focus on close', async () => {
    const user = userEvent.setup()
    render(<Harness onSave={vi.fn()}/>)
    const opener = screen.getByRole('button', { name: 'Open access' })
    await user.click(opener)
    await waitFor(() => expect(document.activeElement).toBe(select('Everyone in workspace')))
    const order: string[] = []
    for (let i = 0; i < 5; i++) {
      await user.tab()
      const active = document.activeElement as HTMLElement
      order.push(active.getAttribute('aria-label') ?? active.textContent ?? '')
    }
    // The creator's own select is disabled and the primary button is disabled until something changes.
    expect(order).toEqual(['Access for Test team', 'Access for Teammate', 'Cancel', 'Close', 'Access for Everyone in workspace'])
    await user.keyboard('{Escape}')
    await waitFor(() => expect(document.activeElement).toBe(opener))
  })

  it('shows Save when something changed in the tab order and zh copy', async () => {
    localStorage.setItem('flow:locale', 'zh-CN')
    const user = userEvent.setup()
    render(<Harness onSave={vi.fn()}/>)
    await user.click(screen.getByRole('button', { name: 'Open access' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: '取消' })).toBeEnabled()
  })
})
