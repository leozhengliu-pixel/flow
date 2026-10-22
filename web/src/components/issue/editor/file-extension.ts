import { Node, mergeAttributes } from '@tiptap/core'
import type { EditorView } from '@tiptap/pm/view'
import { formatFileSize } from '@/lib/format-file-size'

type DescriptionImageUpload = (file: File) => Promise<string>

export const DescriptionFile = Node.create({
  name: 'file',
  group: 'block',
  atom: true,
  draggable: true,
  selectable: true,
  addAttributes() {
    return {
      src: { default: null },
      title: { default: null },
      size: { default: null },
      contentType: { default: null },
    }
  },
  parseHTML() {
    return [{ tag: 'a[data-description-file]', getAttrs: element => {
      const node = element as HTMLAnchorElement
      return { src: node.getAttribute('href'), title: node.dataset.title || node.textContent, size: node.dataset.size, contentType: node.dataset.type }
    } }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['a', mergeAttributes(HTMLAttributes, { class: 'description-file', 'data-description-file': '', href: HTMLAttributes.src, 'data-title': HTMLAttributes.title, 'data-size': HTMLAttributes.size, 'data-type': HTMLAttributes.contentType }), String(HTMLAttributes.title || 'File')]
  },
  addNodeView() {
    return ({ node }) => {
      const dom = document.createElement('div')
      dom.className = 'description-file-node'
      dom.contentEditable = 'false'
      const link = document.createElement('div')
      link.className = 'description-file'
      const icon = document.createElement('div')
      icon.className = 'description-file-icon'
      const copy = document.createElement('div')
      copy.className = 'description-file-copy'
      const name = document.createElement('span')
      name.className = 'description-file-name'
      const meta = document.createElement('span')
      meta.className = 'description-file-meta'
      copy.append(name, meta)
      link.append(icon, copy)
      const download = document.createElement('button')
      download.type = 'button'
      download.className = 'description-file-download'
      download.setAttribute('aria-label', 'Download')
      download.title = 'Download'
      download.innerHTML = downloadGlyph()
      download.addEventListener('mousedown', event => event.preventDefault())
      let href = ''
      let fileName = 'File'
      download.addEventListener('click', event => {
        event.preventDefault()
        event.stopPropagation()
        const anchor = document.createElement('a')
        anchor.href = href
        anchor.download = fileName
        anchor.rel = 'noreferrer'
        anchor.click()
      })
      const row = document.createElement('div')
      row.className = 'description-file-row'
      row.append(link, download)
      dom.append(row)
      const preview = createPdfPreview()
      const apply = (attrs: Record<string, unknown>) => {
        const title = String(attrs.title || 'File')
        const contentType = String(attrs.contentType ?? '')
        name.textContent = title
        meta.textContent = attrs.size == null ? '' : formatFileSize(Number(attrs.size) || 0)
        meta.hidden = !meta.textContent
        icon.innerHTML = fileGlyph(contentType, title)
        href = String(attrs.src ?? '')
        fileName = title
        const pdf = isPdf(contentType, title)
        dom.classList.toggle('has-preview', pdf)
        if (pdf) {
          if (!preview.dom.isConnected) dom.append(preview.dom)
          preview.show(href, Number(attrs.size) || 0)
        } else {
          preview.dom.remove()
          preview.show('', 0)
        }
      }
      apply(node.attrs)
      return {
        dom,
        update(updated) {
          if (updated.type.name !== 'file') return false
          apply(updated.attrs)
          return true
        },
        destroy() { preview.destroy() },
      }
    }
  },
  markdownTokenName: 'descriptionFile',
  parseMarkdown: (token, helpers) => helpers.createNode('file', { src: String(token.href ?? ''), title: String(token.text ?? 'File'), size: token.size ? Number(token.size) : null, contentType: token.contentType ? String(token.contentType) : null }),
  renderMarkdown: node => `::file[${String(node.attrs?.title || 'File')}](${String(node.attrs?.src || '')}){size="${node.attrs?.size ?? ''}" type="${node.attrs?.contentType ?? ''}"}`,
  markdownTokenizer: {
    name: 'descriptionFile',
    level: 'block' as const,
    start: (src: string) => src.startsWith('::file[') ? 0 : -1,
    tokenize(src: string) {
      const match = src.match(/^::file\[([^\]]*)]\(([^)\s]+)\)(?:\{size="([^"]*)" type="([^"]*)"\})?[ \t]*\n?/)
      if (!match) return undefined
      return { type: 'descriptionFile', raw: match[0], text: match[1], href: match[2], size: match[3], contentType: match[4] }
    },
  },
})

