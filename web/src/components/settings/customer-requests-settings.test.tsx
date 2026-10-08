import type { ComponentProps } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { toast } from 'sonner'

import { ActionDialogHost } from '@/components/ui/action-dialogs'
import { I18nProvider } from '@/i18n/i18n'
import { archiveCustomerTaxonomyItem, createCustomerTaxonomyItem, customerCurrencyOptions, restoreCustomerTaxonomyItem, updateCustomerTaxonomyItem } from '@/lib/customer-settings'
import { makeBootstrap } from '@/test/fixtures'
import type { BootstrapData, CustomerStatus, WorkspaceSettings } from '@/types/flow'

import { CustomerRequestsSettings } from './customer-requests-settings'

vi.mock('@/lib/customer-settings', async original => ({
  ...await original<typeof import('@/lib/customer-settings')>(),
  createCustomerTaxonomyItem: vi.fn(),
  updateCustomerTaxonomyItem: vi.fn(),
  archiveCustomerTaxonomyItem: vi.fn(),
  restoreCustomerTaxonomyItem: vi.fn(),
}))
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }))

const now = '2026-10-01T00:00:00Z'
const statuses: CustomerStatus[] = [
  { id: 'active', name: 'Active', color: '#5e6ad2', position: 0, createdAt: now, updatedAt: now },
  { id: 'prospect', name: 'Prospect', description: 'Evaluating', color: '#4cb782', position: 1, createdAt: now, updatedAt: now },
  { id: 'old', name: 'Old', color: '#eb5757', position: 2, archivedAt: now, createdAt: now, updatedAt: now },
]
const featureSettings = { customerRevenueFormat: 'annual', customerRevenueCurrency: 'USD', customerManualEdits: true, customerStatuses: [], customerTiers: [], customerExcludedDomains: [], customerGenericDomains: [] }
const settings = (enabled = true, overrides: Record<string, unknown> = {}) => ({ featureFlags: { 'customer-requests': enabled }, featureSettings: { ...featureSettings, ...overrides } }) as unknown as WorkspaceSettings

beforeEach(() => { vi.clearAllMocks(); localStorage.setItem('flow:locale', 'en-US') })

type Props = ComponentProps<typeof CustomerRequestsSettings>
function renderPage({ enabled = true, overrides = {}, data = {}, setFeature = vi.fn(), setEnabled = vi.fn(), onReload = vi.fn(async () => {}) }: { enabled?: boolean; overrides?: Record<string, unknown>; data?: Partial<BootstrapData>; setFeature?: Props['setFeature']; setEnabled?: Props['setEnabled']; onReload?: () => Promise<void> } = {}) {
  const bootstrap = makeBootstrap({ customers: [], customerStatuses: statuses, customerTiers: [], viewerRole: 'admin', ...data })
  render(<MemoryRouter><I18nProvider>
    <CustomerRequestsSettings data={bootstrap} settings={settings(enabled, overrides)} busy={false} setEnabled={setEnabled} setFeature={setFeature} onReload={onReload}/>
    <ActionDialogHost/>
  </I18nProvider></MemoryRouter>)
  return { bootstrap, setFeature, setEnabled, onReload }
}

it('renders the page structure with links, counts and the data source tooltip trigger', () => {
  const { bootstrap } = renderPage()
  expect(screen.getByRole('heading', { level: 1, name: 'Customer requests' })).toBeVisible()
  expect(screen.getByRole('link', { name: 'Docs' })).toHaveAttribute('href', 'https://flow.app/docs/customer-requests')
  expect(screen.getByRole('link', { name: 'List of generic domains' })).toHaveAttribute('target', '_blank')
  expect(screen.getByRole('link', { name: 'Manage customers settings' })).toHaveAttribute('href', `/${bootstrap.workspace.urlKey}/customers`)
  expect(screen.getByText('No customers')).toBeVisible()
  // Archived statuses are hidden; the header counts the rest.
  expect(screen.getByText('2 customer statuses')).toBeVisible()
  expect(screen.queryByText('Old')).toBeNull()
  expect(screen.getByText('Evaluating')).toBeVisible()
  expect(screen.getByText('No customer tiers')).toBeVisible()
  expect(screen.getByText('None')).toHaveAttribute('tabindex', '0')
  expect(screen.getByText('Excluded domains and emails').tagName).toBe('SPAN')
  expect(screen.getByRole('heading', { level: 3, name: 'Issue routing' })).toBeVisible()
  expect(screen.getByRole('combobox', { name: 'Revenue currency' })).toHaveTextContent('USD ($)')
  expect(screen.getByRole('combobox', { name: 'Default team for customer requests' })).toHaveTextContent('No default team')
})

