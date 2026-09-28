import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import {
  ContentViewContainer,
  ContentViewHeader,
  ContentViewHeaderBreadcrumb,
  ContentViewHeaderFavoriteActionButton,
  ContentViewHeaderTitle,
  ContentViewSubheader,
  NewContentViewHeaderTitle,
  ToolbarButtonsNavigation,
} from './index'

describe('ContentView kit', () => {
  it('renders a framed container with tall inset', () => {
    const { container } = render(
      <ContentViewContainer framed inset="tall" aria-label="My issues">
        body
      </ContentViewContainer>,
    )
    const root = container.querySelector('[data-content-view-container]')
    expect(root).toHaveAttribute('data-framed', 'true')
    expect(root).toHaveAttribute('data-inset', 'tall')
    expect(root).toHaveAttribute('aria-label', 'My issues')
  })

  it('renders breadcrumb links, favorite, title, and subheader slots', () => {
    const onFavorite = vi.fn()
    render(
      <ContentViewContainer>
        <ContentViewHeader compact onOpenSidebar={vi.fn()}>
          <ContentViewHeaderBreadcrumb
            items={[
              { id: 'team', label: 'Flow', href: '/flow/team/FLOW/all' },
              { id: 'issues', label: 'Issues', current: true },
            ]}
          />
          <ContentViewHeaderFavoriteActionButton favorited onClick={onFavorite} />
        </ContentViewHeader>
        <NewContentViewHeaderTitle title="Launch" icon={<span data-testid="icon" />} actions={<button type="button">Menu</button>} />
        <ContentViewSubheader start={<div>Tabs</div>} end={<div>Actions</div>} />
      </ContentViewContainer>,
    )
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Flow' })).toHaveAttribute('href', '/flow/team/FLOW/all')
    expect(screen.getByRole('switch', { name: 'Remove from favorites' })).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(screen.getByRole('switch', { name: 'Remove from favorites' }))
    expect(onFavorite).toHaveBeenCalledOnce()
    expect(screen.getByRole('heading', { name: 'Launch' })).toBeInTheDocument()
    expect(screen.getByTestId('icon')).toBeInTheDocument()
    expect(screen.getByText('Tabs')).toBeInTheDocument()
    expect(screen.getByText('Actions')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open sidebar' })).toBeInTheDocument()
  })

  it('moves focus across toolbar buttons with arrow keys', () => {
    render(
      <ToolbarButtonsNavigation>
        <button type="button">Filter</button>
        <button type="button">Display</button>
        <button type="button">Insights</button>
      </ToolbarButtonsNavigation>,
    )
    const buttons = screen.getAllByRole('button')
    buttons[0].focus()
    fireEvent.keyDown(buttons[0].parentElement!, { key: 'ArrowRight' })
    expect(buttons[1]).toHaveFocus()
    fireEvent.keyDown(buttons[1].parentElement!, { key: 'ArrowLeft' })
    expect(buttons[0]).toHaveFocus()
  })

  it('renders a plain header title', () => {
    render(<ContentViewHeaderTitle title="My issues" />)
    expect(screen.getByRole('heading', { name: 'My issues' })).toBeInTheDocument()
  })
})