export const DescriptionVideo = Node.create({
  name: 'video',
  group: 'block',
  atom: true,
  draggable: true,
  selectable: true,
  addAttributes() {
    return { src: { default: null }, title: { default: null } }
  },
  parseHTML() {
    return [{ tag: 'video[src]', getAttrs: element => ({ src: (element as HTMLVideoElement).getAttribute('src'), title: (element as HTMLVideoElement).getAttribute('title') }) }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['video', mergeAttributes(HTMLAttributes, { class: 'description-video', controls: 'true' })]
  },
  addNodeView() {
    return ({ node }) => {
      const dom = document.createElement('div')
      dom.className = 'description-video-node'
      dom.contentEditable = 'false'
      const video = document.createElement('video')
      video.className = 'description-video'
      video.controls = true
      video.preload = 'none'
      const apply = (attrs: Record<string, unknown>) => {
        video.src = String(attrs.src ?? '')
        video.title = String(attrs.title ?? '')
      }
      apply(node.attrs)
      dom.append(video)
      return {
        dom,
        update(updated) {
          if (updated.type.name !== 'video') return false
          apply(updated.attrs)
          return true
        },
      }
    }
  },
  markdownTokenName: 'descriptionVideo',
  parseMarkdown: (token, helpers) => helpers.createNode('video', { src: String(token.href ?? token.src ?? ''), title: String(token.text ?? '') }),
  renderMarkdown: node => `::video[${String(node.attrs?.title || '')}](${String(node.attrs?.src || '')})`,
  markdownTokenizer: {
    name: 'descriptionVideo',
    level: 'block' as const,
    start: (src: string) => src.startsWith('::video[') ? 0 : -1,
    tokenize(src: string) {
      const match = src.match(/^::video\[([^\]]*)]\(([^)\s]+)\)[ \t]*\n?/)
      if (!match) return undefined
      return { type: 'descriptionVideo', raw: match[0], text: match[1], href: match[2] }
    },
  },
})

export function insertEmbedFiles(view: EditorView, files: File[], upload?: DescriptionImageUpload, pos?: number) {
  const videos = files.filter(file => file.type.startsWith('video/'))
  const others = files.filter(file => !file.type.startsWith('video/') && !file.type.startsWith('image/'))
  let insertPos = pos
  let inserted = false
  // Each atom goes right after the previous one, so several files keep the order they were picked in.
  for (const file of videos) {
    if (!view.state.schema.nodes.video) break
    const end = insertAtom(view, 'video', { src: URL.createObjectURL(file), title: file.name }, upload, file, insertPos)
    if (end != null) { inserted = true; insertPos = end }
  }
  for (const file of others) {
    if (!view.state.schema.nodes.file) break
    const end = insertAtom(view, 'file', { src: URL.createObjectURL(file), title: file.name, size: file.size, contentType: file.type }, upload, file, insertPos)
    if (end != null) { inserted = true; insertPos = end }
  }
  return inserted
}

function insertAtom(view: EditorView, type: 'file' | 'video', attrs: Record<string, unknown>, upload: DescriptionImageUpload | undefined, file: File, pos?: number) {
  const nodeType = view.state.schema.nodes[type]
  if (!nodeType) return null
  const node = nodeType.create(attrs)
  const tr = pos == null ? view.state.tr.replaceSelectionWith(node) : view.state.tr.insert(pos, node)
  view.dispatch(tr.scrollIntoView())
  void persistSrc(view, type, String(attrs.src), file, upload)
  let end = pos == null ? view.state.selection.to : pos + node.nodeSize
  view.state.doc.descendants((child, childPos) => {
    if (child.type === nodeType && child.attrs.src === attrs.src) end = childPos + child.nodeSize
    return true
  })
  return end
}

function persistSrc(view: EditorView, type: string, placeholder: string, file: File, upload?: DescriptionImageUpload) {
  const apply = (src: string | null) => {
    const tr = view.state.tr
    view.state.doc.descendants((node, pos) => {
      if (node.type.name === type && node.attrs.src === placeholder) {
        if (src) tr.setNodeMarkup(pos, undefined, { ...node.attrs, src })
        else tr.delete(pos, pos + node.nodeSize)
      }
    })
    if (tr.docChanged) view.dispatch(tr)
    URL.revokeObjectURL(placeholder)
  }
  if (!upload) return
  return upload(file).then(url => apply(url)).catch(() => apply(null))
}

const PDF_PREVIEW_MAX_BYTES = 20 * 1024 * 1024

/**
 * The strip under a PDF card: the first page in a clipped 480px box. There is no PDF renderer in the bundle, so the
 * browser's own viewer is embedded. Uploads are served with a sandboxing CSP that blocks that viewer in frames, so a
 * same-origin file is fetched and framed from a blob URL instead. The frame ignores the pointer, so a click on the
 * strip only selects the card.
 */
