import { Node, mergeAttributes, type Editor } from '@tiptap/core'
import { Plugin, PluginKey } from '@tiptap/pm/state'
import type { EditorView } from '@tiptap/pm/view'
import { insertEmbedFiles } from './file-extension'

export type DescriptionImageUpload = (file: File) => Promise<string>
export type DescriptionImageComment = (selection: { text: string; from: number; to: number; src: string }) => void

declare module '@tiptap/core' {
  interface Storage {
    image: { upload?: DescriptionImageUpload; onComment?: DescriptionImageComment }
  }
}

const imageKey = new PluginKey('flowDescriptionImage')

export function descriptionImageSrcs(markdown: string, state?: string, data?: Record<string, unknown>) {
  const srcs = new Set<string>()
  for (const match of markdown.matchAll(/!\[[^\]]*]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)) srcs.add(match[1])
  for (const match of markdown.matchAll(/::(?:file|video)\[[^\]]*]\(([^)\s]+)\)/g)) srcs.add(match[1])
  const walk = (node: unknown) => {
    if (!node || typeof node !== 'object') return
    const value = node as { type?: string; attrs?: { src?: string }; content?: unknown[] }
    if ((value.type === 'image' || value.type === 'video' || value.type === 'file') && value.attrs?.src) srcs.add(value.attrs.src)
    value.content?.forEach(walk)
  }
  if (state) {
    try { walk(JSON.parse(state) as unknown) } catch { /* markdown-only records */ }
  }
  if (data) walk(data)
  return srcs
}

export const DescriptionImage = Node.create({
  name: 'image',
  group: 'block',
  atom: true,
  draggable: true,
  selectable: true,
  addStorage() {
    return { upload: undefined as DescriptionImageUpload | undefined, onComment: undefined as DescriptionImageComment | undefined }
  },
  addAttributes() {
    return {
      src: { default: null },
      alt: { default: null },
      title: { default: null },
      width: { default: null },
      height: { default: null },
    }
  },
  parseHTML() {
    return [{ tag: 'img[src]' }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['img', mergeAttributes(HTMLAttributes, { class: 'description-image', draggable: 'false' })]
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = document.createElement('div')
      dom.className = 'description-image-node'
      dom.contentEditable = 'false'
      const frame = document.createElement('div')
      frame.className = 'description-image-frame'
      const img = document.createElement('img')
      img.className = 'description-image'
      img.draggable = false
      img.loading = 'lazy'
      applyImageAttrs(img, node.attrs)
      const actions = () => imageActions(editor, getPos, img)
      const toolbar = document.createElement('div')
      toolbar.className = 'description-image-toolbar'
      toolbar.append(
        iconButton('View image', viewIcon(), () => actions().view()),
        iconButton('Download', downloadIcon(), () => void actions().download()),
        iconButton('Copy image', copyIcon(), () => void actions().copyImage()),
        iconButton('Copy link', linkIcon(), () => void actions().copyLink()),
        iconButton('Add comment', commentIcon(), () => actions().comment()),
        iconButton('Delete', deleteIcon(), () => actions().remove()),
      )
      img.addEventListener('click', event => {
        event.preventDefault()
        actions().view()
      })
      frame.addEventListener('contextmenu', event => {
        event.preventDefault()
        event.stopPropagation()
        const pos = getPos()
        if (typeof pos === 'number') editor.chain().focus().setNodeSelection(pos).run()
        openImageContextMenu(event.clientX, event.clientY, actions())
      })
      frame.append(img, toolbar)
      dom.append(frame)
      return {
        dom,
        update(updated) {
          if (updated.type.name !== 'image') return false
          applyImageAttrs(img, updated.attrs)
          return true
        },
      }
    }
  },
  markdownTokenName: 'descriptionImage',
  parseMarkdown: (token, helpers) => helpers.createNode('image', { src: String(token.href ?? token.src ?? ''), alt: String(token.text ?? ''), title: token.title ? String(token.title) : null }),
  renderMarkdown: node => {
    const alt = String(node.attrs?.alt ?? '')
    const src = String(node.attrs?.src ?? '')
    const title = node.attrs?.title ? ` "${String(node.attrs.title)}"` : ''
    return `![${alt}](${src}${title})`
  },
  markdownTokenizer: {
    name: 'descriptionImage',
    level: 'block' as const,
    start: (src: string) => {
      const match = src.match(/^(?:[ \t]*)!\[[^\]]*]\([^)\s]+(?:\s+"[^"]*")?\)/)
      return match ? src.indexOf(match[0]) : -1
    },
    tokenize(src: string) {
      const match = src.match(/^(?:[ \t]*)!\[([^\]]*)]\(([^)\s]+)(?:\s+"([^"]*)")?\)[ \t]*(?:\n|$)/)
      if (!match) return undefined
      return { type: 'descriptionImage', raw: match[0], href: match[2], text: match[1], title: match[3] ?? null }
    },
  },
  addProseMirrorPlugins() {
    const editor = this.editor
    return [new Plugin({
      key: imageKey,
      props: {
        handlePaste(_view, event) {
          const files = [...(event.clipboardData?.files ?? [])]
          if (!files.length) return false
          const images = files.filter(file => file.type.startsWith('image/'))
          const rest = files.filter(file => !file.type.startsWith('image/'))
          if (!images.length && !rest.length) return false
          event.preventDefault()
          const uploaded = images.length ? insertImageFiles(editor.view, images, editor.storage.image.upload) : false
          return insertEmbedFiles(editor.view, rest, editor.storage.image.upload) || uploaded
        },
        handleDrop(view, event, _slice, moved) {
          if (moved) return false
          const files = [...(event.dataTransfer?.files ?? [])]
          if (!files.length) return false
          const images = files.filter(file => file.type.startsWith('image/'))
          const rest = files.filter(file => !file.type.startsWith('image/'))
          if (!images.length && !rest.length) return false
          event.preventDefault()
          const coords = view.posAtCoords({ left: event.clientX, top: event.clientY })
          const uploaded = images.length ? insertImageFiles(view, images, editor.storage.image.upload, coords?.pos) : false
          return insertEmbedFiles(view, rest, editor.storage.image.upload, coords?.pos) || uploaded
        },
      },
    })]
  },
})

