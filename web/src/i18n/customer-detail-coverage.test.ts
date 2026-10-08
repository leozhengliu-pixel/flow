import { describe, expect, it } from 'vitest'
import { translateToChinese } from './i18n'

describe('customer detail page strings', () => {
  it('translates every customer page control, menu item, state and template', () => {
    const strings = [
      'Customers', 'Customer requests', 'Requests', 'Add request', 'Add customer request', 'Add request details', 'Note', 'Request', 'Source', 'Add source',
      'Paste link…', 'Remove link', 'Clear source', 'Attach images, files or videos', 'Cancel', 'Discard', 'Save', 'Create', 'Press', 'to add request', 'to create request',
      'This request will be added to', 'New issue', 'Edit new issue title', 'Save new title', 'Search issues or projects…', 'Recent projects', 'Projects', 'Recent issues', 'Issues',
      'No matching issues or projects', 'Request details or source required', 'When linked to a new issue, the request must have details or a source.',
      'Invalid URL, please enter a valid URL', 'Discard this request?', 'Confirm that you want to discard this customer request.', 'Customer request updated',
      'Customer request added', 'Failed to save customer request', 'Create a team before adding requests',
      'Status', 'Tier', 'Revenue', 'Size', 'Owner', 'No owner', 'Change customer status…', 'Change customer owner…', 'Manual edits of attributes are disabled', 'Edit customer', 'Edit customer…',
      'Open customer menu', 'Add to favorites', 'Remove from favorites', 'Copy page URL', 'Copy customer URL', 'Customer notifications', 'Setup customer notifications', 'Send inbox notifications for',
      'A request is added', 'A request is marked as important', 'A requested issue or project is completed or canceled', 'Could not update customer notifications',
      'Edit…', 'Favorite', 'Unfavorite', 'Copy…', 'Copy URL', 'Copy requests as Markdown', 'Subscribe', 'Show archived requests', 'Hide archived requests', 'Merge with…',
      'Search for customer to merge with…', 'Delete', 'Customer actions', 'Are you sure you want to delete this customer?', 'This will also delete all associated customer requests.',
      'Could not delete customer', 'Could not update customer', 'Customer URL copied to clipboard', 'Customer requests copied to clipboard', 'Could not copy to clipboard',
      'Display options', 'Grouping', 'Ordering', 'Completed', 'Status type', 'None', 'Created date', 'All', 'Past day', 'Past week', 'Past month', 'Show important first',
      'Display properties', 'ID', 'Priority', 'Target/Due date', 'Triage', 'Started', 'Unstarted', 'Backlog', 'Canceled', 'Expand group', 'Collapse group',
      'Request with details', 'Open issue', 'Open project', 'Due date', 'Target date', 'Archived', 'Issue is archived', 'Project is archived', 'Number of requests', 'Important',
      'Customer request actions', 'Added by', 'Open link', 'Loading…', 'Unlinked request',
      'Mark as important', 'Not important', 'Move to…', 'Move to issue…', 'Move to project…', 'Copy', 'Copy link', 'Copy source link', 'Create issue from request…', 'Create issue',
      'Edit request', 'New request on issue', 'New request on project', 'Change customer…', 'Search customers…', 'Search issues…', 'Search projects…', 'No results',
      'URL copied to clipboard', 'Source URL copied to clipboard', 'You cannot undo this action.', 'Something went wrong',
      'No customer requests created yet. Use a supported integration to automatically create requests, or create one manually.',
      'All customer requests for this customer are archived. Create a new request or show the archived ones.', 'Documentation', 'Show options', 'Show 1 archived request',
      '1 important request', '1 request hidden by display options', 'Customers merged', 'Unable to merge customers', 'Something unexpected went wrong when merging customers.',
      'Merge customers', 'Merge summary', 'Switch direction', 'Merge', 'With', 'Result', 'Name', 'Domains', 'Type merge to continue', 'Please type the required string',
      'Customer not found', 'This customer does not exist or has been deleted.', '/yr', '/mo', 'just now', 'now', '1 minute ago', '1 hour ago', '1 day ago',
    ]
    for (const source of strings) expect(translateToChinese(source), source).not.toBe(source)
    const templates = [
      'Created at {date}', 'Request from {customer} added to {target}', 'Customer request moved to {target}', 'Customer changed to {customer}', 'Delete customer request from {customer}',
      'Delete {name}', 'Remove {name}', 'via {host}', '{count} important requests', '{count} requests hidden by display options', 'Show {count} archived requests',
      'Type {word} to continue', '{count}min', '{count}h', '{count}d', '{count}w', '{count}mo', '{count}y', '{count} minutes ago', '{count} hours ago', '{count} days ago',
      '{count} weeks ago', '{count} months ago', '{count} years ago',
    ]
    for (const template of templates) {
      const translated = translateToChinese(template)
      expect(translated, template).not.toBe(template)
      for (const token of template.match(/\{\w+\}/g) ?? []) expect(translated, template).toContain(token)
    }
  })
})