function createPdfPreview() {
  const dom = document.createElement('div')
  dom.className = 'description-file-preview'
  dom.contentEditable = 'false'
  const frame = document.createElement('iframe')
  frame.className = 'description-file-preview-frame'
  frame.tabIndex = -1
  frame.setAttribute('aria-hidden', 'true')
  frame.setAttribute('loading', 'lazy')
  const page = document.createElement('div')
  page.className = 'description-file-preview-page'
  page.append(frame)
  dom.append(page)
  // The viewer paints its own dark ground before the page; show the frame only once it has loaded.
  frame.addEventListener('load', () => { if (frame.getAttribute('src')) dom.dataset.state = 'ready' })
  let current = ''
  let objectUrl = ''
  let token = 0
  const release = () => {
    if (objectUrl) URL.revokeObjectURL(objectUrl)
    objectUrl = ''
  }
  const frameFor = (url: string) => `${url}#toolbar=0&navpanes=0&scrollbar=0&view=FitH`
  return {
    dom,
    show(src: string, size: number) {
      if (src === current) return
      current = src
      token += 1
      const mine = token
      release()
      frame.removeAttribute('src')
      dom.dataset.state = src ? 'loading' : 'empty'
      if (!src || size > PDF_PREVIEW_MAX_BYTES) return
      if (src.startsWith('blob:')) {
        frame.src = frameFor(src)
        return
      }
      if (typeof fetch !== 'function' || typeof URL.createObjectURL !== 'function') return
      void fetch(src, { credentials: 'same-origin' })
        .then(response => response.ok ? response.blob() : Promise.reject(new Error('preview')))
        .then(blob => {
          if (mine !== token) return
          objectUrl = URL.createObjectURL(new Blob([blob], { type: 'application/pdf' }))
          frame.src = frameFor(objectUrl)
        })
        .catch(() => { if (mine === token) dom.dataset.state = 'error' })
    },
    destroy() {
      token += 1
      release()
    },
  }
}

const FILE_PATH = 'M6.75 1V6.75C6.75 7.5297 7.34489 8.17045 8.10554 8.24313L8.25 8.25H14V13C14 14.1046 13.1046 15 12 15H4C2.89543 15 2 14.1046 2 13V3C2 1.89543 2.89543 1 4 1H6.75ZM8 1L14 7.03022H9C8.44772 7.03022 8 6.5825 8 6.03022V1Z'
const PDF_PATH = 'M8.75 5.9h4.125L8.75 2.05zM3.5 1h6L14 5.2v8.4a1.5 1.4 0 0 1-1.5 1.4h-9A1.5 1.4 0 0 1 2 13.6V2.4A1.5 1.4 0 0 1 3.5 1m3.698 7.308c.307.63.697 1.148 1.147 1.505l.307.224a15 15 0 0 0-2.505.651l-.082.028.375-.728a10 10 0 0 0 .757-1.68m4.86 2.667a.66.66 0 0 0 .21-.462.53.53 0 0 0-.09-.385c-.217-.329-.78-.483-1.71-.483l-.967.049-.652-.406c-.473-.364-.9-1.001-1.2-1.792l.03-.098c.247-.931.48-2.058-.015-2.52a.638.595 0 0 0-.458-.168h-.18c-.277 0-.525.273-.592.539-.278.931-.113 1.442.165 2.289v.007c-.188.616-.428 1.33-.81 2.051l-.72 1.26-.668.343c-.9.525-1.327 1.113-1.41 1.484a.6.6 0 0 0 .038.378l.022.035.36.217.33.077c.608 0 1.298-.665 2.228-2.149l.135-.049c.772-.231 1.732-.392 3.022-.525.773.357 1.68.518 2.25.518.33 0 .555-.077.683-.21m-.308-.497.068.077c-.008.07-.03.077-.068.091h-.03l-.142.014c-.345 0-.878-.133-1.425-.357.067-.07.097-.07.172-.07 1.05 0 1.35.175 1.425.245M4.873 11.5c-.488.833-.93 1.295-1.268 1.4.038-.266.375-.728.908-1.183zm2.265-4.837c-.173-.63-.18-1.141-.053-1.435l.053-.084.112.035c.128.168.143.392.068.77l-.023.112-.12.574z'
const DOWNLOAD_PATHS = [
  'M7 2a1 1 0 0 1 2 0v5h2.5c.444 0 .668.568.353.9l-3.5 2.946a.482.482 0 0 1-.706 0L4.147 7.9c-.314-.332-.091-.9.354-.9H7V2Z',
  'M1.808 8.382c.446 0 .807.342.807.763v2.546c0 .984.844 1.782 1.885 1.782h7c1.04 0 1.885-.798 1.885-1.782V9.145c0-.421.361-.763.807-.763.446 0 .808.342.808.763v2.546C15 13.518 13.433 15 11.5 15h-7C2.567 15 1 13.518 1 11.69V9.146c0-.421.362-.763.808-.763Z',
]

function isPdf(contentType: string, title: string) {
  return contentType === 'application/pdf' || /\.pdf$/i.test(title)
}

function fileGlyph(contentType = '', title = '') {
  const path = isPdf(contentType, title) ? PDF_PATH : FILE_PATH
  return `<svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true"><path fill-rule="evenodd" clip-rule="evenodd" d="${path}"/></svg>`
}

function downloadGlyph() {
  return `<svg viewBox="0 0 16 16" width="14" height="14" fill="currentColor" aria-hidden="true">${DOWNLOAD_PATHS.map(d => `<path d="${d}"/>`).join('')}</svg>`
}
