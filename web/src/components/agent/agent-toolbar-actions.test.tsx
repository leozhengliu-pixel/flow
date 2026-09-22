import { describe, expect, it, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { I18nProvider } from '@/i18n/i18n'
import { AgentToolbarActions, hasOpenToolbarSessions, listOpenToolbarSessions } from './agent-toolbar-actions'

describe('LS-0043/0044 AgentToolbarActions', () => {
  it('lists open toolbar sessions', () => {
    const sessions = [
      { id: '1', location: 'toolbar' },
      { id: '2', location: 'page' },
      { id: '3', location: 'toolbar' },
    ]
    const closed = new Set(['3'])
    expect(listOpenToolbarSessions(sessions, closed).map(item => item.id)).toEqual(['1'])
    expect(hasOpenToolbarSessions(sessions, closed)).toBe(true)
  })

  it('renders session switcher and feedback', () => {
    const onSelect = vi.fn()
    const onFeedback = vi.fn()
    render(
      <I18nProvider>
        <AgentToolbarActions
          activeSessionId="a"
          onFeedback={onFeedback}
          onSelectSession={onSelect}
          sessions={[
            { id: 'a', title: 'First' },
            { id: 'b', title: 'Second' },
          ]}
        />
      </I18nProvider>,
    )
    fireEvent.change(screen.getByLabelText('Sessions'), { target: { value: 'b' } })
    expect(onSelect).toHaveBeenCalledWith('b')
    fireEvent.click(screen.getByLabelText('Helpful'))
    expect(onFeedback).toHaveBeenCalledWith('up')
  })
})