it('formats and pluralises the customer count', () => {
  const customer = { id: 'c', name: 'Acme', status: 'Active', domains: [], createdAt: now, updatedAt: now }
  renderPage({ data: { customers: Array.from({ length: 1234 }, (_, index) => ({ ...customer, id: `c${index}` })) } })
  expect(screen.getByText('1,234 customers')).toBeVisible()
})

it('offers Linear currencies with symbols, preferred ones first', () => {
  const options = customerCurrencyOptions()
  expect(options.slice(0, 3).map(option => option.label)).toEqual(['USD ($)', 'EUR (€)', 'GBP (£)'])
  expect(options[3].value).toBe('AUD')
  expect(options[3].divider).toBe(true)
  expect(options).toHaveLength(20)
})

it('edits a status inline and saves name, description and colour', async () => {
  const user = userEvent.setup()
  vi.mocked(updateCustomerTaxonomyItem).mockResolvedValue({ ...statuses[0], name: 'Live' })
  const { onReload } = renderPage()
  const row = screen.getByText('Active').closest('[aria-roledescription=sortable]') as HTMLElement
  await user.click(within(row).getByRole('button', { name: 'Open menu' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
  const name = screen.getByRole('textbox', { name: 'Name' })
  expect(name).toHaveFocus()
  expect(name).toHaveAttribute('maxLength', '25')
  expect(screen.getByRole('textbox', { name: 'Description' })).toHaveAttribute('placeholder', 'Description…')
  expect(screen.getByRole('button', { name: 'Color' })).toBeVisible()
  await user.clear(name)
  await user.type(name, 'Live')
  await user.type(screen.getByRole('textbox', { name: 'Description' }), 'Paying{Enter}')
  await waitFor(() => expect(updateCustomerTaxonomyItem).toHaveBeenCalledWith('status', 'active', { name: 'Live', description: 'Paying', color: '#5e6ad2' }))
  expect(onReload).toHaveBeenCalled()
  expect(screen.queryByRole('textbox', { name: 'Name' })).toBeNull()
})

it('rejects an empty name with a toast and cancels on Escape', async () => {
  const user = userEvent.setup()
  renderPage()
  await user.click(screen.getByRole('button', { name: 'Create new customer status' }))
  const name = screen.getByRole('textbox', { name: 'Name' })
  await user.click(screen.getByRole('button', { name: 'Submit' }))
  expect(toast.error).toHaveBeenCalledWith('Name required', { description: 'The customer status name cannot be empty.' })
  expect(createCustomerTaxonomyItem).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: 'Submit' })).toHaveTextContent('Create')
  await user.type(name, '{Escape}')
  expect(screen.queryByRole('textbox', { name: 'Name' })).toBeNull()
})

it('creates tiers at the bottom without a colour field', async () => {
  const user = userEvent.setup()
  vi.mocked(createCustomerTaxonomyItem).mockResolvedValue({ id: 'gold', name: 'Gold', color: '#8a8f98', position: 0, createdAt: now, updatedAt: now })
  renderPage()
  await user.click(screen.getByRole('button', { name: 'Create new customer tier' }))
  expect(screen.queryByRole('button', { name: 'Color' })).toBeNull()
  await user.type(screen.getByRole('textbox', { name: 'Name' }), 'Gold{Enter}')
  await waitFor(() => expect(createCustomerTaxonomyItem).toHaveBeenCalledWith('tier', { name: 'Gold', description: '', position: 0 }))
  expect(await screen.findByText('1 customer tier')).toBeVisible()
})

