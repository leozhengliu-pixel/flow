import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FilePreviewCard } from './file-preview-card'
import { highlightedLines, resetFilePreviewCache, segmentLines, type FilePreview } from './file-preview'

vi.mock('@/i18n/i18n', () => ({ useI18n: () => ({ t: (value: string) => value }) }))

const SRC = 'https://github.com/golang/go/blob/master/README.md#L1-L3'

function preview(overrides: Partial<FilePreview> = {}): FilePreview {
  return { provider: 'github', repo: 'golang/go', path: 'README.md', ref: 'master', language: 'markdown', lines: ['# The Go Programming Language', '', 'Go is an open source language'], startLine: 1, endLine: 3, totalLines: 40, truncated: false, htmlUrl: SRC, ...overrides }
}

function respond(status: number, body: unknown) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

let fetchMock: ReturnType<typeof vi.fn>

beforeEach(() => {
  resetFilePreviewCache()
  fetchMock = vi.fn()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const card = () => document.querySelector('[data-file-preview]') as HTMLElement

describe('FilePreviewCard', () => {
  it('shows the header from the link and a loading state while the file loads', async () => {
    let resolve: (value: Response) => void = () => {}
    fetchMock.mockReturnValue(new Promise<Response>(next => { resolve = next }))
    render(<FilePreviewCard provider="github" src={SRC}/>)
    expect(card()).toHaveAttribute('data-file-preview', 'loading')
    expect(screen.getByRole('status')).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByText('Loading file preview…')).toBeInTheDocument()
    expect(card()).toHaveTextContent('README.md')
    expect(card()).toHaveTextContent('golang/go')
    expect(card()).toHaveTextContent('master')
    expect(card()).toHaveTextContent('L1-L3')
    expect(screen.getByRole('link', { name: /Open in GitHub/ })).toHaveAttribute('href', SRC)
    resolve(respond(200, preview()))
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'ready'))
  })

  it('renders the linked range with line numbers and syntax highlighting', async () => {
    fetchMock.mockResolvedValue(respond(200, preview({ path: 'src/main.go', language: 'go', lines: ['func main() {', '\treturn', '}'], startLine: 10, endLine: 12, htmlUrl: 'https://github.com/golang/go/blob/master/src/main.go#L10-L12' })))
    render(<FilePreviewCard provider="github" src="https://github.com/golang/go/blob/master/src/main.go#L10-L12"/>)
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'ready'))
    expect([...card().querySelectorAll('.flow-code-block__gutter span')].map(node => node.textContent)).toEqual(['10', '11', '12'])
    expect(card().querySelector('.flow-code-block__code')?.textContent).toBe('func main() {\n\treturn\n}')
    expect(card().querySelector('.hljs-keyword')?.textContent).toBe('func')
    expect(card()).toHaveTextContent('L10-L12')
    expect(card()).not.toHaveAttribute('data-collapsed')
    expect(screen.queryByText(/Preview truncated/)).toBeNull()
  })

  it('collapses long previews and notes when the file was truncated', async () => {
    const lines = Array.from({ length: 1000 }, (_, index) => `line ${index + 1}`)
    fetchMock.mockResolvedValue(respond(200, preview({ language: 'plaintext', lines, startLine: 1, endLine: 1000, totalLines: 5000, truncated: true, htmlUrl: 'https://github.com/golang/go/blob/master/README.md' })))
    render(<FilePreviewCard provider="github" src="https://github.com/golang/go/blob/master/README.md"/>)
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'ready'))
    expect(card()).toHaveAttribute('data-collapsed')
    expect(screen.getByText('Preview truncated. Open the file on GitHub to see all of it.')).toBeInTheDocument()
    expect(card()).not.toHaveTextContent('L1-L')
    fireEvent.click(screen.getByRole('button', { name: 'Show all 1000 lines' }))
    expect(card()).not.toHaveAttribute('data-collapsed')
    fireEvent.click(screen.getByRole('button', { name: 'Collapse' }))
    expect(card()).toHaveAttribute('data-collapsed')
  })

  it('asks to connect GitHub for a private file without the integration', async () => {
    window.history.pushState({}, '', '/dev-workspace/document/abc')
    fetchMock.mockResolvedValue(respond(404, { error: 'Connect the repository integration to preview this file', code: 'not_connected' }))
    render(<FilePreviewCard provider="github" src={SRC}/>)
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'not_connected'))
    expect(screen.getByText('Connect GitHub to preview this file')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Connect GitHub' })).toHaveAttribute('href', '/dev-workspace/settings/integrations/github')
    expect(screen.getByRole('link', { name: /Open in GitHub/ })).toHaveAttribute('href', SRC)
  })

  it('shows "No access" when the connected integration cannot read the file (GitLab)', async () => {
    fetchMock.mockResolvedValue(respond(404, { error: 'no', code: 'no_access' }))
    render(<FilePreviewCard provider="gitlab" src="https://gitlab.com/group/proj/-/blob/main/app.rb#L2-4"/>)
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'no_access'))
    expect(screen.getByText('No access to this file')).toBeInTheDocument()
    expect(screen.getByText('The file does not exist, or the connected GitLab integration cannot read it.')).toBeInTheDocument()
    expect(card()).toHaveTextContent('group/proj')
    expect(card()).toHaveTextContent('L2-L4')
    expect(screen.getByRole('link', { name: /Open in GitLab/ })).toBeInTheDocument()
  })

  it('shows an error with Retry, and loads again when retried', async () => {
    fetchMock.mockResolvedValueOnce(respond(502, { error: 'bad gateway', code: 'upstream' })).mockResolvedValueOnce(respond(200, preview()))
    render(<FilePreviewCard provider="github" src={SRC}/>)
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'upstream'))
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load the file preview')
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'ready'))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('maps size and type failures to their own messages', async () => {
    fetchMock.mockResolvedValue(respond(413, { error: 'too large', code: 'too_large' }))
    render(<FilePreviewCard provider="github" src="https://github.com/a/b/blob/main/big.json"/>)
    await waitFor(() => expect(card()).toHaveAttribute('data-file-preview', 'too_large'))
    expect(screen.getByText('This file is too large to preview')).toBeInTheDocument()
  })

  it('asks the server once for the same link', async () => {
    fetchMock.mockResolvedValue(respond(200, preview()))
    render(<><FilePreviewCard provider="github" src={SRC}/><FilePreviewCard provider="github" src={SRC}/></>)
    await waitFor(() => expect(document.querySelectorAll('[data-file-preview="ready"]')).toHaveLength(2))
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('segmentLines', () => {
  it('splits nested token ranges into per-line segments with every enclosing class', () => {
    const text = 'ab\ncd'
    const lines = segmentLines(text, [{ from: 0, to: 4, className: 'outer' }, { from: 1, to: 2, className: 'inner' }])
    expect(lines).toEqual([
      [{ text: 'a', className: 'outer' }, { text: 'b', className: 'outer inner' }],
      [{ text: 'c', className: 'outer' }, { text: 'd' }],
    ])
  })

  it('keeps empty lines so numbering stays aligned', () => {
    expect(highlightedLines(['one', '', 'three'], 'plaintext').map(line => line.map(part => part.text).join(''))).toEqual(['one', '', 'three'])
  })
})
