import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import { AgentContentWithBlocks } from './agent-content-blocks'
import { AgentRichText } from './agent-rich-text'

describe('AgentContentWithBlocks', () => {
  it('shows pasted formatting in a sent message: links, bold, lists and code blocks', async () => {
    const content = 'See [the docs](https://example.com/a) and **bold**\n\n- one\n- two\n\n```ts\nconst a = 1\n```'
    const { container } = render(<I18nProvider><AgentContentWithBlocks content={content} renderText={text => <AgentRichText content={text}/>}/></I18nProvider>)
    await screen.findByRole('link', { name: 'the docs' })
    expect(screen.getByRole('link', { name: 'the docs' })).toHaveAttribute('href', 'https://example.com/a')
    expect(container.querySelector('strong')).toHaveTextContent('bold')
    expect(container.querySelectorAll('li')).toHaveLength(2)
    expect(container).toHaveTextContent(/const\s*a\s*=\s*1/)
  })

  it('shows an embedded video block next to the text', () => {
    const { container } = render(<I18nProvider><AgentContentWithBlocks content={'Watch this\n::embed[https://www.youtube.com/watch?v=dQw4w9WgXcQ](https://www.youtube.com/watch?v=dQw4w9WgXcQ)\n'} renderText={text => <p>{text}</p>}/></I18nProvider>)
    expect(container.querySelector('iframe')).toHaveAttribute('src', 'https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ')
    expect(screen.getByText('Watch this')).toBeInTheDocument()
  })
})
