import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { Initiative, InitiativeUpdate, User } from '@/types/flow'
import { InitiativeLatestUpdate } from './initiative-latest-update'

describe('InitiativeLatestUpdate', () => {
  it.each([{ age: 25 * 60_000, timeLabel: '25m ago' }, { age: 22 * 86_400_000, timeLabel: '22d ago' }])('renders update data and compact age $timeLabel', ({ age, timeLabel }) => {
    const onOpen = vi.fn()
    const user = { id: 'viewer', name: 'Reviewer', displayName: 'Reviewer' } as User
    const update: InitiativeUpdate = { id: 'update', initiativeId: 'initiative', body: 'Delivery is on schedule.', health: 'onTrack', user, createdAt: new Date(Date.now() - age).toISOString(), comments: [], reactions: {}, attachments: [] }
    render(<I18nProvider><InitiativeLatestUpdate initiative={{ id: 'initiative' } as Initiative} update={update} viewer={user} onOpen={onOpen} onReload={vi.fn()}/></I18nProvider>)
    expect(screen.getByText('Delivery is on schedule.')).toBeInTheDocument()
    expect(screen.getByText('On track')).toBeInTheDocument()
    expect(screen.getByText('Reviewer')).toBeInTheDocument()
    expect(screen.getByText(timeLabel)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Comments' })).toHaveClass('ui-pill')
    expect(screen.getByRole('button', { name: 'Comments' }).querySelector('[data-toolbar-glyph="comment"]')).not.toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Comments' }))
    expect(onOpen).toHaveBeenCalledOnce()
  })
})
