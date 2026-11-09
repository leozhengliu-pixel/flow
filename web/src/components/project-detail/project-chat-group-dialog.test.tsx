import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { SettingsSelect } from '@/components/settings/settings-primitives'
import { I18nProvider } from '@/i18n/i18n'
import * as api from '@/lib/chat-integrations-api'
import { makeBootstrap, project } from '@/test/fixtures'
import type { ChatIntegrationSettings } from '@/types/chat-integrations'

import { ProjectChatGroupDialog } from './project-chat-group-dialog'

vi.mock('@/lib/chat-integrations-api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/chat-integrations-api')>(),
  fetchChatIntegration: vi.fn(),
  fetchChatIntegrationChats: vi.fn(),
}))

const settings = (): ChatIntegrationSettings => ({
  connection: { id: 'connection-1', provider: 'feishu', name: 'Feishu', status: 'connected', config: {}, scopes: [], channels: [], linkbackEnabled: false, deliveryAttempts: 0, connectedBy: 'u', createdAt: '', updatedAt: '' },
  identity: null, bindings: [], bindableTeamIds: ['team-1'], credentialsConfigured: true, webhookURL: '', canManage: true,
  permissions: { tenant: { status: 'authorized' }, workspace: { canManage: true }, teams: { manageableTeamIds: ['team-1'], privateTeamIds: [] } },
})

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  localStorage.setItem('flow:locale', 'en-US')
  vi.mocked(api.fetchChatIntegration).mockResolvedValue(settings())
  vi.mocked(api.fetchChatIntegrationChats).mockResolvedValue({ items: [{ id: 'oc_room', name: 'Room', external: false }], hasMore: false })
})
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks() })

async function openForm() {
  render(<MemoryRouter><I18nProvider><ProjectChatGroupDialog open onOpenChange={vi.fn()} provider="feishu" project={{ ...project, teamIds: ['team-1'] }} data={makeBootstrap()}/></I18nProvider></MemoryRouter>)
  await userEvent.setup().click(await screen.findByRole('button', { name: 'Connect Feishu group' }))
  return screen.findByRole('form', { name: 'Add group binding' })
}

it('wears the notification panel skin, not the project form dialog or the settings dialog', async () => {
  await openForm()
  const dialog = screen.getByRole('dialog')
  expect(dialog).toHaveClass('chat-group-dialog')
  expect(dialog).not.toHaveClass('project-detail-page__form-dialog', 'feature-dialog', 'dialog-content')
  expect(within(dialog).getByRole('button', { name: 'Close' })).toBeInTheDocument()
})

it('renders selects as the shared settings select with a sized chevron', async () => {
  const form = await openForm()
  for (const name of ['Group', 'Team', 'Default template', 'Card language']) {
    const trigger = within(form).getByRole('combobox', { name })
    expect(trigger).toHaveClass('settings-select')
    const chevron = trigger.querySelector('svg')
    expect(chevron).toHaveAttribute('width', '9')
    expect(chevron).toHaveAttribute('height', '5')
  }
})

it('renders the notification options as the panel checkboxes and the Flow-only options as the shared switch', async () => {
  const form = await openForm()
  const options = within(form).getByRole('heading', { name: 'Notifications' }).parentElement!
  const checkboxes = [...options.querySelectorAll('button[role="checkbox"]')]
  expect(checkboxes.map(box => box.getAttribute('aria-label'))).toEqual(['Issue created', 'Status changed', 'Assignee changed', 'Comment added', 'Project update published', 'Initiative update published'])
  expect(within(form).getByRole('checkbox', { name: 'Issue created' })).toBeChecked()
  expect(within(form).getByRole('checkbox', { name: 'Comment added' })).not.toBeChecked()
  const switches = [...form.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')]
  expect(switches.length).toBeGreaterThanOrEqual(5)
  for (const input of switches) expect(input).toHaveAttribute('data-control', 'toggle')
  expect(within(form).getByRole('checkbox', { name: 'Allow creating issues' })).toBeChecked()
})

it('gives every icon in the dialog an explicit size', async () => {
  await openForm()
  const dialog = screen.getByRole('dialog')
  const unsized = [...dialog.querySelectorAll('svg, img')].filter(icon => !icon.getAttribute('width') || !icon.getAttribute('height'))
  expect(unsized.map(icon => icon.outerHTML.slice(0, 80))).toEqual([])
})

it('lays rows out as copy on the left and the control on the right', async () => {
  const form = await openForm()
  const row = within(form).getByRole('combobox', { name: 'Card language' }).closest('.feature-row')!
  expect(row.firstElementChild?.tagName).toBe('DIV')
  expect(row.firstElementChild).toHaveTextContent('Card language')
  expect(row.firstElementChild).toHaveTextContent('The language of the cards and messages Flow posts in this group.')
  expect(row.lastElementChild?.tagName).toBe('ASIDE')
  // The read-only project shows its name as a muted value in the control column.
  const value = within(form).getAllByText('Project one').find(element => element.classList.contains('chat-fixed-value'))!
  expect(value.parentElement?.tagName).toBe('ASIDE')
})

it('shows the placeholder on the phone select while no option is chosen', () => {
  const original = window.matchMedia
  window.matchMedia = ((query: string) => ({ matches: true, media: query, onchange: null, addEventListener: () => undefined, removeEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined, dispatchEvent: () => false })) as typeof window.matchMedia
  try {
    const { container } = render(<I18nProvider><SettingsSelect label="Group" options={[{ value: 'a', label: 'Alpha' }]} value="__none__" placeholder="Select group" onChange={vi.fn()}/></I18nProvider>)
    const native = container.querySelector<HTMLSelectElement>('select.settings-select-native')!
    expect(native.value).toBe('__none__')
    expect(native.selectedOptions[0]).toHaveTextContent('Select group')
  } finally { window.matchMedia = original }
})

it("starts on the project's only shareable team", async () => {
  const form = await openForm()
  expect(within(form).getByRole('combobox', { name: 'Team' })).not.toHaveTextContent('Select team')
})

it('saves the project scope after the team is picked', async () => {
  const save = vi.spyOn(api, 'saveChatBinding').mockResolvedValue({})
  const form = await openForm()
  const user = userEvent.setup()
  await user.click(await within(form).findByRole('combobox', { name: 'Group' }))
  await user.click(await screen.findByRole('option', { name: 'Room' }))
  await user.click(within(form).getByLabelText(/I approve sharing/))
  await user.click(within(form).getByRole('button', { name: 'Save group binding' }))
  expect(save).toHaveBeenCalledWith('feishu', expect.objectContaining({ chatId: 'oc_room', projectIds: [project.id] }))
})
