import { describe, expect, it } from 'vitest'

import { nodeText } from './document-history-diff'
import { markdownToDoc, parseInline } from './document-markdown-doc'

describe('markdownToDoc', () => {
  it('turns Markdown into block nodes (no literal newlines survive as text)', () => {
    const doc = markdownToDoc('## Overview\n\nFirst line\nsecond line\n\n- a\n- b\n\n1. one\n2. two\n\n- [x] done\n- [ ] todo\n\n> quote\n\n---\n\n```ts\nconst a = 1\n```')
    expect(doc.content!.map(block => block.type)).toEqual(['heading', 'paragraph', 'bulletList', 'orderedList', 'taskList', 'blockquote', 'horizontalRule', 'codeBlock'])
    expect(doc.content![0].attrs).toEqual({ level: 2 })
    expect(doc.content![1].content!.map(node => node.type)).toEqual(['text', 'hardBreak', 'text'])
    expect(doc.content![4].content!.map(item => item.attrs?.checked)).toEqual([true, false])
    expect(doc.content![7].attrs).toEqual({ language: 'ts' })
    expect(JSON.stringify(doc)).not.toContain('\\\\n')
  })

  it('nests lists by indentation', () => {
    const doc = markdownToDoc('- parent\n  - child\n- next')
    const list = doc.content![0]
    expect(list.content).toHaveLength(2)
    expect(list.content![0].content!.map(node => node.type)).toEqual(['paragraph', 'bulletList'])
  })

  it('parses tables and inline marks', () => {
    const doc = markdownToDoc('| A | B |\n| - | - |\n| **x** | [l](https://e.com) |')
    const table = doc.content![0]
    expect(table.type).toBe('table')
    expect(table.content![0].content!.map(cell => cell.type)).toEqual(['tableHeader', 'tableHeader'])
    expect(nodeText(table.content![1].content![0])).toBe('x')
    expect(parseInline('a **b** *c* ~~d~~ `e`').map(node => node.marks?.[0]?.type ?? 'none')).toEqual(['none', 'bold', 'none', 'italic', 'none', 'strike', 'none', 'code'])
    expect(parseInline('[l](https://e.com)')[0].marks![0]).toEqual({ type: 'link', attrs: { href: 'https://e.com' } })
  })

  it('returns an empty paragraph for empty input', () => {
    expect(markdownToDoc('').content).toEqual([{ type: 'paragraph' }])
  })
})
