import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const error = vi.hoisted(() => vi.fn())
vi.mock('@/lib/toast', () => ({ toast: { error } }))

import { AGENT_ATTACHMENT_MAX_BYTES, addAgentAttachments, attachAgentFiles, pastedAgentFiles, shrinkAgentImage } from './agent-attachments'

const MB = 1024 * 1024
const file = (name: string, type: string, size: number) => new File([new Uint8Array(size)], name, { type })

describe('pasted agent attachments', () => {
  const clipboard = (files: File[], types: Record<string, string> = {}) => ({ files, getData: (type: string) => types[type] ?? '' }) as unknown as DataTransfer

  it('returns pasted files and nothing for a text paste', () => {
    const image = file('image.png', 'image/png', 3)
    expect(pastedAgentFiles(clipboard([image]))).toEqual([image])
    expect(pastedAgentFiles(clipboard([]))).toEqual([])
    expect(pastedAgentFiles(null)).toEqual([])
  })

  it('reads an Office selection (text, html and a picture of it) as text, but a copied file as a file', () => {
    const picture = file('image.png', 'image/png', 3)
    expect(pastedAgentFiles(clipboard([picture], { 'text/plain': 'a\tb', 'text/html': '<table></table>' }))).toEqual([])
    expect(pastedAgentFiles(clipboard([picture], { 'text/html': '<img src="x">' }))).toEqual([picture])
    expect(pastedAgentFiles(clipboard([picture], { 'text/plain': 'image.png' }))).toEqual([picture])
  })
})

describe('shrinking large images', () => {
  let encoded: Array<{ type: string; quality?: number }>
  let sizes: Record<string, number>
  let drawn: { width: number; height: number } | undefined

  beforeEach(() => {
    encoded = []
    sizes = {}
    drawn = undefined
    error.mockReset()
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 5120, height: 2880, close: vi.fn() })))
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(function (this: HTMLCanvasElement) {
      return { drawImage: () => { drawn = { width: this.width, height: this.height } } } as unknown as CanvasRenderingContext2D
    } as never)
    vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation((callback, type = 'image/png', quality) => {
      encoded.push({ type, quality })
      callback(new Blob([new Uint8Array(sizes[`${type}:${quality ?? ''}`] ?? 100)], { type }))
    })
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('scales an oversized screenshot to 2048 px and keeps it a PNG when that fits', async () => {
    const shrunk = await shrinkAgentImage(file('Screenshot.png', 'image/png', 6 * MB))
    expect(drawn).toEqual({ width: 2048, height: 1152 })
    expect(shrunk).toMatchObject({ name: 'Screenshot.png', type: 'image/png', size: 100 })
  })

  it('falls back to JPEG when the scaled PNG is still too large', async () => {
    sizes['image/png:'] = 3 * MB
    const shrunk = await shrinkAgentImage(file('photo.png', 'image/png', 9 * MB))
    expect(encoded).toEqual([{ type: 'image/png', quality: undefined }, { type: 'image/jpeg', quality: 0.85 }])
    expect(shrunk).toMatchObject({ name: 'photo.jpg', type: 'image/jpeg', size: 100 })
  })

  it('leaves small images, GIFs and other files alone', async () => {
    vi.stubGlobal('createImageBitmap', vi.fn(async () => ({ width: 800, height: 600, close: vi.fn() })))
    const small = file('small.png', 'image/png', 200_000)
    const gif = file('anim.gif', 'image/gif', 5 * MB)
    const pdf = file('spec.pdf', 'application/pdf', 5 * MB)
    expect(await shrinkAgentImage(small)).toBe(small)
    expect(await shrinkAgentImage(gif)).toBe(gif)
    expect(await shrinkAgentImage(pdf)).toBe(pdf)
    expect(encoded).toEqual([])
  })

  it('attaches the shrunk screenshot and reports files still over the limit', async () => {
    let list: File[] = []
    const setFiles = (update: File[] | ((items: File[]) => File[])) => { list = typeof update === 'function' ? update(list) : update }
    await attachAgentFiles([file('Screenshot.png', 'image/png', 6 * MB), file('spec.pdf', 'application/pdf', 3 * MB)], text => `t:${text}`, setFiles)
    expect(list.map(item => [item.name, item.size <= AGENT_ATTACHMENT_MAX_BYTES])).toEqual([['Screenshot.png', true]])
    expect(error).toHaveBeenCalledWith("t:Files over 2 MB can't be attached")
  })
})

describe('attachment limits per message', () => {
  it('keeps at most 8 files and 10 MB in all', () => {
    const files = Array.from({ length: 6 }, (_, index) => file(`f${index}.txt`, 'text/plain', 2 * MB - 1))
    const kept = addAgentAttachments([], files)
    expect(kept.map(item => item.name)).toEqual(['f0.txt', 'f1.txt', 'f2.txt', 'f3.txt', 'f4.txt'])
    const small = Array.from({ length: 10 }, (_, index) => file(`s${index}.txt`, 'text/plain', 10))
    expect(addAgentAttachments([], small)).toHaveLength(8)
  })
})
