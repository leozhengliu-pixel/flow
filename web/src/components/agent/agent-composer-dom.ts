import type { AgentFileKind } from './agent-attachments'
import { splitAgentBlocks } from './agent-content-model'
import styles from './agent-mention-input.module.css'

export type BlockKind = Exclude<AgentFileKind, 'audio'> | 'embed'

/** An image, video or file block in the composer: shown from `preview` while it uploads, then from its uploaded `src`. */
export type InlineBlock = { el: HTMLElement; id: string; kind: BlockKind; name: string; src: string; size: number; type: string; preview?: string; failed?: boolean }

let inlineBlockId = 0

export function inlineBlock(kind: BlockKind, name: string, src: string, size = 0, type = '') {
  const block = document.createElement('div')
  block.setAttribute('contenteditable', 'false')
  block.className = styles.block
  block.dataset.agentBlock = `block-${++inlineBlockId}`
  block.dataset.kind = kind
  block.dataset.name = name
  block.dataset.src = src
  block.dataset.size = String(size)
  block.dataset.type = type
  return block
}

export const isBlock = (node: Node | null | undefined) => node instanceof HTMLElement && node.dataset.agentBlock !== undefined

/** A line with nothing on it: the one to type on after a block. */
export const isEmptyLine = (node: Node | null | undefined) => node instanceof HTMLElement && node.tagName === 'DIV' && !isBlock(node) && !node.textContent && !node.querySelector('[data-agent-block], [data-mention-id]')

export function emptyLine() {
  const line = document.createElement('div')
  line.append(document.createElement('br'))
  return line
}

/**
 * Writes a message into the composer: text as typed, its Markdown images, videos and files as blocks (editing a sent
 * message, restoring a draft). The composer picks the blocks up on the input event this fires.
 */
export function fillAgentComposer(editor: HTMLElement, value: string) {
  editor.textContent = ''
  const parts = splitAgentBlocks(value)
  parts.forEach((part, index) => {
    if (part.type === 'text') {
      if (!part.text) return
      // Text after a block sits on its own line, so the block's spacing applies to it.
      if (isBlock(editor.lastChild)) {
        const line = document.createElement('div')
        line.append(document.createTextNode(part.text))
        editor.append(line)
      } else editor.append(document.createTextNode(part.text))
      return
    }
    editor.append(part.type === 'image' ? inlineBlock('image', part.alt, part.src)
      : part.type === 'embed' ? inlineBlock('embed', part.src, part.src)
      : part.type === 'video' ? inlineBlock('video', part.name, part.src)
      : inlineBlock('file', part.name, part.src, part.size, part.contentType))
    if (!parts[index + 1]) editor.append(emptyLine())
  })
  editor.dispatchEvent(new Event('input', { bubbles: true }))
}
