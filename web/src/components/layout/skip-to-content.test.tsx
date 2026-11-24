import { act, render, screen, waitFor } from '@testing-library/react'
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

  it('hands focus to the loaded route when its loading landmark is replaced', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<><SkipToContent /><main key="loading" aria-busy="true">Loading</main></>)
    await user.tab()
    await user.keyboard('{Enter}')
    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveFocus()
    rerender(<><SkipToContent /><main key="loaded">Loaded content</main></>)
    await waitFor(() => expect(screen.getByRole('main')).toHaveFocus())
  })

  it('leaves focus alone when the user tabs away before the route loads', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<><SkipToContent /><button>Sidebar</button><main key="loading" aria-busy="true">Loading</main></>)
    await user.tab()
    await user.keyboard('{Enter}')
    await user.tab()
    await act(async () => {
      rerender(<><SkipToContent /><button>Sidebar</button><main key="loaded">Loaded content</main></>)
    })
    expect(screen.getByRole('button', { name: 'Sidebar' })).toHaveFocus()
  })
})
