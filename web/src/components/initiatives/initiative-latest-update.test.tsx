import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative, InitiativeUpdate, User } from '@/types/flow'
import { InitiativeLatestUpdate } from './initiative-latest-update'

describe('InitiativeLatestUpdate', () => {
  it('renders persisted update data and opens activity from the comment control', () => {
    const onOpen = vi.fn()
    const user = { id: 'viewer', name: 'Reviewer', displayName: 'Reviewer' } as User
    const update: InitiativeUpdate = { id: 'update', initiativeId: 'initiative', body: 'Delivery is on schedule.', health: 'onTrack', user, createdAt: new Date().toISOString(), comments: [], reactions: {}, attachments: [] }
    render(<I18nProvider><InitiativeLatestUpdate initiative={{ id: 'initiative' } as Initiative} update={update} viewer={user} onOpen={onOpen} onReload={vi.fn()}/></I18nProvider>)
    expect(screen.getByText('Delivery is on schedule.')).toBeInTheDocument()
    expect(screen.getByText('On track')).toBeInTheDocument()
    expect(screen.getByText('Reviewer')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Comments' }))
    expect(onOpen).toHaveBeenCalledOnce()
  })
})
