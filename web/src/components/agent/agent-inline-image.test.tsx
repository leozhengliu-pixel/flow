import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'

const lightbox = vi.hoisted(() => ({ openLightbox: vi.fn(() => true) }))
vi.mock('@/components/editor/lightbox-bridge', () => lightbox)

import { AgentContentWithBlocks } from './agent-content-blocks'
import { splitAgentBlocks } from './agent-content-model'

describe('inline blocks in agent messages', () => {
  it('splits a message into its text and images, in order', () => {
    expect(splitAgentBlocks('![a.png](/uploads/a)\n\nWhat is this?\n![b](/uploads/b)')).toEqual([
      { type: 'image', alt: 'a.png', src: '/uploads/a' },
      { type: 'text', text: 'What is this?' },
      { type: 'image', alt: 'b', src: '/uploads/b' },
    ])
    expect(splitAgentBlocks('plain text')).toEqual([{ type: 'text', text: 'plain text' }])
  })

  it('shows a sent image that opens in the lightbox, with a view-only toolbar', async () => {
    const user = userEvent.setup()
    render(<I18nProvider><AgentContentWithBlocks content={'![shot.png](/uploads/shot.png)\n\nWhy?'} renderText={text => <p>{text}</p>}/></I18nProvider>)
    expect(screen.getByText('Why?')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Copy link' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
    await user.click(screen.getByRole('img', { name: 'shot.png' }))
    expect(lightbox.openLightbox).toHaveBeenCalledWith([{ src: '/uploads/shot.png', alt: 'shot.png' }])
  })

  it('splits images, videos and files in order', () => {
    expect(splitAgentBlocks('::file[spec.pdf](/uploads/spec.pdf){size="900" type="application/pdf"}\n\n::video[demo.mp4](/uploads/demo.mp4)\nSee both')).toEqual([
      { type: 'file', name: 'spec.pdf', src: '/uploads/spec.pdf', size: 900, contentType: 'application/pdf' },
      { type: 'video', name: 'demo.mp4', src: '/uploads/demo.mp4' },
      { type: 'text', text: 'See both' },
    ])
  })

  it('shows a sent file card with its size and read-only actions', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    render(<I18nProvider><AgentContentWithBlocks content={'::file[spec.pdf](/uploads/spec.pdf){size="1258291" type="application/pdf"}\n\nRead this'} renderText={text => <p>{text}</p>}/></I18nProvider>)
    const card = screen.getByRole('group', { name: 'spec.pdf' })
    expect(card).toHaveTextContent('1.2 MB')
    expect(screen.getByText('Read this')).toBeVisible()
    for (const name of ['View PDF', 'Download', 'Copy link', 'Print']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'View PDF' }))
    expect(open).toHaveBeenCalledWith('/uploads/spec.pdf', '_blank', 'noopener')
    open.mockRestore()
  })

  it('omits View PDF and Print for a file that is neither', () => {
    render(<I18nProvider><AgentContentWithBlocks content={'::file[data.zip](/uploads/data.zip){size="43" type="application/zip"}'} renderText={text => <p>{text}</p>}/></I18nProvider>)
    expect(screen.getByRole('group', { name: 'data.zip' })).toHaveTextContent('43 B')
    expect(screen.queryByRole('button', { name: 'View PDF' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Print' })).toBeNull()
  })

  it('shows a sent video in the custom player and wires its controls', () => {
    render(<I18nProvider><AgentContentWithBlocks content={'::video[demo.mp4](/uploads/demo.mp4)'} renderText={text => <p>{text}</p>}/></I18nProvider>)
    const video = screen.getByRole('group', { name: 'demo.mp4' }).querySelector('video')!
    expect(video).toHaveAttribute('preload', 'metadata')
    expect(video).not.toHaveAttribute('controls')
    const play = vi.spyOn(video, 'play').mockResolvedValue(undefined)
    fireEvent.click(screen.getByRole('button', { name: 'Play' }))
    expect(play).toHaveBeenCalled()
    fireEvent.change(screen.getByRole('combobox', { name: 'Playback speed' }), { target: { value: '1.5' } })
    expect(video.playbackRate).toBe(1.5)
    expect(screen.getByRole('slider', { name: 'Seek' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull()
  })

  it('keeps links that are neither uploads nor web addresses as text', () => {
    expect(splitAgentBlocks('see ::file[x](javascript:alert(1)) and ![y](data:image/png;base64,AA)')).toEqual([
      { type: 'text', text: 'see ::file[x](javascript:alert(1)) and ![y](data:image/png;base64,AA)' },
    ])
  })

  it('keeps keys pressed in the player away from the surrounding composer', () => {
    const onKeyDown = vi.fn()
    render(<I18nProvider><div onKeyDown={onKeyDown}><AgentContentWithBlocks content={'::video[demo.mp4](/uploads/demo.mp4)'} renderText={text => <p>{text}</p>}/></div></I18nProvider>)
    fireEvent.keyDown(screen.getByRole('combobox', { name: 'Playback speed' }), { key: 'Enter' })
    fireEvent.keyDown(screen.getByRole('group', { name: 'demo.mp4' }), { key: 'Enter' })
    expect(onKeyDown).not.toHaveBeenCalled()
  })

  it('leaves a message without images to the text renderer', () => {
    render(<I18nProvider><AgentContentWithBlocks content="Just text" renderText={text => <p data-testid="text">{text}</p>}/></I18nProvider>)
    expect(screen.getByTestId('text')).toHaveTextContent('Just text')
  })
})
