import { Node, mergeAttributes } from '@tiptap/core'

const defaultSource = 'flowchart TD\n    A[Start] --> B[End]'

export const DescriptionDiagram = Node.create({
  name: 'diagram',
  group: 'block',
  atom: true,
  selectable: true,
  addAttributes() {
    return { source: { default: defaultSource } }
  },
  parseHTML() {
    return [{ tag: 'div[data-diagram]', getAttrs: element => ({ source: (element as HTMLElement).dataset.source || defaultSource }) }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['div', mergeAttributes(HTMLAttributes, { 'data-diagram': '', class: 'description-diagram', 'data-source': HTMLAttributes.source })]
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = document.createElement('div')
      dom.className = 'description-diagram'
      dom.contentEditable = 'false'
      const toolbar = document.createElement('div')
      toolbar.className = 'description-diagram-toolbar'
      const edit = document.createElement('button')
      edit.type = 'button'
      edit.textContent = 'Edit'
      const preview = document.createElement('div')
      preview.className = 'description-diagram-preview'
      const source = document.createElement('textarea')
      source.className = 'description-diagram-source'
      source.spellcheck = false
      let editing = false
      let rendered = ''
      const render = (value: string) => {
        if (value === rendered && preview.childElementCount) return
        rendered = value
        source.value = value
        void renderMermaid(preview, value)
      }
      render(String(node.attrs.source || defaultSource))
      edit.addEventListener('mousedown', event => event.preventDefault())
      edit.addEventListener('click', () => {
        editing = !editing
        dom.classList.toggle('is-editing', editing)
        edit.textContent = editing ? 'Preview' : 'Edit'
        if (!editing) {
          const pos = getPos()
          if (typeof pos === 'number') editor.chain().focus().command(({ tr }) => {
            tr.setNodeMarkup(pos, undefined, { source: source.value })
            return true
          }).run()
          void renderMermaid(preview, source.value)
        }
      })
      toolbar.append(edit)
      dom.append(toolbar, preview, source)
      return {
        dom,
        update(updated) {
          if (updated.type.name !== 'diagram') return false
          if (!editing) render(String(updated.attrs.source || defaultSource))
          return true
        },
      }
    }
  },
  markdownTokenName: 'descriptionDiagram',
  parseMarkdown: (token, helpers) => helpers.createNode('diagram', { source: String(token.text ?? defaultSource) }),
  renderMarkdown: node => `\`\`\`mermaid\n${String(node.attrs?.source || defaultSource).trim()}\n\`\`\`\n`,
  markdownTokenizer: {
    name: 'descriptionDiagram',
    level: 'block' as const,
    start: (src: string) => src.startsWith('```mermaid') ? 0 : -1,
    tokenize(src: string) {
      const match = src.match(/^```mermaid[ \t]*\n([\s\S]*?)\n```[ \t]*(?:\n|$)/)
      if (!match) return undefined
      return { type: 'descriptionDiagram', raw: match[0], text: match[1] }
    },
  },
})

async function renderMermaid(target: HTMLElement, source: string) {
  const render = window.__flowMermaidPreview
  if (render) {
    try {
      await render(target, source || defaultSource)
      return
    } catch { /* fall through to source preview */ }
  }
  const pre = document.createElement('pre')
  pre.textContent = source || defaultSource
  target.replaceChildren(pre)
}

declare global {
  interface Window {
    __flowMermaidPreview?: (target: HTMLElement, source: string) => Promise<void>
  }
}
