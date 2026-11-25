import { Editor } from '@tiptap/core'
import StarterKit from '@tiptap/starter-kit'
import { Markdown } from '@tiptap/markdown'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { DescriptionFile, DescriptionVideo, insertEmbedFiles } from './file-extension'
import { DescriptionImage, attachmentInBody, descriptionImageSrcs } from './image-extension'
import { encodeLegacyAttachmentDirectives } from './legacy-attachment-directives'

const DOCX = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
const spaced = { url: '/uploads/attachment_a_测试 report.docx', title: '测试 report.docx' }
const parens = { url: '/uploads/attachment_b_report (1).docx', title: 'report (1).docx' }
const plain = { url: '/uploads/attachment_c_测试报告.docx', title: '测试报告.docx' }
// What the server's attachmentEmbedMarkdown now writes.
const encodedDirective = (item: { url: string; title: string }) => `::file[${item.title}](${item.url.replaceAll(' ', '%20').replaceAll('(', '%28').replaceAll(')', '%29')}){size="12" type="${DOCX}"}`
const rawDirective = (item: { url: string; title: string }) => `::file[${item.title}](${item.url}){size="12" type="${DOCX}"}`

describe('attachment directive URLs in the description', () => {
  let editor: Editor | undefined
  afterEach(() => { editor?.destroy(); editor = undefined })

  const load = (content: string) => {
    editor = new Editor({ extensions: [StarterKit, Markdown, DescriptionFile, DescriptionVideo, DescriptionImage], content, contentType: 'markdown' })
    return editor
  }
  const files = (target: Editor) => (target.getJSON().content ?? []).filter(node => node.type === 'file').map(node => node.attrs)

  it('keeps refusing a raw space in the URL but reads the encoded form, with the original display name', () => {
    expect(files(load(rawDirective(spaced)))).toEqual([])
    expect(files(load(encodedDirective(spaced)))).toEqual([{ src: '/uploads/attachment_a_测试%20report.docx', title: '测试 report.docx', size: 12, contentType: DOCX }])
  })

  it.each([
    ['space first', [spaced, plain, parens]],
    ['space last', [plain, parens, spaced]],
  ])('reads several file cards in one body (%s) and writes them back byte for byte', (_label, items) => {
    const markdown = items.map(encodedDirective).join('\n\n')
    const first = load(markdown)
    expect(files(first).map(attrs => attrs?.title)).toEqual(items.map(item => item.title))
    const saved = first.getMarkdown()
    expect(saved).toBe(markdown)
    // Refresh and re-edit: the saved Markdown parses to the same cards and serializes identically.
    const reloaded = load(saved)
    expect(files(reloaded)).toEqual(files(first))
    expect(reloaded.getMarkdown()).toBe(saved)
  })

  it('writes the encoded URL for a node that still holds a raw src (rich-text state saved before the fix)', () => {
    const target = load('')
    target.commands.setContent({ type: 'doc', content: [
      { type: 'file', attrs: { src: spaced.url, title: spaced.title, size: 12, contentType: DOCX } },
      { type: 'video', attrs: { src: '/uploads/attachment_d_my clip.mp4', title: 'my clip.mp4' } },
      { type: 'image', attrs: { src: '/uploads/attachment_e_shot (2).png', alt: 'shot (2).png' } },
    ] })
    const markdown = target.getMarkdown()
    expect(markdown).toContain(encodedDirective(spaced))
    expect(markdown).toContain('::video[my clip.mp4](/uploads/attachment_d_my%20clip.mp4)')
    expect(markdown).toContain('![shot (2).png](/uploads/attachment_e_shot%20%282%29.png)')
    const reloaded = load(markdown)
    // (The parser may add an empty trailing paragraph after a final block atom.)
    expect((reloaded.getJSON().content ?? []).filter(node => node.type !== 'paragraph' || node.content?.length).map(node => node.type)).toEqual(['file', 'video', 'image'])
    expect(reloaded.getMarkdown()).toBe(markdown)
  })

  it('stores the uploaded URL in its directive form on a card inserted in the editor', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const target = new Editor({ element: host, extensions: [StarterKit, Markdown, DescriptionFile], content: '<p></p>' })
    editor = target
    const original = URL.createObjectURL
    URL.createObjectURL = () => 'blob:test/0'
    const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => undefined)
    const upload = vi.fn(async () => spaced.url)
    try {
      insertEmbedFiles(target.view, [new File(['x'], spaced.title, { type: DOCX })], upload)
      await vi.waitFor(() => expect(files(target)[0]?.src).toBe('/uploads/attachment_a_测试%20report.docx'))
    } finally {
      URL.createObjectURL = original
      revoke.mockRestore()
    }
    expect(files(target)[0]?.title).toBe('测试 report.docx')
    expect(target.getMarkdown()).toContain('::file[测试 report.docx](/uploads/attachment_a_测试%20report.docx)')
    host.remove()
  })

  it('matches encoded references to the raw attachment URLs', () => {
    const srcs = descriptionImageSrcs([spaced, parens].map(encodedDirective).join('\n\n'))
    expect(attachmentInBody(srcs, spaced.url)).toBe(true)
    expect(attachmentInBody(srcs, parens.url)).toBe(true)
    expect(attachmentInBody(srcs, plain.url)).toBe(false)
  })
})

describe('legacy attachment directives', () => {
  const attachments = [spaced, parens, plain]

  it('encodes raw URLs of the issue\'s own attachments so the body reads as file cards', () => {
    const body = `Intro\n\n${rawDirective(spaced)}\n\n${rawDirective(plain)}\n\n${rawDirective(parens)}\n`
    const repaired = encodeLegacyAttachmentDirectives(body, attachments)
    expect(repaired).toBe(`Intro\n\n${encodedDirective(spaced)}\n\n${rawDirective(plain)}\n\n${encodedDirective(parens)}\n`)
    const editor = new Editor({ extensions: [StarterKit, Markdown, DescriptionFile], content: repaired, contentType: 'markdown' })
    expect((editor.getJSON().content ?? []).filter(node => node.type === 'file').map(node => node.attrs?.title)).toEqual([spaced.title, plain.title, parens.title])
    editor.destroy()
    expect(encodeLegacyAttachmentDirectives(repaired, attachments)).toBe(repaired)
  })

  it('leaves everything that is not a full-line directive of a known attachment alone', () => {
    const untouched = [
      rawDirective({ url: '/uploads/attachment_z_other file.docx', title: 'other file.docx' }),
      `See ${rawDirective(spaced)} inline`,
      '```\n' + rawDirective(spaced) + '\n```',
      `::file\\[${spaced.title}\\](${spaced.url}){size="12" type="${DOCX}"}`,
      `[${spaced.title}](${spaced.url})`,
    ]
    for (const body of untouched) expect(encodeLegacyAttachmentDirectives(body, attachments)).toBe(body)
    expect(encodeLegacyAttachmentDirectives(rawDirective(spaced), [])).toBe(rawDirective(spaced))
  })
})
