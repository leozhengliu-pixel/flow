import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { I18nProvider } from '@/i18n/i18n'
import { WorkspaceWelcomeInboxView } from './workspace-welcome-inbox-view'

describe('workspace welcome inbox view', () => {
  it('renders the welcome document instead of an unavailable issue state', () => {
    render(<I18nProvider><WorkspaceWelcomeInboxView title="Welcome to Flow" message="Start here." /></I18nProvider>)
    expect(screen.getByRole('heading', { name: 'Welcome to Flow' })).toBeVisible()
    expect(screen.getByText('Start here.')).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Resources' })).toBeVisible()
    expect(screen.queryByText('Issue is no longer available')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Flow documentation' })).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/tree/main/docs')
    expect(screen.getByRole('link', { name: 'Community and support' })).toHaveAttribute('href', 'https://github.com/leozhengliu-pixel/flow/issues')
    expect(screen.queryByText('Introductory video unavailable')).not.toBeInTheDocument()
  })

  it('plays Flow\'s own intro video without autoplay, with captions and labelled controls', () => {
    const { container } = render(<I18nProvider><WorkspaceWelcomeInboxView title="Welcome to Flow" message="" /></I18nProvider>)
    const video = container.querySelector('video')!
    expect(video).toHaveAttribute('poster', '/media/flow-intro-poster.jpg')
    expect(video).not.toHaveAttribute('autoplay')
    expect(video.autoplay).toBe(false)
    expect(video).toHaveAttribute('aria-label')
    expect(video.querySelector('track[kind="captions"]')).toHaveAttribute('src', '/media/flow-intro.en.vtt')
    expect(video.querySelector('source[type="video/mp4"]')).toHaveAttribute('src', '/media/flow-intro.mp4')
    expect(screen.getByRole('group', { name: 'Welcome video' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Play (keyboard shortcut k)' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Mute (keyboard shortcut m)' })).toBeInTheDocument()
    expect(screen.getByRole('slider', { name: 'Seek' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: /Change playback rate/ })).toHaveValue('1')
    expect(screen.getByRole('button', { name: 'Download' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Full window (keyboard shortcut f)' })).toBeInTheDocument()
    expect(screen.getByRole('timer')).toHaveTextContent('00:00')
  })
})
