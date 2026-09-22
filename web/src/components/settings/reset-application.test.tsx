import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { ClientStorage } from '@/lib/client-storage'
import { clearViewPreferences } from '@/lib/view-preferences'
import {
  ResetApplication,
  ResetApplicationControl,
  deleteAllDatabases,
} from './reset-application'

describe('ResetApplication (LS-0530)', () => {
  beforeEach(() => {
    ClientStorage.set('flow:draft', { body: 'x' }, 'local')
    ClientStorage.setSession('tmp', 1)
  })

  it('deleteAllDatabases clears local client prefs', async () => {
    await deleteAllDatabases()
    expect(ClientStorage.get('flow:draft')).toBeUndefined()
    expect(ClientStorage.getSession('tmp')).toBeUndefined()
    clearViewPreferences()
  })

  it('auto-runs and calls onComplete', async () => {
    const onComplete = vi.fn()
    render(<ResetApplication onComplete={onComplete} />)
    await waitFor(() => expect(onComplete).toHaveBeenCalled())
  })

  it('control confirms before navigating', async () => {
    const user = userEvent.setup()
    const onNavigate = vi.fn()
    render(<ResetApplicationControl onNavigate={onNavigate} />)
    await user.click(screen.getByRole('button', { name: 'Reset application' }))
    expect(screen.getByText(/clears local drafts/i)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Reset and reload' }))
    await waitFor(() => expect(onNavigate).toHaveBeenCalledWith('/'))
  })
})
