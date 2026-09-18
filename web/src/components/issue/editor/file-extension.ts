import { Node, mergeAttributes } from '@tiptap/core'
import type { EditorView } from '@tiptap/pm/view'

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
    return ({ node, editor, getPos }) => {
      const dom = document.createElement('div')
      dom.className = 'description-file-node'
      dom.contentEditable = 'false'
      const link = document.createElement('a')
      link.className = 'description-file'
      link.target = '_blank'
      link.rel = 'noreferrer'
      const icon = document.createElement('span')
      icon.className = 'description-file-icon'
      icon.innerHTML = fileGlyph()
      const copy = document.createElement('span')
      copy.className = 'description-file-copy'
      const name = document.createElement('strong')
      const meta = document.createElement('small')
      copy.append(name, meta)
      const apply = (attrs: Record<string, unknown>) => {
        link.href = String(attrs.src ?? '')
        name.textContent = String(attrs.title || 'File')
        meta.textContent = formatBytes(Number(attrs.size) || 0)
      }
      apply(node.attrs)
      link.append(icon, copy)
      link.addEventListener('click', event => event.stopPropagation())
      const remove = document.createElement('button')
      remove.type = 'button'
      remove.setAttribute('aria-label', 'Delete')
      remove.className = 'description-file-remove'
      remove.textContent = '×'
      remove.addEventListener('mousedown', event => event.preventDefault())
      remove.addEventListener('click', event => {
        event.preventDefault()
        const pos = getPos()
        if (typeof pos !== 'number') return
        const current = editor.state.doc.nodeAt(pos)
        editor.chain().focus().deleteRange({ from: pos, to: pos + (current?.nodeSize ?? 1) }).run()
      })
      dom.append(link, remove)
      return {
        dom,
        update(updated) {
          if (updated.type.name !== 'file') return false
          apply(updated.attrs)
          return true
        },
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
  for (const file of videos) {
    if (!view.state.schema.nodes.video) break
    inserted = insertAtom(view, 'video', { src: URL.createObjectURL(file), title: file.name }, upload, file, insertPos) || inserted
    insertPos = insertPos == null ? view.state.selection.from : insertPos + 1
  }
  for (const file of others) {
    if (!view.state.schema.nodes.file) break
    inserted = insertAtom(view, 'file', { src: URL.createObjectURL(file), title: file.name, size: file.size, contentType: file.type }, upload, file, insertPos) || inserted
    insertPos = insertPos == null ? view.state.selection.from : insertPos + 1
  }
  return inserted
}

function insertAtom(view: EditorView, type: 'file' | 'video', attrs: Record<string, unknown>, upload: DescriptionImageUpload | undefined, file: File, pos?: number) {
  const nodeType = view.state.schema.nodes[type]
  if (!nodeType) return false
  const node = nodeType.create(attrs)
  if (pos == null) view.dispatch(view.state.tr.replaceSelectionWith(node).scrollIntoView())
  else view.dispatch(view.state.tr.insert(pos, node).scrollIntoView())
  void persistSrc(view, type, String(attrs.src), file, upload)
  return true
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

function formatBytes(size: number) {
  if (!size) return ''
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${Math.round(size / 1024)} KB`
  return `${(size / 1024 / 1024).toFixed(1)} MB`
}

function fileGlyph() {
  return '<svg viewBox="0 0 16 16" width="16" height="16" fill="currentColor" aria-hidden="true"><path d="M3.75 1A1.75 1.75 0 0 0 2 2.75v10.5C2 14.216 2.784 15 3.75 15h8.5A1.75 1.75 0 0 0 14 13.25V6.586a1.75 1.75 0 0 0-.513-1.238L9.652 1.513A1.75 1.75 0 0 0 8.414 1H3.75ZM8 2v3.25c0 .966.784 1.75 1.75 1.75H13v6.25a.25.25 0 0 1-.25.25h-8.5a.25.25 0 0 1-.25-.25V2.75a.25.25 0 0 1 .25-.25H8Zm1.5.56 2.94 2.94H9.75a.25.25 0 0 1-.25-.25V2.56Z"/></svg>'
}
