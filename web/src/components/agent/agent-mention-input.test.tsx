import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { I18nProvider } from '@/i18n/i18n'
import type { BootstrapData } from '@/types/flow'

vi.mock('@/lib/api', () => ({ listIssueRecords: vi.fn(() => new Promise(() => undefined)) }))

import { AgentMentionInput, type AgentMention, type AgentUpload } from './agent-mention-input'
import { fillAgentComposer } from './agent-composer-dom'
import { mentionIcon } from './agent-mention-icon'

const data = {
  workspace: { urlKey: 'ws' },
  users: [],
  issues: [],
  projects: [],
  documents: [],
  initiatives: [{ id: 'initiative-1', slugId: 'payments-q4', name: 'Payments Q4', color: '#f2c94c', icon: 'Initiative' }],
} as unknown as BootstrapData

describe('AgentMentionInput', () => {
  it('offers initiatives under @ and reports a chosen one as an initiative mention', async () => {
    const onChange = vi.fn<(value: string, mentions: AgentMention[]) => void>()
    const user = userEvent.setup()
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={onChange} onSubmit={vi.fn()} placeholder="Ask" value=""/></I18nProvider>)
    await user.click(screen.getByRole('textbox', { name: 'Ask' }))
    await user.keyboard('@Pay')
    const menu = await screen.findByRole('listbox', { name: 'Mention' })
    expect(menu).toHaveTextContent('Initiatives')
    await user.click(screen.getByRole('option', { name: /Payments Q4/ }))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.any(String), [{ type: 'initiative', id: 'initiative-1', label: 'Payments Q4' }]))
    expect(screen.getByRole('textbox', { name: 'Ask' })).toHaveTextContent('Payments Q4')
  })

  it('lists the exact issue identifier first, ahead of longer identifiers that contain it', async () => {
    const state = { id: 's', name: 'Backlog', color: '#999', type: 'backlog' }
    const issueData = { ...data, issues: ['206', '20', '200'].map(number => ({ id: `issue_${number}`, identifier: `DEV-${number}`, title: `Issue ${number}`, state })) } as unknown as BootstrapData
    const user = userEvent.setup()
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={issueData} onChange={vi.fn()} onSubmit={vi.fn()} placeholder="Ask" value=""/></I18nProvider>)
    await user.click(screen.getByRole('textbox', { name: 'Ask' }))
    await user.keyboard('@DEV-20')
    await screen.findByRole('listbox', { name: 'Mention' })
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual([expect.stringContaining('DEV-20Issue 20'), expect.stringContaining('DEV-206'), expect.stringContaining('DEV-200')])
  })

  const upload = (file: File, n = 1) => ({ name: file.name, contentType: file.type, size: file.size, url: `/uploads/agent_upload_0123456789abcdef_${n}_${file.name}` })

  it('hands a pasted screenshot to the host as a file when the composer cannot upload, and keeps text pastes as text', () => {
    const onPasteFiles = vi.fn<(files: File[]) => void>()
    const insertText = vi.fn(() => true)
    document.execCommand = insertText
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={vi.fn()} onPasteFiles={onPasteFiles} onSubmit={vi.fn()} placeholder="Ask" value=""/></I18nProvider>)
    const box = screen.getByRole('textbox', { name: 'Ask' })
    const image = new File(['png'], 'image.png', { type: 'image/png' })
    fireEvent.paste(box, { clipboardData: { files: [image], getData: () => '' } })
    expect(onPasteFiles).toHaveBeenCalledWith([image])
    expect(insertText).not.toHaveBeenCalled()
    fireEvent.paste(box, { clipboardData: { files: [], getData: () => 'hello' } })
    expect(insertText).toHaveBeenCalledWith('insertText', false, 'hello')
    expect(onPasteFiles).toHaveBeenCalledTimes(1)
  })

  it('puts a pasted screenshot into the message as an uploaded image block', async () => {
    const onChange = vi.fn<(value: string, mentions: AgentMention[]) => void>()
    const onBusyChange = vi.fn<(busy: boolean) => void>()
    let finish: (value: AgentUpload) => void = () => undefined
    const onUploadFile = vi.fn(() => new Promise<AgentUpload>(resolve => { finish = resolve }))
    URL.createObjectURL = vi.fn(() => 'blob:preview')
    URL.revokeObjectURL = vi.fn()
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onBusyChange={onBusyChange} onChange={onChange} onSubmit={vi.fn()} onUploadFile={onUploadFile} placeholder="Ask" value=""/></I18nProvider>)
    const box = screen.getByRole('textbox', { name: 'Ask' })
    const screenshot = new File(['png'], 'image.png', { type: 'image/png' })
    fireEvent.paste(box, { clipboardData: { files: [screenshot], getData: () => '' } })
    const block = box.querySelector<HTMLElement>('[data-agent-block]')!
    expect(block).toHaveAttribute('contenteditable', 'false')
    expect(block).toHaveAttribute('data-kind', 'image')
    expect(block.querySelector('img')).toHaveAttribute('src', 'blob:preview')
    await waitFor(() => expect(onUploadFile).toHaveBeenCalledWith(screenshot))
    expect(onBusyChange).toHaveBeenLastCalledWith(true)
    finish(upload(screenshot))
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(expect.stringContaining('![image.png](/uploads/agent_upload_0123456789abcdef_1_image.png)'), []))
    await waitFor(() => expect(onBusyChange).toHaveBeenLastCalledWith(false))
    expect(block.querySelector('img')).toHaveAttribute('src', '/uploads/agent_upload_0123456789abcdef_1_image.png')
    // The toolbar's delete takes the image (and its empty line) back out.
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    expect(box.querySelector('[data-agent-block]')).toBeNull()
    expect(onChange).toHaveBeenLastCalledWith('', [])
  })

  it('turns a pasted PDF, video and SVG into blocks and ignores audio', async () => {
    const onChange = vi.fn<(value: string, mentions: AgentMention[]) => void>()
    const onUploadFile = vi.fn(async (file: File) => upload(file))
    URL.createObjectURL = vi.fn(() => 'blob:preview')
    URL.revokeObjectURL = vi.fn()
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={onChange} onSubmit={vi.fn()} onUploadFile={onUploadFile} placeholder="Ask" value=""/></I18nProvider>)
    const box = screen.getByRole('textbox', { name: 'Ask' })
    const pdf = new File(['pdf'], 'spec.pdf', { type: 'application/pdf' })
    const video = new File(['mp4'], 'demo.mp4', { type: 'video/mp4' })
    const svg = new File(['<svg/>'], 'logo.svg', { type: 'image/svg+xml' })
    const song = new File(['mp3'], 'song.mp3', { type: 'audio/mpeg' })
    const noType = new File(['wav'], 'clip.wav', { type: '' })
    fireEvent.paste(box, { clipboardData: { files: [pdf, video, svg, song, noType], getData: () => '' } })
    expect([...box.querySelectorAll<HTMLElement>('[data-agent-block]')].map(block => block.dataset.kind)).toEqual(['file', 'video', 'image'])
    await waitFor(() => expect(onUploadFile).toHaveBeenCalledTimes(3))
    expect(onUploadFile.mock.calls.map(([file]) => file.name)).toEqual(['spec.pdf', 'demo.mp4', 'logo.svg'])
    await waitFor(() => expect(onChange).toHaveBeenLastCalledWith(
      '\n::file[spec.pdf](/uploads/agent_upload_0123456789abcdef_1_spec.pdf){size="3" type="application/pdf"}\n\n::video[demo.mp4](/uploads/agent_upload_0123456789abcdef_1_demo.mp4)\n\n![logo.svg](/uploads/agent_upload_0123456789abcdef_1_logo.svg)\n\n',
      [],
    ))
    // The card shows the name and size; the video has the custom player.
    expect(within(box).getByRole('group', { name: 'spec.pdf' })).toHaveTextContent('3 B')
    expect(within(box).getByRole('button', { name: 'View PDF' })).toBeInTheDocument()
    expect(within(box).getByRole('group', { name: 'demo.mp4' }).querySelector('video')).toHaveAttribute('preload', 'metadata')
    expect(within(box).getByRole('button', { name: 'Play' })).toBeInTheDocument()
    // Blocks stack directly, with one line to type on after them.
    expect(box.children).toHaveLength(4)
    // Audio alone inserts nothing, not even text.
    document.execCommand = vi.fn(() => true)
    fireEvent.paste(box, { clipboardData: { files: [song], getData: () => '' } })
    expect(box.querySelectorAll('[data-agent-block]')).toHaveLength(3)
    expect(document.execCommand).not.toHaveBeenCalled()
  })

  it('removes a block whose upload fails and rejects files over the limit', async () => {
    const onUploadFile = vi.fn(async () => { throw new Error('Upload failed') })
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={vi.fn()} onSubmit={vi.fn()} onUploadFile={onUploadFile} placeholder="Ask" value=""/></I18nProvider>)
    const box = screen.getByRole('textbox', { name: 'Ask' })
    fireEvent.paste(box, { clipboardData: { files: [new File(['x'], 'a.zip', { type: 'application/zip' })], getData: () => '' } })
    expect(box.querySelector('[data-agent-block]')).not.toBeNull()
    await waitFor(() => expect(box.querySelector('[data-agent-block]')).toBeNull())
    const huge = new File(['x'], 'big.zip', { type: 'application/zip' })
    Object.defineProperty(huge, 'size', { value: 21 * 1024 * 1024 })
    onUploadFile.mockClear()
    fireEvent.paste(box, { clipboardData: { files: [huge], getData: () => '' } })
    await waitFor(() => expect(box.querySelector('[data-agent-block]')).toBeNull())
    expect(onUploadFile).not.toHaveBeenCalled()
  })

  it('keeps at most 8 blocks in a message', async () => {
    const onUploadFile = vi.fn(() => new Promise<AgentUpload>(() => undefined))
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={vi.fn()} onSubmit={vi.fn()} onUploadFile={onUploadFile} placeholder="Ask" value=""/></I18nProvider>)
    const box = screen.getByRole('textbox', { name: 'Ask' })
    const files = Array.from({ length: 10 }, (_, index) => new File(['x'], `f${index}.txt`, { type: 'text/plain' }))
    fireEvent.paste(box, { clipboardData: { files, getData: () => '' } })
    expect(box.querySelectorAll('[data-agent-block]')).toHaveLength(8)
  })

  it('writes a message with images, videos and files back as blocks (editing, drafts)', async () => {
    const onChange = vi.fn<(value: string, mentions: AgentMention[]) => void>()
    render(<I18nProvider><AgentMentionInput ariaLabel="Ask" data={data} onChange={onChange} onSubmit={vi.fn()} onUploadFile={vi.fn()} placeholder="Ask" value=""/></I18nProvider>)
    const box = screen.getByRole('textbox', { name: 'Ask' })
    const message = '\n![shot.png](/uploads/agent_upload_0123456789abcdef_2_shot.png)\n\n::video[demo.mp4](/uploads/agent_upload_0123456789abcdef_3_demo.mp4)\n\n::file[spec.pdf](/uploads/agent_upload_0123456789abcdef_4_spec.pdf){size="2048" type="application/pdf"}\n\nWhy is this red?'
    fillAgentComposer(box, message)
    await waitFor(() => expect(box.querySelector('[data-kind="image"] img')).toHaveAttribute('src', '/uploads/agent_upload_0123456789abcdef_2_shot.png'))
    expect(box.querySelector('[data-kind="video"] video')).toHaveAttribute('src', '/uploads/agent_upload_0123456789abcdef_3_demo.mp4')
    expect(within(box).getByRole('group', { name: 'spec.pdf' })).toHaveTextContent('2 KB')
    expect(onChange).toHaveBeenLastCalledWith(message, [])
  })

  it('draws an initiative chip with its glyph', () => {
    const { container } = render(<>{mentionIcon({ type: 'initiative', id: 'initiative-1' }, data)}</>)
    expect(container.querySelector('svg')).not.toBeNull()
  })
})
