import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import { registerAppNavigator } from '@/lib/app-navigation'
import { RichComment } from './rich-comment'

const markdown = `## Heading

First paragraph.

Second paragraph.

- item one
- item two

This has **bold** and \`inline\` and a [link](https://linear.app).

\`\`\`
block code
\`\`\`
`

const richDocument = {
  type: 'doc',
  content: [
    { type: 'heading', attrs: { level: 3 }, content: [{ type: 'text', text: 'Structured' }] },
    { type: 'paragraph', content: [
      { type: 'mention', attrs: { id: 'user-1', label: 'Viewer' } },
      { type: 'text', text: ' mentioned' },
    ] },
  ],
}

describe('RichComment markdown rendering', () => {
  it('parses markdown when bodyData is missing', async () => {
    render(<div className="timeline-item comment"><div className="comment-body"><RichComment body={markdown}/></div></div>)
    expect(await screen.findByRole('heading', { name: 'Heading' })).toBeVisible()
    expect(screen.getByText('First paragraph.')).toBeVisible()
    expect(screen.getByText('Second paragraph.')).toBeVisible()
    expect(screen.getByRole('list')).toBeVisible()
    expect(screen.getByText('item one')).toBeVisible()
    expect(screen.getByText('bold').tagName).toBe('STRONG')
    expect(screen.getByText('inline').tagName).toBe('CODE')
    expect(screen.getByRole('link', { name: 'link' })).toHaveAttribute('href', 'https://linear.app')
    expect(screen.getByText('block code').closest('pre')).not.toBeNull()
    expect(screen.queryByText('## Heading')).not.toBeInTheDocument()
  })

  it('parses markdown when bodyData is invalid', async () => {
    render(<RichComment body="## Fallback heading" data={{ type: 'paragraph', content: [] }}/>)
    expect(await screen.findByRole('heading', { name: 'Fallback heading' })).toBeVisible()
  })

  it('renders inline images from comment bodyData', async () => {
    render(<RichComment body="![shot](/uploads/shot.png)" data={{ type: 'doc', content: [{ type: 'image', attrs: { src: '/uploads/shot.png', alt: 'shot' } }] }}/>)
    const image = await screen.findByRole('img', { name: 'shot' })
    expect(image).toHaveAttribute('src', '/uploads/shot.png')
  })

  it('keeps valid rich-text bodyData instead of the markdown projection', async () => {
    render(<RichComment body="## This markdown should not win" data={richDocument} version={3}/>)
    expect(await screen.findByRole('heading', { name: 'Structured' })).toBeVisible()
    expect(screen.getByText('@Viewer')).toHaveAttribute('data-flow-mention', 'user-1')
    expect(screen.queryByRole('heading', { name: 'This markdown should not win' })).not.toBeInTheDocument()
  })

  it('refreshes the DOM when body, bodyData, or version changes', async () => {
    const view = render(<RichComment body="Before" version={1}/>)
    expect(await screen.findByText('Before')).toBeVisible()
    expect(screen.queryByRole('heading')).not.toBeInTheDocument()

    view.rerender(<RichComment body={'## After\n\nUpdated paragraph.'} version={2}/>)
    expect(await screen.findByRole('heading', { name: 'After' })).toBeVisible()
    expect(screen.getByText('Updated paragraph.')).toBeVisible()
    await waitFor(() => expect(screen.queryByText('Before')).not.toBeInTheDocument())

    view.rerender(<RichComment body="ignored markdown" data={richDocument} version={3}/>)
    expect(await screen.findByRole('heading', { name: 'Structured' })).toBeVisible()
    expect(screen.queryByRole('heading', { name: 'After' })).not.toBeInTheDocument()
  })
})

describe('RichComment links', () => {
  beforeAll(() => {
    Range.prototype.getClientRects = () => [] as unknown as DOMRectList
    Range.prototype.getBoundingClientRect = () => new DOMRect()
    Document.prototype.elementFromPoint = function elementFromPoint(this: Document) { return this.body }
  })
  it('opens an external link in a new tab and a workspace link in-app, and blocks unsafe schemes', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const navigate = vi.fn()
    const unregister = registerAppNavigator(navigate)
    try {
      render(<RichComment body={'[outside](https://example.com/a) and [inside](/acme/issue/FLO-1/title) and [bad](javascript:alert(1))'}/>)
      const user = userEvent.setup()
      await user.click(await screen.findByRole('link', { name: 'outside' }))
      expect(open).toHaveBeenCalledWith('https://example.com/a', '_blank', 'noopener,noreferrer')
      await user.click(screen.getByRole('link', { name: 'inside' }))
      expect(navigate).toHaveBeenCalledWith('/acme/issue/FLO-1/title')
      expect(open).toHaveBeenCalledTimes(1)
      expect(screen.queryByRole('link', { name: 'bad' })).toBeNull()
      await user.keyboard('{Meta>}')
      await user.click(screen.getByRole('link', { name: 'inside' }))
      await user.keyboard('{/Meta}')
      expect(open).toHaveBeenCalledTimes(2)
    } finally {
      unregister()
      open.mockRestore()
    }
  })
})
