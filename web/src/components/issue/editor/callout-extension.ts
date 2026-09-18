import { Node, mergeAttributes } from '@tiptap/core'

export const calloutColors = ['cyan', 'gray', 'green', 'yellow', 'orange', 'red', 'purple'] as const
export type CalloutColor = typeof calloutColors[number]

export const DescriptionCallout = Node.create({
  name: 'callout',
  group: 'block',
  content: 'block+',
  defining: true,
  addAttributes() {
    return { color: { default: 'cyan' } }
  },
  parseHTML() {
    return [{ tag: 'aside[data-callout]', getAttrs: element => ({ color: (element as HTMLElement).dataset.color || 'cyan' }) }]
  },
  renderHTML({ HTMLAttributes }) {
    return ['aside', mergeAttributes(HTMLAttributes, { 'data-callout': '', class: `description-callout is-${HTMLAttributes.color || 'cyan'}` }), 0]
  },
  addNodeView() {
    return ({ node, editor, getPos }) => {
      const dom = document.createElement('aside')
      const content = document.createElement('div')
      content.className = 'description-callout-content'
      const controls = document.createElement('span')
      controls.className = 'description-callout-controls'
      controls.contentEditable = 'false'
      const button = document.createElement('button')
      button.type = 'button'
      button.setAttribute('aria-label', 'Change callout icon or color')
      button.append(lightbulbIcon())
      const apply = (color: string) => {
        dom.className = `description-callout is-${color}`
        dom.dataset.callout = ''
        dom.dataset.color = color
      }
      apply(String(node.attrs.color || 'cyan'))
      button.addEventListener('mousedown', event => event.preventDefault())
      button.addEventListener('click', event => {
        event.preventDefault()
        event.stopPropagation()
        openCalloutColorMenu(button, color => {
          const pos = getPos()
          if (typeof pos !== 'number') return
          editor.chain().focus().command(({ tr }) => {
            tr.setNodeMarkup(pos, undefined, { ...editor.state.doc.nodeAt(pos)?.attrs, color })
            return true
          }).run()
        })
      })
      controls.append(button)
      dom.append(controls, content)
      return {
        dom,
        contentDOM: content,
        update(updated) {
          if (updated.type.name !== 'callout') return false
          apply(String(updated.attrs.color || 'cyan'))
          return true
        },
      }
    }
  },
  markdownTokenName: 'descriptionCallout',
  parseMarkdown: (token, helpers) => {
    const body = String((token as { text?: string }).text ?? '')
    return helpers.createNode('callout', { color: String((token as { color?: string }).color || 'cyan') }, [
      helpers.createNode('paragraph', {}, body ? [{ type: 'text', text: body }] : []),
    ])
  },
  renderMarkdown: (node, helpers) => `:::callout{${String(node.attrs?.color || 'cyan')}}\n${helpers.renderChildren(node.content ?? [])}:::\n`,
  markdownTokenizer: {
    name: 'descriptionCallout',
    level: 'block' as const,
    start: (src: string) => src.startsWith(':::callout') ? 0 : -1,
    tokenize(src: string) {
      const match = src.match(/^:::callout(?:\{([^}]+)\})?[ \t]*\n([\s\S]*?)\n:::[ \t]*(?:\n|$)/)
      if (!match) return undefined
      return { type: 'descriptionCallout', raw: match[0], color: match[1] || 'cyan', text: match[2] ?? '' }
    },
  },
})

function lightbulbIcon() {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  svg.setAttribute('viewBox', '0 0 16 16')
  svg.setAttribute('width', '16')
  svg.setAttribute('height', '16')
  svg.setAttribute('fill', 'currentColor')
  svg.setAttribute('aria-hidden', 'true')
  svg.innerHTML = '<path d="M10 13a2 2 0 1 1-4 0zM7.989 1a5.033 5.033 0 0 1 3.737 8.404l-1.25 1.387A1.8 1.8 0 0 0 10.011 12H6.014a1.9 1.9 0 0 0-.475-1.252L4.252 9.293A4.988 4.988 0 0 1 7.988 1"/>'
  return svg
}

function openCalloutColorMenu(anchor: HTMLElement, onPick: (color: CalloutColor) => void) {
  document.querySelector('.description-callout-menu')?.remove()
  const menu = document.createElement('div')
  menu.className = 'description-callout-menu'
  menu.setAttribute('role', 'menu')
  for (const color of calloutColors) {
    const item = document.createElement('button')
    item.type = 'button'
    item.setAttribute('role', 'menuitem')
    item.dataset.color = color
    item.setAttribute('aria-label', color)
    item.addEventListener('click', () => {
      menu.remove()
      onPick(color)
    })
    menu.append(item)
  }
  document.body.append(menu)
  const rect = anchor.getBoundingClientRect()
  menu.style.left = `${rect.left}px`
  menu.style.top = `${rect.bottom + 4}px`
  const close = (event: Event) => {
    if (menu.contains(event.target as globalThis.Node)) return
    menu.remove()
    window.removeEventListener('mousedown', close, true)
  }
  window.addEventListener('mousedown', close, true)
}
