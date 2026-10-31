import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { AgentChatMenu } from './agent-chat-actions'

vi.mock('@/hooks/use-phone-layout', () => ({ usePhoneLayout: () => true }))

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})
afterEach(() => vi.unstubAllGlobals())

describe('phone chat actions', () => {
  it('keeps feedback reachable and closes the menu when choosing a filtered action', async () => {
    const user = userEvent.setup()
    const onFeedback = vi.fn(), onCopy = vi.fn()
    render(<I18nProvider><AgentChatMenu onCopy={onCopy} onDelete={vi.fn()} onFeedback={onFeedback} onRename={vi.fn()}/></I18nProvider>)
    await user.click(screen.getByRole('button', { name: 'Chat options' }))
    await user.click(screen.getByRole('option', { name: 'Leave feedback…' }))
    expect(onFeedback).toHaveBeenCalledOnce()
    expect(screen.queryByRole('dialog', { name: 'Chat options' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Chat options' }))
    await user.type(screen.getByRole('combobox'), 'Copy')
    expect(screen.getAllByRole('option')).toHaveLength(1)
    await user.click(screen.getByRole('option', { name: 'Copy as markdown' }))
    expect(onCopy).toHaveBeenCalledOnce()
    expect(screen.queryByRole('dialog', { name: 'Chat options' })).not.toBeInTheDocument()
  })
})