export function insertImageFiles(view: EditorView, files: File[], upload?: DescriptionImageUpload, pos?: number) {
  if (!files.length || !view.state.schema.nodes.image) return false
  let insertPos = pos
  for (const file of files) {
    const placeholder = URL.createObjectURL(file)
    const node = view.state.schema.nodes.image.create({ src: placeholder, alt: file.name })
    if (insertPos == null) {
      view.dispatch(view.state.tr.replaceSelectionWith(node).scrollIntoView())
      insertPos = view.state.selection.from
    } else {
      view.dispatch(view.state.tr.insert(insertPos, node).scrollIntoView())
      insertPos += node.nodeSize
    }
    void persistImage(view, placeholder, file, upload)
  }
  return true
}

function persistImage(view: EditorView, placeholder: string, file: File, upload?: DescriptionImageUpload) {
  const apply = (src: string | null) => {
    const tr = view.state.tr
    view.state.doc.descendants((node, pos) => {
      if (node.type.name === 'image' && node.attrs.src === placeholder) {
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

function applyImageAttrs(img: HTMLImageElement, attrs: Record<string, unknown>) {
  img.src = String(attrs.src ?? '')
  img.alt = String(attrs.alt ?? '')
  if (attrs.width) img.width = Number(attrs.width)
  if (attrs.height) img.height = Number(attrs.height)
  const applySize = () => {
    if (!img.naturalWidth) return
    if (!img.getAttribute('width')) img.width = img.naturalWidth
    if (!img.getAttribute('height')) img.height = img.naturalHeight
    img.style.maxWidth = `min(100%, ${img.naturalWidth * 2}px)`
    img.style.minWidth = `${img.naturalWidth}px`
  }
  if (img.complete) applySize()
  else img.addEventListener('load', applySize, { once: true })
}

function imageActions(editor: Editor, getPos: () => number | undefined, img: HTMLImageElement) {
  const range = () => {
    const pos = getPos()
    if (typeof pos !== 'number') return null
    const node = editor.state.doc.nodeAt(pos)
    return { from: pos, to: pos + (node?.nodeSize ?? 1) }
  }
  return {
    view: () => openDescriptionLightbox(img.src, img.alt),
    download: () => downloadImage(img.src, img.alt || 'image'),
    copyImage: () => copyImage(img.src),
    copyLink: () => navigator.clipboard.writeText(imageHref(img.src)),
    comment: () => {
      const selection = range()
      if (!selection) return
      editor.storage.image.onComment?.({ text: img.alt || 'Image', from: selection.from, to: selection.to, src: img.src })
    },
    remove: () => {
      const selection = range()
      if (!selection) return
      editor.chain().focus().deleteRange(selection).run()
    },
  }
}

function openImageContextMenu(x: number, y: number, actions: ReturnType<typeof imageActions>) {
  closeImageContextMenu()
  const menu = document.createElement('div')
  menu.className = 'description-image-menu'
  menu.setAttribute('role', 'menu')
  const item = (label: string, icon: SVGSVGElement, run: () => void) => {
    const button = document.createElement('button')
    button.type = 'button'
    button.setAttribute('role', 'menuitem')
    button.append(icon, document.createTextNode(label))
    button.addEventListener('click', () => {
      closeImageContextMenu()
      run()
    })
    return button
  }
  const separator = document.createElement('div')
  separator.className = 'description-image-menu-separator'
  separator.setAttribute('role', 'separator')
  menu.append(
    item('View image', viewIcon(), actions.view),
    item('Download', downloadIcon(), () => void actions.download()),
    item('Copy image', copyIcon(), () => void actions.copyImage()),
    item('Copy link', linkIcon(), () => void actions.copyLink()),
    separator,
    item('Add comment', commentIcon(), actions.comment),
    item('Delete', deleteIcon(), actions.remove),
  )
  document.body.append(menu)
  const width = menu.offsetWidth
  const height = menu.offsetHeight
  menu.style.left = `${Math.min(x, window.innerWidth - width - 8)}px`
  menu.style.top = `${Math.min(y, window.innerHeight - height - 8)}px`
  const onPointer = (event: Event) => {
    if (menu.contains(event.target as globalThis.Node)) return
    closeImageContextMenu()
  }
  const onKey = (event: KeyboardEvent) => {
    if (event.key === 'Escape') closeImageContextMenu()
  }
  window.addEventListener('mousedown', onPointer, true)
  window.addEventListener('keydown', onKey, true)
  menu.dataset.listeners = '1'
  ;(menu as HTMLDivElement & { _onPointer?: EventListener; _onKey?: (event: KeyboardEvent) => void })._onPointer = onPointer
  ;(menu as HTMLDivElement & { _onKey?: (event: KeyboardEvent) => void })._onKey = onKey
}

export function closeImageContextMenu() {
  document.querySelectorAll('.description-image-menu').forEach(node => {
    const menu = node as HTMLDivElement & { _onPointer?: EventListener; _onKey?: (event: KeyboardEvent) => void }
    if (menu._onPointer) window.removeEventListener('mousedown', menu._onPointer, true)
    if (menu._onKey) window.removeEventListener('keydown', menu._onKey, true)
    menu.remove()
  })
}

function iconButton(label: string, icon: SVGSVGElement, onClick: () => void) {
  const button = document.createElement('button')
  button.type = 'button'
  button.setAttribute('aria-label', label)
  button.append(icon)
  button.addEventListener('mousedown', event => event.preventDefault())
  button.addEventListener('click', event => {
    event.preventDefault()
    event.stopPropagation()
    onClick()
  })
  return button
}

function filledIcon(path: string) {
  const node = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  node.setAttribute('viewBox', '0 0 16 16')
  node.setAttribute('width', '16')
  node.setAttribute('height', '16')
  node.setAttribute('fill', 'currentColor')
  node.setAttribute('aria-hidden', 'true')
  node.innerHTML = path
  return node
}

function viewIcon() {
  return filledIcon('<path d="M6.2168 8.72266C6.50798 8.42824 6.98279 8.4257 7.27734 8.7168C7.57154 9.00799 7.57423 9.48287 7.2832 9.77734L4.59863 12.5H6.2998C6.71402 12.5 7.0498 12.8358 7.0498 13.25C7.04964 13.6641 6.71392 14 6.2998 14H2.75C2.55116 14 2.36036 13.9208 2.21973 13.7803C2.07915 13.6397 2.00008 13.4488 2 13.25V9.75C2 9.33579 2.33579 9 2.75 9C3.16421 9 3.5 9.33579 3.5 9.75V11.4775L6.2168 8.72266Z"/><path d="M13.25 2C13.4488 2.00006 13.6397 2.07917 13.7803 2.21973C13.9208 2.36033 14 2.55119 14 2.75V6.25C14 6.66414 13.6641 6.99988 13.25 7C12.8358 7 12.5 6.66421 12.5 6.25V4.52246L9.7832 7.27734C9.49206 7.57173 9.01721 7.57419 8.72266 7.2832C8.42838 6.99201 8.42575 6.51716 8.7168 6.22266L11.4014 3.5H9.7002C9.28598 3.5 8.9502 3.16421 8.9502 2.75C8.95028 2.33586 9.28603 2 9.7002 2H13.25Z"/>')
}
function downloadIcon() {
  return filledIcon('<path d="M7 2a1 1 0 0 1 2 0v5h2.5c.444 0 .668.568.353.9l-3.5 2.946a.482.482 0 0 1-.706 0L4.147 7.9c-.314-.332-.091-.9.354-.9H7V2Z"/><path d="M1.808 8.382c.446 0 .807.342.807.763v2.546c0 .984.844 1.782 1.885 1.782h7c1.04 0 1.885-.798 1.885-1.782V9.145c0-.421.361-.763.807-.763.446 0 .808.342.808.763v2.546C15 13.518 13.433 15 11.5 15h-7C2.567 15 1 13.518 1 11.69V9.146c0-.421.362-.763.808-.763Z"/>')
}
function copyIcon() {
  return filledIcon('<path fill-rule="evenodd" d="M6.08 2.5v1.167h3.834V2.5H6.08Zm-1.5-.083C4.58 1.634 5.216 1 5.998 1h4c.783 0 1.417.634 1.417 1.417V3.75c0 .782-.634 1.417-1.417 1.417h-4A1.417 1.417 0 0 1 4.581 3.75V2.417Z" clip-rule="evenodd"/><path fill-rule="evenodd" d="M4.087 3.749a.583.583 0 0 0-.583.583l.001 8.583a.583.583 0 0 0 .584.584h7.82a.583.583 0 0 0 .583-.584V4.332a.583.583 0 0 0-.584-.583H11a.75.75 0 0 1 0-1.5h.909a2.083 2.083 0 0 1 2.083 2.083v8.583A2.083 2.083 0 0 1 11.908 15h-7.82a2.083 2.083 0 0 1-2.083-2.084l-.001-8.583A2.084 2.084 0 0 1 4.087 2.25H5a.75.75 0 1 1 0 1.5h-.913Z" clip-rule="evenodd"/>')
}
function linkIcon() {
  return filledIcon('<path d="M9.30558 10.206C9.57224 10.4726 9.59447 10.8912 9.37225 11.1831L9.30558 11.2594L6.84751 13.7175C5.58692 14.9781 3.54311 14.9781 2.28252 13.7175C1.0654 12.5004 1.02344 10.5531 2.15661 9.28564L2.28252 9.15251L4.74059 6.69443C5.0315 6.40353 5.50315 6.40353 5.79405 6.69443C6.06071 6.9611 6.08294 7.37963 5.86072 7.67161L5.79405 7.74789L3.33598 10.206C2.6572 10.8847 2.6572 11.9853 3.33598 12.664C3.98082 13.3089 5.00628 13.3411 5.68918 12.7608L5.79405 12.664L8.25212 10.206C8.54303 9.91506 9.01468 9.91506 9.30558 10.206ZM9.82982 6.17019C10.1207 6.46109 10.1207 6.93274 9.82982 7.22365L7.34921 9.70427C7.0583 9.99518 6.58665 9.99518 6.29575 9.70427C6.00484 9.41337 6.00484 8.94172 6.29575 8.65081L8.77637 6.17019C9.06727 5.87928 9.53892 5.87928 9.82982 6.17019ZM13.7175 2.2825C14.9346 3.49962 14.9766 5.44688 13.8434 6.71436L13.7175 6.84749L11.2594 9.30557C10.9685 9.59647 10.4969 9.59647 10.206 9.30557C9.93931 9.03891 9.91709 8.62037 10.1393 8.32839L10.206 8.25211L12.664 5.79403C13.3428 5.11525 13.3428 4.01474 12.664 3.33596C12.0192 2.69112 10.9938 2.65888 10.3109 3.23923L10.206 3.33596L7.74791 5.79403C7.457 6.08494 6.98535 6.08494 6.69445 5.79403C6.42779 5.52737 6.40556 5.10883 6.62778 4.81686L6.69445 4.74057L9.15252 2.2825C10.4131 1.02191 12.4569 1.02191 13.7175 2.2825Z"/>')
}
function commentIcon() {
  return filledIcon('<path d="M10.75 14.5576C10.75 14.9718 11.0858 15.3076 11.5 15.3076C11.9142 15.3076 12.25 14.9718 12.25 14.5576V12.5576H14.25C14.6642 12.5576 15 12.2218 15 11.8076C15 11.3934 14.6642 11.0576 14.25 11.0576H12.25V9.05762C12.25 8.64341 11.9142 8.30762 11.5 8.30762C11.0858 8.30762 10.75 8.64341 10.75 9.05762V11.0576H8.75C8.33579 11.0576 8 11.3934 8 11.8076C8 12.2218 8.33579 12.5576 8.75 12.5576H10.75V14.5576Z"/><path d="M13.3775 6.80762C13.1401 5.67391 12.578 4.86209 11.8688 4.28759C10.8792 3.48587 9.486 3.05762 8 3.05762C6.30594 3.05762 4.9352 3.49835 4.01455 4.27103C3.13416 5.00992 2.5 6.18862 2.5 8.05762C2.5 9.16174 2.75464 10.018 3.14105 10.6797C3.68197 11.489 3.36513 12.3299 3.25987 12.586C3.18733 12.7624 3.09968 12.9296 3.04096 13.0411L3.01094 13.098C2.95814 13.198 2.9202 13.2699 2.88441 13.3453L2.87483 13.3657C3.11737 13.5453 3.43758 13.5828 3.97549 13.4433C4.28137 13.3639 4.57095 13.2468 4.86788 13.1242L4.895 13.113C5.01891 13.0618 5.18568 12.9929 5.33284 12.9436C5.41258 12.917 5.54224 12.8766 5.69294 12.852C5.78288 12.8373 6.05757 12.7944 6.3882 12.8807C6.42534 12.8891 6.46261 12.8973 6.5 12.9051C6.91095 12.9913 7.26196 13.2464 7.26196 13.6672C7.26196 14.0881 6.92082 14.4292 6.5 14.4292V14.4318C6.34375 14.4092 6.33347 14.4022 6.23623 14.3823C6.16278 14.3673 6.0896 14.3514 6.01674 14.3344C5.93992 14.3045 5.73556 14.3889 5.44698 14.5079C4.5721 14.869 2.92323 15.5495 1.70812 14.3344C1.02304 13.6493 1.38868 12.9574 1.68711 12.3926C1.88052 12.0266 2.04571 11.714 1.87837 11.4913C1.32592 10.5695 1 9.42951 1 8.05762C1 3.5628 4.13401 1.55762 8 1.55762C11.0799 1.55762 14.1578 3.10916 14.854 6.54198C14.8773 6.65673 14.8982 6.69433 14.8982 6.78156C14.8982 7.25541 14.5622 7.572 14.1827 7.572C13.7659 7.572 13.4766 7.18182 13.379 6.80762H13.3775Z"/>')
}
function deleteIcon() {
  return filledIcon('<path fill-rule="evenodd" d="m2 3 1.652 9.911A2.5 2.5 0 0 0 6.118 15h3.764a2.5 2.5 0 0 0 2.466-2.089L14 3H2Zm1.77 1.5 1.361 8.164a1 1 0 0 0 .987.836h3.764a1 1 0 0 0 .987-.836l1.36-8.164H3.771Z" clip-rule="evenodd"/><path d="M5.5 2.5A1.5 1.5 0 0 1 7 1h2a1.5 1.5 0 0 1 1.5 1.5v1h-5v-1Z"/><path d="M1 3.75A.75.75 0 0 1 1.75 3h12.5a.75.75 0 0 1 0 1.5H1.75A.75.75 0 0 1 1 3.75Z"/>')
}

function imageHref(src: string) {
  try { return new URL(src, window.location.origin).href } catch { return src }
}

async function copyImage(src: string) {
  const blob = await fetch(src).then(response => response.blob())
  const png = blob.type === 'image/png' ? blob : await blobToPng(blob)
  await navigator.clipboard.write([new ClipboardItem({ 'image/png': png })])
}

async function blobToPng(blob: Blob) {
  const bitmap = await createImageBitmap(blob)
  const canvas = document.createElement('canvas')
  canvas.width = bitmap.width
  canvas.height = bitmap.height
  canvas.getContext('2d')?.drawImage(bitmap, 0, 0)
  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(next => next ? resolve(next) : reject(new Error('png')), 'image/png')
  })
}

async function downloadImage(src: string, name: string) {
  const blob = await fetch(src).then(response => response.blob())
  const href = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = href
  link.download = name
  link.rel = 'noreferrer'
  document.body.append(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(href)
}

export function openDescriptionLightbox(src: string, alt = '') {
  document.querySelector('.description-image-lightbox')?.remove()
  const overlay = document.createElement('div')
  overlay.className = 'description-image-lightbox'
  overlay.setAttribute('role', 'dialog')
  overlay.setAttribute('aria-label', 'View image')
  const img = document.createElement('img')
  img.src = src
  img.alt = alt
  overlay.append(img)
  const close = () => overlay.remove()
  overlay.addEventListener('click', close)
  document.addEventListener('keydown', function onKey(event) {
    if (event.key !== 'Escape') return
    close()
    document.removeEventListener('keydown', onKey)
  })
  document.body.append(overlay)
}
