import { ALERT_CALLOUTS } from './markdown-to-doc'

/**
 * Rewrites the html GitHub puts on the clipboard (rendered Markdown in READMEs, issues, PRs and comments) into the
 * shapes the editor's own `parseHTML` rules understand, before ProseMirror parses it:
 *   - `li.task-list-item` + checkbox  ->  `ul[data-type=taskList] > li[data-type=taskItem][data-checked]`
 *   - `div.highlight-source-js > pre` ->  `pre > code.language-js`
 *   - `div.markdown-alert-note`       ->  `aside[data-callout][data-color][data-icon]`
 *   - tables                          ->  a header row of `th` cells (GitHub always renders one)
 *   - heading anchors and octicon svgs are removed, camo image proxies fall back to the canonical URL
 *   - `<details><summary>`            ->  summary + `div[data-type=detailsContent]`
 * Html without GitHub markers is returned untouched.
 */

const GITHUB_MARKERS = /markdown-body|task-list-item|contains-task-list|highlight-source-|highlight-text-|markdown-alert|markdown-heading|octicon|data-snippet-clipboard-copy-content|user-content-|js-file-line|<g-emoji/i

export function isGithubHtml(html: string) {
  return GITHUB_MARKERS.test(html)
}

const ALERT_KINDS = Object.keys(ALERT_CALLOUTS) as (keyof typeof ALERT_CALLOUTS)[]

function languageOf(element: Element): string {
  for (const name of element.className.split(/\s+/)) {
    const source = /^highlight-source-(.+)$/.exec(name)
    if (source) return source[1]
    const text = /^highlight-text-(?:html-)?(.+)$/.exec(name)
    if (text) return text[1] === 'basic' ? 'html' : text[1]
    const language = /^language-(.+)$/.exec(name)
    if (language) return language[1]
  }
  return element.getAttribute('data-lang') ?? ''
}

function replaceWithChildren(element: Element) {
  element.replaceWith(...Array.from(element.childNodes))
}

function normalizeCode(document: Document) {
  for (const wrapper of Array.from(document.querySelectorAll('div.highlight, div[class*="highlight-"], div.snippet-clipboard-content'))) {
    const pre = wrapper.querySelector('pre')
    if (!pre) continue
    const language = languageOf(wrapper) || languageOf(pre) || (pre.querySelector('code') ? languageOf(pre.querySelector('code')!) : '')
    const code = document.createElement('code')
    if (language) code.className = `language-${language}`
    code.textContent = pre.textContent?.replace(/\n$/, '') ?? ''
    const replacement = document.createElement('pre')
    replacement.append(code)
    wrapper.replaceWith(replacement)
  }
  for (const pre of Array.from(document.querySelectorAll('pre'))) {
    const inner = pre.querySelector('code')
    if (inner && !inner.className) {
      const language = languageOf(pre)
      if (language) inner.className = `language-${language}`
    } else if (!inner) {
      const code = document.createElement('code')
      const language = languageOf(pre)
      if (language) code.className = `language-${language}`
      code.textContent = pre.textContent ?? ''
      pre.replaceChildren(code)
    }
  }
}

function checkboxOf(item: Element): HTMLInputElement | null {
  for (const child of Array.from(item.children)) {
    if (child instanceof HTMLInputElement && child.type === 'checkbox') return child
    // `<p><input> text</p>` in loose lists
    if (child.tagName === 'P' && child.firstElementChild instanceof HTMLInputElement && child.firstElementChild.type === 'checkbox') return child.firstElementChild
  }
  return null
}

function normalizeTasks(document: Document) {
  const lists = new Set<Element>()
  for (const item of Array.from(document.querySelectorAll('li'))) {
    const checkbox = item.classList.contains('task-list-item') || checkboxOf(item) ? checkboxOf(item) : null
    if (!checkbox) continue
    const checked = checkbox.checked || checkbox.hasAttribute('checked')
    checkbox.remove()
    item.setAttribute('data-type', 'taskItem')
    item.setAttribute('data-checked', checked ? 'true' : 'false')
    const body = document.createElement('div')
    const nodes = Array.from(item.childNodes)
    let inline: ChildNode[] = []
    const flushInline = () => {
      if (!inline.length || inline.every(node => node.nodeType === Node.TEXT_NODE && !node.textContent?.trim())) {
        inline = []
        return
      }
      const paragraph = document.createElement('p')
      paragraph.append(...inline)
      body.append(paragraph)
      inline = []
    }
    for (const node of nodes) {
      if (node instanceof HTMLElement && /^(P|UL|OL|DIV|PRE|BLOCKQUOTE|TABLE|H[1-6]|DETAILS)$/.test(node.tagName)) {
        flushInline()
        body.append(node)
      } else {
        inline.push(node)
      }
    }
    flushInline()
    if (!body.firstElementChild || body.firstElementChild.tagName !== 'P') body.prepend(document.createElement('p'))
    item.replaceChildren(body)
    const list = item.parentElement
    if (list) lists.add(list)
  }
  for (const list of lists) {
    // A list is a task list when every item became a task item (mixed lists keep their bullets).
    const items = Array.from(list.children)
    if (items.length && items.every(child => child.getAttribute('data-type') === 'taskItem')) {
      if (list.tagName === 'OL') {
        const bullets = document.createElement('ul')
        bullets.append(...items)
        list.replaceWith(bullets)
        bullets.setAttribute('data-type', 'taskList')
      } else {
        list.setAttribute('data-type', 'taskList')
      }
    } else {
      for (const child of items) {
        if (child.getAttribute('data-type') !== 'taskItem') continue
        child.removeAttribute('data-type')
        child.removeAttribute('data-checked')
        child.replaceChildren(...Array.from(child.querySelector('div')?.childNodes ?? []))
      }
    }
  }
}

