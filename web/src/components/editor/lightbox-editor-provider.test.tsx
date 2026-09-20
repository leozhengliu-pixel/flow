import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import { useRef } from 'react'
import { LightboxEditorProvider, useLightboxEditor } from './lightbox-editor-provider'
import { openLightbox, isLightboxRegistered } from './lightbox-bridge'
import { isAnyCommentInHash, isCommentIdInHash } from '@/hooks/use-comment-hash-popover'
import { useWindowControlsInsets, WindowControlsAwareLightboxProvider } from './window-controls-aware-lightbox-provider'

afterEach(() => {
  cleanup()
  window.location.hash = ''
})

function OpenButton() {
  const lightbox = useLightboxEditor()
  return (
    <button
      type="button"
      onClick={() => lightbox.open([
        { src: '/a.png', alt: 'A' },
        { src: '/b.png', alt: 'B' },
      ], 0)}
    >
      Open lightbox
    </button>
  )
}

function InsetsProbe() {
  const insets = useWindowControlsInsets()
  return <span data-testid="insets">{`${insets.left},${insets.right}`}</span>
}

describe('LightboxEditorProvider (LS-0382)', () => {
  it('registers the imperative bridge and opens a multi-image portal', async () => {
    const user = userEvent.setup()
    render(
      <LightboxEditorProvider>
        <OpenButton />
      </LightboxEditorProvider>,
    )
    expect(isLightboxRegistered()).toBe(true)
    await user.click(screen.getByRole('button', { name: 'Open lightbox' }))
    expect(await screen.findByRole('dialog', { name: 'View image' })).toBeVisible()
    expect(screen.getByAltText('A')).toBeVisible()
    expect(screen.getByText('1 / 2')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Next image' }))
    expect(screen.getByAltText('B')).toBeVisible()
    expect(screen.getByText('2 / 2')).toBeVisible()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog', { name: 'View image' })).toBeNull()
  })

  it('opens via lightbox-bridge openLightbox', async () => {
    render(
      <LightboxEditorProvider>
        <span>host</span>
      </LightboxEditorProvider>,
    )
    expect(openLightbox([{ src: '/c.png', alt: 'C' }])).toBe(true)
    expect(await screen.findByAltText('C')).toBeVisible()
  })

  it('exposes editorInstanceRef and updates lightboxIndex on item change', async () => {
    const user = userEvent.setup()
    function Host() {
      const ref = useRef<{ lightboxIndex?: number } | null>(null)
      return (
        <LightboxEditorProvider editorInstanceRef={ref}>
          <OpenButton />
          <button type="button" onClick={() => {
            const value = ref.current as { lightboxIndex?: number } | null
            document.body.dataset.index = String(value?.lightboxIndex ?? 'none')
          }}>Read ref</button>
        </LightboxEditorProvider>
      )
    }
    render(<Host />)
    await user.click(screen.getByRole('button', { name: 'Open lightbox' }))
    await user.click(screen.getByRole('button', { name: 'Next image' }))
    await user.click(screen.getByRole('button', { name: 'Read ref' }))
    expect(document.body.dataset.index).toBe('1')
  })

  it('shows CommentPopover when hash is #comment-{id}', async () => {
    window.location.hash = '#comment-abc'
    render(
      <LightboxEditorProvider
        resolveComment={id => ({ id, body: 'Hello from hash', authorName: 'Ada' })}
      >
        <span>host</span>
      </LightboxEditorProvider>,
    )
    expect(await screen.findByRole('dialog', { name: 'Comment' })).toBeVisible()
    expect(screen.getByText('Hello from hash')).toBeVisible()
    expect(screen.getByText(/Comment from/)).toBeVisible()
  })
})

describe('comment hash helpers', () => {
  it('detects comment ids in hash', () => {
    expect(isAnyCommentInHash('#comment-xyz')).toBe(true)
    expect(isCommentIdInHash('#comment-xyz', 'xyz')).toBe(true)
    expect(isCommentIdInHash('#comment-xyz', 'other')).toBe(false)
    expect(isAnyCommentInHash('#activity-1')).toBe(false)
  })
})

describe('WindowControlsAwareLightboxProvider (LS-0654 stub)', () => {
  it('defaults insets to 0,0 for web', () => {
    render(
      <WindowControlsAwareLightboxProvider>
        <InsetsProbe />
      </WindowControlsAwareLightboxProvider>,
    )
    expect(screen.getByTestId('insets')).toHaveTextContent('0,0')
  })
})
