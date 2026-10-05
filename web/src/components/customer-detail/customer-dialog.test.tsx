import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import type { Customer, User } from '@/types/flow'

import { CustomerDialog } from './customer-dialog'
import { validateCustomerDraft } from './customer-form-model'

const users = [{ id: 'user-1', name: 'ada', displayName: 'Ada Lovelace', email: 'ada@example.com', active: true }] as unknown as User[]

function renderDialog(props: Partial<Parameters<typeof CustomerDialog>[0]> = {}) {
  const onSubmit = vi.fn().mockResolvedValue(undefined)
  const onOpenChange = vi.fn()
  render(<I18nProvider><CustomerDialog open users={users} onOpenChange={onOpenChange} onSubmit={onSubmit} {...props}/></I18nProvider>)
  return { onSubmit, onOpenChange }
}

describe('CustomerDialog (Linear create customer modal)', () => {
  it('lays out Linear\'s fields in order with the logo picker and footer actions', () => {
    renderDialog()
    expect(screen.getByRole('dialog', { name: 'Create customer' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Upload logo' })).toBeVisible()
    expect(screen.getByText('Recommended size is 128 x 128px.')).toBeVisible()
    const labels = [...document.querySelectorAll('.customer-form__field > label, .customer-form__field > .customer-form__label, .customer-form__domains-header > label')].map(node => node.textContent)
    expect(labels).toEqual(['Name', 'Owner', 'Status', 'Tier', 'Annual revenue', 'Size', 'Domains'])
    expect(screen.getByPlaceholderText('Customer name')).toHaveFocus()
    expect(screen.getByPlaceholderText('customer.com')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Add domain' })).toBeVisible()
    expect(screen.queryByLabelText('Logo URL')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Create customer' })).toHaveAttribute('type', 'submit')
    // The revenue field shows the workspace currency symbol inside the input.
    expect(document.querySelector('.customer-form__number > span')).toHaveTextContent('$')
  })

  it('keeps the owner chevron inside its sized wrapper', () => {
    renderDialog()
    const owner = screen.getByRole('combobox', { name: 'Owner' })
    const chevron = owner.querySelector('svg')
    expect(chevron?.parentElement?.tagName).toBe('SPAN')
    expect(chevron?.parentElement).toBe(owner.lastElementChild)
  })

  it('validates like Linear before creating', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderDialog()
    await user.click(screen.getByRole('button', { name: 'Create customer' }))
    expect(screen.getByText('Name is a required field')).toBeVisible()
    expect(onSubmit).not.toHaveBeenCalled()

    await user.type(screen.getByPlaceholderText('Customer name'), 'Acme')
    await user.type(screen.getByPlaceholderText('customer.com'), 'not a domain')
    await user.click(screen.getByRole('button', { name: 'Create customer' }))
    expect(screen.getByText('Please enter a valid domain')).toBeVisible()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('submits every field, with several domains', async () => {
    const user = userEvent.setup()
    const { onSubmit, onOpenChange } = renderDialog()
    await user.type(screen.getByPlaceholderText('Customer name'), '  Acme  ')
    await user.type(screen.getByLabelText('Annual revenue'), '12,500x')
    await user.type(screen.getByLabelText('Size'), '40')
    await user.type(screen.getByPlaceholderText('customer.com'), 'acme.com')
    await user.click(screen.getByRole('button', { name: 'Add domain' }))
    const domains = screen.getAllByPlaceholderText('customer.com')
    expect(domains).toHaveLength(2)
    await user.type(domains[1], 'https://acme.io/path')
    expect(screen.getAllByRole('button', { name: 'Remove domain' })).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: 'Create customer' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({
      name: 'Acme',
      logoUrl: undefined,
      ownerId: undefined,
      status: 'active',
      tier: undefined,
      annualRevenue: 12500,
      size: 40,
      domains: ['acme.com', 'acme.io'],
    }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('edits an existing customer and can clear its logo', async () => {
    const user = userEvent.setup()
    const customer = { id: 'c1', name: 'Acme', logoUrl: 'https://acme.com/logo.png', status: 'inactive', tier: 'Enterprise', annualRevenue: 1000, size: 5, domains: ['acme.com'], createdAt: '', updatedAt: '' } as Customer
    const { onSubmit } = renderDialog({ customer, tiers: [{ name: 'Enterprise', color: '#5e6ad2' }] })
    expect(screen.getByRole('dialog', { name: 'Edit customer' })).toBeVisible()
    expect(screen.getByLabelText('Annual revenue')).toHaveValue('1,000')
    await user.click(screen.getByRole('button', { name: 'Remove image' }))
    expect(screen.queryByRole('button', { name: 'Remove image' })).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Update customer' }))
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ name: 'Acme', logoUrl: '', status: 'inactive', tier: 'Enterprise', domains: ['acme.com'] })))
  })

  it('warns about a customer with the same name', async () => {
    const user = userEvent.setup()
    renderDialog({ customers: [{ id: 'c2', name: 'Globex' }] })
    await user.type(screen.getByPlaceholderText('Customer name'), 'globex')
    expect(screen.getByRole('status')).toHaveTextContent('A customer with this name already exists: Globex')
  })

  it('translates the modal', async () => {
    const previousLocale = localStorage.getItem('flow:locale')
    localStorage.setItem('flow:locale', 'zh-CN')
    try {
      renderDialog()
      expect(await screen.findByRole('dialog', { name: '创建客户' })).toBeVisible()
      expect(screen.getByText('建议尺寸为 128 x 128 像素。')).toBeVisible()
      expect(screen.getByRole('button', { name: '添加域名' })).toBeVisible()
    } finally {
      if (previousLocale === null) localStorage.removeItem('flow:locale')
      else localStorage.setItem('flow:locale', previousLocale)
    }
  })
})

describe('validateCustomerDraft', () => {
  it('applies Linear\'s limits and domain rules', () => {
    expect(validateCustomerDraft({ name: 'A', annualRevenue: '2147483648', size: '2147483648', domains: ['a.com', 'A.com'] })).toEqual({
      annualRevenue: 'Annual revenue must be less than or equal to 2147483647',
      size: 'Size must be less than or equal to 2147483647',
      domainsAll: 'All domains must be unique',
    })
    expect(validateCustomerDraft({ name: 'A', annualRevenue: '2147483647', size: '', domains: ['', 'sub.example.org'] })).toEqual({})
  })
})