function normalizeAlerts(document: Document) {
  for (const alert of Array.from(document.querySelectorAll('div.markdown-alert'))) {
    const kind = ALERT_KINDS.find(name => alert.classList.contains(`markdown-alert-${name.toLowerCase()}`))
    if (!kind) continue
    alert.querySelector('.markdown-alert-title')?.remove()
    const aside = document.createElement('aside')
    aside.setAttribute('data-callout', '')
    aside.setAttribute('data-color', ALERT_CALLOUTS[kind].color)
    aside.setAttribute('data-icon', ALERT_CALLOUTS[kind].icon)
    aside.append(...Array.from(alert.childNodes))
    if (!aside.textContent?.trim() && !aside.querySelector('img,pre,table')) aside.append(document.createElement('p'))
    alert.replaceWith(aside)
  }
}

function normalizeTables(document: Document) {
  for (const table of Array.from(document.querySelectorAll('table'))) {
    const rows = Array.from(table.querySelectorAll('tr'))
    if (!rows.length) continue
    if (table.querySelector('th')) {
      for (const row of rows) if (row.closest('thead')) for (const cell of Array.from(row.children)) if (cell.tagName === 'TD') renameElement(document, cell, 'th')
      continue
    }
    for (const cell of Array.from(rows[0].children)) if (cell.tagName === 'TD') renameElement(document, cell, 'th')
  }
}

function renameElement(document: Document, element: Element, tag: string) {
  const next = document.createElement(tag)
  for (const attribute of Array.from(element.attributes)) next.setAttribute(attribute.name, attribute.value)
  next.append(...Array.from(element.childNodes))
  element.replaceWith(next)
}

function normalizeDetails(document: Document) {
  for (const details of Array.from(document.querySelectorAll('details'))) {
    let summary = Array.from(details.children).find(child => child.tagName === 'SUMMARY')
    if (!summary) {
      summary = document.createElement('summary')
      summary.textContent = 'Details'
      details.prepend(summary)
    }
    summary.textContent = summary.textContent?.replace(/\s+/g, ' ').trim() ?? ''
    if (details.querySelector(':scope > div[data-type="detailsContent"]')) continue
    const content = document.createElement('div')
    content.setAttribute('data-type', 'detailsContent')
    for (const node of Array.from(details.childNodes)) if (node !== summary) content.append(node)
    if (!content.firstElementChild || !/^(P|UL|OL|DIV|PRE|BLOCKQUOTE|TABLE|H[1-6]|ASIDE|DETAILS|HR)$/.test(content.firstElementChild.tagName)) {
      const paragraph = document.createElement('p')
      paragraph.append(...Array.from(content.childNodes))
      content.replaceChildren(paragraph)
    }
    details.append(content)
  }
}

function cleanup(document: Document) {
  document.querySelectorAll('svg, .octicon, meta, style, script, link, template, .zeroclipboard-container, clipboard-copy').forEach(node => node.remove())
  for (const anchor of Array.from(document.querySelectorAll('a.anchor, a[aria-label^="Permalink"], a[href^="#user-content-"][aria-hidden]'))) anchor.remove()
  for (const heading of Array.from(document.querySelectorAll('div.markdown-heading'))) replaceWithChildren(heading)
  for (const emoji of Array.from(document.querySelectorAll('g-emoji'))) emoji.replaceWith(document.createTextNode(emoji.textContent ?? ''))
  for (const picture of Array.from(document.querySelectorAll('picture'))) {
    const image = picture.querySelector('img')
    if (image) picture.replaceWith(image)
  }
  for (const image of Array.from(document.querySelectorAll('img'))) {
    const canonical = image.getAttribute('data-canonical-src')
    if (canonical) image.setAttribute('src', canonical)
    image.removeAttribute('srcset')
  }
  // `<a><img></a>` badges: keep the image, drop the wrapping link so it is not lost when the image is a block.
  for (const link of Array.from(document.querySelectorAll('a'))) {
    if (link.children.length === 1 && link.firstElementChild?.tagName === 'IMG' && !link.textContent?.trim()) link.replaceWith(link.firstElementChild)
  }
}

export function normalizeGithubHtml(html: string): string {
  if (!isGithubHtml(html)) return html
  try {
    const document = new DOMParser().parseFromString(html, 'text/html')
    cleanup(document)
    normalizeCode(document)
    normalizeAlerts(document)
    normalizeTasks(document)
    normalizeTables(document)
    normalizeDetails(document)
    return document.body.innerHTML
  } catch {
    return html
  }
}
