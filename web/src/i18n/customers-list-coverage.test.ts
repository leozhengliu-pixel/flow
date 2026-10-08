import { describe, expect, it } from 'vitest'
import { translateToChinese } from './i18n'

describe('customers page, create customer dialog and customer commands', () => {
  it('translates every visible string', () => {
    const strings = [
      // Header, toolbar, empty state
      'Customers', 'New customer', 'Find by name or domain…', 'Add filter', 'Add another filter', 'Display options', 'Filter',
      'Add organizations using your product to track their feature requests and use attributes like revenue and size to prioritize development.',
      'Create new customer', 'Documentation', 'No customers matching the filters', 'Clear filters', 'No results',
      // Columns, display options
      'Name', 'Requests', 'Annual revenue', 'Monthly revenue', 'Size', 'Status', 'Tier', 'Owner', 'Domains', 'Data source',
      'Request count', 'Created', 'Order by', 'sorted ascending', 'sorted descending',
      // Filters and chips
      'Revenue', 'Enter revenue…', 'Enter size…', 'No owner', 'Current user', 'greater than or equals', 'less than or equals', 'equals', 'not equals',
      'is', 'is not', 'is any of', 'is none of', 'statuses', 'tiers', 'users', 'values', 'operator', 'Remove filter', 'Clear', 'Clear all filters',
      'Customer filters', 'Match', 'all filters', 'any filter', 'Filter conjunction', 'Remove advanced filter',
      // Row pickers and context menu
      'Change customer status', 'Change customer owner', 'Change customer status…', 'Change customer owner…', 'Customer actions',
      'Edit…', 'Favorite', 'Unfavorite', 'Copy…', 'Copy URL', 'Subscribe', 'Unsubscribe', 'Delete',
      'Customer URL copied to clipboard', 'Subscribed to customer', 'Unsubscribed from customer',
      'Are you sure you want to delete this customer?', 'Are you sure you want to delete this customer? This will also delete all associated customer requests.',
      // Dialog
      'Create customer', 'Edit customer', 'Update customer', 'Customer name', 'Logo', 'Recommended size is 128 x 128px.', 'No tier', 'Add domain', 'Cancel',
      // Command menu
      'Create new customer…', 'Go to customers', 'Open customer…',
    ]
    for (const source of strings) {
      const translated = translateToChinese(source)
      expect(translated, source).not.toBe(source)
      // A whole-sentence translation, not a word-by-word fallback that leaves English behind.
      expect(translated, source).not.toMatch(/[a-z]{4,}/)
    }
  })
})
