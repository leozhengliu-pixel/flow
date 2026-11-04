import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { SkipToContent } from './skip-to-content'

describe('skip to content', () => {
  it('is the first Tab stop and moves focus to the main content', async () => {
    const user = userEvent.setup()
    render(<><SkipToContent /><button>Sidebar</button><main>Content</main></>)
    await user.tab()
    const link = screen.getByRole('link', { name: 'Skip to content' })
    expect(link).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('main')).toHaveFocus()
  })
})
