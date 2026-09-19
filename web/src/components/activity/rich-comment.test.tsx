import { render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
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