it('deletes a status with an undo toast that restores the reassigned customers', async () => {
  const user = userEvent.setup()
  vi.mocked(archiveCustomerTaxonomyItem).mockResolvedValue({ ...statuses[0], archivedAt: now, reassignedCustomerIds: ['c1'] })
  vi.mocked(restoreCustomerTaxonomyItem).mockResolvedValue(statuses[0])
  renderPage()
  const row = screen.getByText('Active').closest('[aria-roledescription=sortable]') as HTMLElement
  await user.click(within(row).getByRole('button', { name: 'Open menu' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Delete' }))
  await waitFor(() => expect(archiveCustomerTaxonomyItem).toHaveBeenCalledWith('status', 'active'))
  expect(screen.queryByText('Active')).toBeNull()
  const [title, options] = vi.mocked(toast).mock.calls[0] as unknown as [string, { action: { label: string; onClick: () => void } }]
  expect(title).toBe('Customer status deleted')
  expect(options.action.label).toBe('Undo delete Active')
  options.action.onClick()
  await waitFor(() => expect(restoreCustomerTaxonomyItem).toHaveBeenCalledWith('status', 'active', ['c1']))
})

it('adds an excluded domain inline after confirming, with validation', async () => {
  const user = userEvent.setup()
  const setFeature = vi.fn(async () => {})
  const { onReload } = renderPage({ setFeature, overrides: { customerExcludedDomains: ['acme.com'] } })
  expect(screen.getByText('1 exclusion')).toBeVisible()
  const card = screen.getByText('1 exclusion').closest('section') as HTMLElement
  await user.click(within(card).getAllByRole('button', { name: 'Open menu' })[0])
  const input = screen.getByRole('textbox', { name: 'Domain or email address' })
  expect(input).toHaveFocus()
  expect(input).toHaveAttribute('maxLength', '100')
  await user.click(screen.getByRole('button', { name: 'Submit' }))
  expect(screen.getByRole('alert')).toHaveTextContent('Source is required')
  await user.type(input, 'ACME.com{Enter}')
  expect(screen.getByRole('alert')).toHaveTextContent('Source already is already hidden')
  await user.clear(input)
  await user.type(input, 'bad@x{Enter}')
  expect(screen.getByRole('alert')).toHaveTextContent('Email address is not valid')
  await user.clear(input)
  await user.type(input, 'localhost{Enter}')
  expect(screen.getByRole('alert')).toHaveTextContent('Please enter a valid domain')
  await user.clear(input)
  await user.type(input, 'Spam@Example.com{Enter}')
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toHaveTextContent('Add spam@example.com to the list of excluded domains and emails?')
  expect(dialog).toHaveTextContent('Requests will not be created from this email, and any existing requests will be archived.')
  await user.click(within(dialog).getByRole('button', { name: 'Add' }))
  await waitFor(() => expect(setFeature).toHaveBeenCalledWith('customerExcludedDomains', ['acme.com', 'spam@example.com']))
  expect(onReload).toHaveBeenCalled()
})

it('removes an excluded domain after confirming and generic ones without', async () => {
  const user = userEvent.setup()
  const setFeature = vi.fn(async () => {})
  renderPage({ setFeature, overrides: { customerExcludedDomains: ['acme.com'], customerGenericDomains: ['mail.test', 'a@b.test'] } })
  expect(screen.getByText('2 domains and emails')).toBeVisible()
  const row = screen.getByText('acme.com').closest('.crs-source-row') as HTMLElement
  await user.click(within(row).getByRole('button', { name: 'Open menu' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Remove' }))
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toHaveTextContent('Remove acme.com from the list of excluded domains and emails?')
  expect(dialog).toHaveTextContent('Requests from this domain will be converted to customer requests, including any that were previously excluded.')
  await user.click(within(dialog).getByRole('button', { name: 'Remove' }))
  await waitFor(() => expect(setFeature).toHaveBeenCalledWith('customerExcludedDomains', []))
  const generic = screen.getByText('mail.test').closest('.crs-source-row') as HTMLElement
  await user.click(within(generic).getByRole('button', { name: 'Open menu' }))
  await user.click(await screen.findByRole('menuitem', { name: 'Remove' }))
  await waitFor(() => expect(setFeature).toHaveBeenCalledWith('customerGenericDomains', ['a@b.test']))
})

it('disables every control while the feature is off, except the toggle', () => {
  renderPage({ enabled: false, overrides: { customerExcludedDomains: ['acme.com'] } })
  expect(screen.getByRole('checkbox', { name: 'Enable Customer requests' })).toBeEnabled()
  expect(screen.getByRole('checkbox', { name: 'Enable manual edits' })).toBeDisabled()
  expect(screen.getByRole('combobox', { name: 'Default team for customer requests' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Create new customer status' })).toBeDisabled()
  expect(screen.queryByRole('menuitem')).toBeNull()
  for (const button of screen.getAllByRole('button', { name: 'Open menu' })) expect(button).toBeDisabled()
  expect(screen.getByRole('link', { name: 'Manage customers settings' })).toHaveAttribute('aria-disabled', 'true')
})

it('lets members manage settings but only admins toggle the feature; guests can do neither', () => {
  renderPage({ data: { viewerRole: 'member' } })
  expect(screen.getByRole('checkbox', { name: 'Enable Customer requests' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Create new customer status' })).toBeEnabled()
})

it('blocks guests from managing settings', () => {
  renderPage({ data: { viewerRole: 'guest' } })
  expect(screen.getByRole('button', { name: 'Create new customer status' })).toBeDisabled()
  expect(screen.getByRole('checkbox', { name: 'Enable manual edits' })).toBeDisabled()
})
