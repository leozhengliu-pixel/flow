import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { makeBootstrap } from '@/test/fixtures'
import { CommandMenu } from './command-menu'
import { orderingCommandLabel, useRegisterDisplayCommands, type DisplayCommand } from './display-commands'

vi.mock('@/lib/api', async importOriginal => ({ ...await importOriginal<typeof import('@/lib/api')>(), searchWorkspace: vi.fn(async () => ({ results: [] })) }))
class TestResizeObserver { observe() {} unobserve() {} disconnect() {} }
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, value: TestResizeObserver })
beforeEach(() => localStorage.setItem('flow:locale', 'en-US'))

function Register({ commands }: { commands: DisplayCommand[] }) {
  useRegisterDisplayCommands(() => commands)
  return null
}

describe('⌘K Display options commands', () => {
  it("lists Linear's customer display entries as breadcrumbs and runs them", async () => {
    const grouping = vi.fn()
    const commands: DisplayCommand[] = [
      { id: 'display-grouping-customer', kind: 'grouping', path: ['Grouping', 'Customer'], value: 'customer', current: false, run: grouping },
      { id: 'display-ordering-customer-count', kind: 'ordering', path: ['View ordering', orderingCommandLabel('Customer count')], value: 'customerCount', current: false, run: vi.fn() },
    ]
    const noop = vi.fn()
    render(<MemoryRouter><I18nProvider><Register commands={commands}/><CommandMenu open onOpenChange={noop} data={makeBootstrap()}
      onCreateIssue={noop} onCreateDocument={noop} onCreateIssueTemplate={noop} onCreateProject={noop} onCreateView={noop} onCreateInitiative={noop} onSearchWorkspace={noop}
      onNavigateInbox={noop} onNavigateMyIssues={noop} onNavigateProjects={noop} onNavigateInitiatives={noop} onNavigateViews={noop} onNavigateMembers={noop}
      onNavigateCustomers={noop} onNavigateAgent={noop} onOpenResult={noop}/></I18nProvider></MemoryRouter>)
    await userEvent.type(screen.getByLabelText('Command menu', { selector: 'input' }), 'customer')
    const rows = screen.getAllByRole('option').filter(row => row.textContent?.startsWith('Display options'))
    expect(rows.map(row => [...row.querySelectorAll('.command-path > span')].map(part => part.textContent))).toEqual([['Display options', 'Grouping', 'Customer'], ['Display options', 'View ordering', 'By customer count']])
    await userEvent.click(within(rows[0]).getByText('Customer'))
    expect(grouping).toHaveBeenCalled()
  })
})
