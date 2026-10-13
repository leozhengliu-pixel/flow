import styles from './agent-change-cards.module.css'

/**
 * The reference app's "Highlight changes": an accent bar beside every block the agent added or rewrote in the editors on screen.
 * The bars live in a fixed overlay that follows the blocks, so the editor's own DOM (ProseMirror) is never touched.
 */
export type AgentChangeHighlight = { matched: number; stop: () => void }

const normalize = (value: string) => value.replace(/\s+/g, ' ').trim()

function blockText(element: Element) {
  // A list item reads as its own paragraph, without nested lists.
  if (element.tagName === 'LI') {
    const first = element.firstElementChild
    return normalize((first && first.tagName === 'P' ? first : element).textContent ?? '')
  }
  return normalize(element.textContent ?? '')
}

function matches(text: string, target: string) {
  if (!text || !target) return false
  if (text === target) return true
  // Mentions and links can render differently from their Markdown; a long block still matches on its opening.
  return target.length >= 24 && text.length >= 24 && (text.startsWith(target.slice(0, 48)) || target.startsWith(text.slice(0, 48)))
}

/** Editable editors on the page, outside agent chat surfaces (answers render read-only editors too). */
function liveEditors(root: ParentNode) {
  return [...root.querySelectorAll<HTMLElement>('.ProseMirror[contenteditable="true"]')].filter(editor => !editor.closest('[data-agent-changes], [data-agent-chat-surface]'))
}

export function findChangedBlocks(texts: string[], root: ParentNode = document): HTMLElement[] {
  const targets = texts.map(normalize).filter(Boolean)
  if (!targets.length) return []
  const found: HTMLElement[] = []
  for (const editor of liveEditors(root)) {
    for (const element of editor.querySelectorAll<HTMLElement>(':scope > *, li')) {
      if (element.tagName === 'UL' || element.tagName === 'OL') continue
      const text = blockText(element)
      if (targets.some(target => matches(text, target)) && !found.some(item => item.contains(element) || element.contains(item))) found.push(element)
    }
  }
  return found
}

function scrollParent(element: HTMLElement) {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY
    if (overflow === 'auto' || overflow === 'scroll') return node
  }
  return undefined
}

export function highlightAgentChanges(texts: string[], root: ParentNode = document): AgentChangeHighlight {
  let blocks = findChangedBlocks(texts, root)
  if (!blocks.length) return { matched: 0, stop: () => {} }
  const layer = document.createElement('div')
  layer.className = styles.highlightLayer
  layer.dataset.agentChangeHighlight = ''
  layer.setAttribute('aria-hidden', 'true')
  document.body.append(layer)
  let frame = 0
  let ticks = 0
  let stopped = false
  const draw = () => {
    if (stopped) return
    // Editors re-render their blocks (typing, collaboration): look them up again now and then.
    if (++ticks % 30 === 0 || blocks.some(block => !block.isConnected)) {
      const next = findChangedBlocks(texts, root)
      if (next.length) blocks = next
    }
    while (layer.childElementCount < blocks.length) {
      const bar = document.createElement('span')
      bar.className = styles.highlightBar
      layer.append(bar)
    }
    while (layer.childElementCount > blocks.length) layer.lastElementChild?.remove()
    blocks.forEach((block, index) => {
      const bar = layer.children[index] as HTMLElement
      const rect = block.getBoundingClientRect()
      const editor = block.closest('.ProseMirror')?.getBoundingClientRect() ?? rect
      const clip = scrollParent(block)?.getBoundingClientRect()
      const top = Math.max(rect.top, clip?.top ?? -Infinity)
      const bottom = Math.min(rect.bottom, clip?.bottom ?? Infinity)
      bar.hidden = !block.isConnected || bottom <= top
      bar.style.transform = `translate(${Math.round(editor.left - 16)}px, ${Math.round(top)}px)`
      bar.style.height = `${Math.max(0, Math.round(bottom - top))}px`
    })
    frame = requestAnimationFrame(draw)
  }
  draw()
  return {
    matched: blocks.length,
    stop: () => {
      stopped = true
      cancelAnimationFrame(frame)
      layer.remove()
    },
  }
}
