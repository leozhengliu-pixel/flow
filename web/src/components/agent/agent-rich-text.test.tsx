import { render, screen, waitFor } from '@testing-library/react'
import { expect, it } from 'vitest'
import { AgentRichText } from './agent-rich-text'

it('keeps explicit email links, reference links and web links while leaving bare user emails as text', async () => {
  const content = 'Contact bare@example.com, [support](mailto:help@example.com), [mail][address] or [website](https://example.com).\n\n[address]: mailto:ref@example.com'
  const { rerender } = render(<AgentRichText autoLinkEmails={false} content={content}/>)
  await waitFor(() => expect(screen.getByRole('link', { name: 'support' })).toHaveAttribute('href', 'mailto:help@example.com'))
  expect(screen.getByRole('link', { name: 'mail' })).toHaveAttribute('href', 'mailto:ref@example.com')
  expect(screen.getByRole('link', { name: 'website' })).toHaveAttribute('href', 'https://example.com')
  expect(document.querySelector('a[href="mailto:bare@example.com"]')).toBeNull()
  rerender(<AgentRichText autoLinkEmails={false} content="[updated](mailto:new@example.com) plain@example.com"/>)
  await waitFor(() => expect(screen.getByRole('link', { name: 'updated' })).toHaveAttribute('href', 'mailto:new@example.com'))
  expect(document.querySelector('a[href="mailto:plain@example.com"]')).toBeNull()
})
