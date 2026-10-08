import { describe, expect, it } from 'vitest'
import { translateToChinese } from './i18n'
import { customerRequestsZhCN } from './translations-customer-requests'

describe('customer requests on issues and projects', () => {
  it('translates every section, composer, row menu, command and project string', () => {
    const strings = [
      // Issue Customers section and display options.
      'Customers', 'Collapse customers section', 'Expand customers section', 'Display options', 'Add customer request', 'Ordering',
      'Ordering direction', 'ascending', 'descending', 'Direction', 'Show important first', 'Created', 'Customer name', 'Customer revenue',
      // Picker and commands.
      'Add customer request…', 'Add customer request to issue…', 'Add customer request to project…', 'Add customer request to project',
      'Select customer…', 'Unknown customer', 'Type a name to create your first customer', 'Add link as source', 'Command menu',
      // Composer.
      'Search customers', 'Search customers…', 'New customer', 'Customer', 'Request', 'Add request details', 'Remove attachment',
      'Attach images, files, or videos', 'Discard', 'Cancel', 'Press', 'to create request', 'to save request', 'Save', 'Create', 'Add source',
      'Source', 'Source URL', 'Paste link…', 'Remove link', 'Clear source', 'Invalid URL, please enter a valid URL', 'Discard this request?',
      'Confirm that you want to discard this customer request.', 'Please select a customer, provide a request, or specify a source.',
      'Customer request updated', 'Customer request added', 'Failed to save customer request',
      // Rows and the request menu.
      'Number of requests', 'Important', 'Added by', 'Add customer to request', 'Change customer…', 'Open link', 'Request options',
      'Mark as important', 'Not important', 'Move to…', 'Move to issue…', 'Move to project…', 'Search projects…', 'No matching projects',
      'Search issues…', 'No matching issues', 'Copy', 'Copy link', 'Copy source link', 'URL copied to clipboard', 'Create issue from request…',
      'Edit request', 'New request on', 'Open customer', 'Delete', 'Delete customer request', 'You cannot undo this action.',
      'Failed to delete customer request', 'Customer request from', 'New issue', 'Issue created', 'Failed to create issue', 'Request moved to',
      'Customer changed to', 'Customer removed from request', 'No results',
      // Project overview row and Customers tab.
      'Open project customer requests', 'Add customer need', 'and', 'other', 'others', 'Add request', 'Customer requests', 'Documentation',
      'No customer requests created yet. Use a supported integration to automatically create requests, or create one manually.',
    ]
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
    for (const [source, zh] of Object.entries(customerRequestsZhCN)) expect(translateToChinese(source), source).toBe(zh)
    for (const template of ['Create new customer: "{name}"', 'New request from {name}', 'Delete customer request from {name}']) expect(translateToChinese(template)).toContain('{name}')
    expect(translateToChinese('Important to {count} customers')).toContain('{count}')
  })
})
