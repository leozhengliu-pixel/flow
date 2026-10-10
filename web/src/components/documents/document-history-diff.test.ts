import { describe, expect, it } from 'vitest'

import { DIFF_ADDED_MARK, DIFF_REMOVED_MARK, diffDocuments, diffWords, nodeText, wordTokens, type PMNode } from './document-history-diff'

const text = (value: string, marks?: PMNode['marks']): PMNode => ({ type: 'text', text: value, ...(marks ? { marks } : {}) })
const p = (value: string): PMNode => ({ type: 'paragraph', content: value ? [text(value)] : [] })
const h = (value: string, level = 2): PMNode => ({ type: 'heading', attrs: { level }, content: [text(value)] })
const doc = (...content: PMNode[]): PMNode => ({ type: 'doc', content })
const ul = (...items: string[]): PMNode => ({ type: 'bulletList', content: items.map(item => ({ type: 'listItem', content: [p(item)] })) })

/** Flattens inline text into [text, kind] pairs of one block. */
function inline(block: PMNode) {
  return (block.content ?? []).map(node => {
    const mark = node.marks?.find(item => item.type === DIFF_ADDED_MARK || item.type === DIFF_REMOVED_MARK)
    return [node.text, mark ? (mark.type === DIFF_ADDED_MARK ? 'added' : 'removed') : 'same', mark?.attrs?.change] as const
  })
}

describe('wordTokens / diffWords', () => {
  it('keeps whitespace as its own tokens', () => {
    expect(wordTokens('a  b\nc')).toEqual(['a', '  ', 'b', '\n', 'c'])
  })

  it('groups a word-level replacement with the addition first, then the struck deletion', () => {
    const ops = diffWords(wordTokens('the quick fox'), wordTokens('the slow fox'))
    expect(ops.map(op => [op.kind, op.tokens.join('')])).toEqual([['same', 'the '], ['added', 'slow'], ['removed', 'quick'], ['same', ' fox']])
  })

  it('handles pure insertions and empty sides', () => {
    expect(diffWords([], ['a']).map(op => op.kind)).toEqual(['added'])
    expect(diffWords(['a'], []).map(op => op.kind)).toEqual(['removed'])
    expect(diffWords(['a'], ['a']).map(op => op.kind)).toEqual(['same'])
  })
})

describe('diffDocuments', () => {
  it('returns the document untouched without a previous version', () => {
    const next = doc(p('one'))
    expect(diffDocuments(undefined, next)).toEqual({ doc: next, changes: 0 })
    expect(diffDocuments(null, next).changes).toBe(0)
  })

  it('reports no changes for identical documents', () => {
    const a = doc(h('Title'), p('same text'), ul('x', 'y'))
    const result = diffDocuments(a, JSON.parse(JSON.stringify(a)))
    expect(result.changes).toBe(0)
    expect(JSON.stringify(result.doc)).not.toContain('diff')
  })

  it('marks an added block and counts one change', () => {
    const result = diffDocuments(doc(p('one')), doc(p('one'), p('two')))
    expect(result.changes).toBe(1)
    const added = result.doc.content![1]
    expect(added.attrs).toMatchObject({ __diff: 'added', __change: 1 })
    expect(inline(added)).toEqual([['two', 'added', 1]])
    expect(result.doc.content![0].attrs).toBeUndefined()
  })

  it('keeps a removed block in the flow, struck, before the following content', () => {
    const result = diffDocuments(doc(p('one'), p('gone'), p('three')), doc(p('one'), p('three')))
    expect(result.changes).toBe(1)
    expect(result.doc.content!.map(block => nodeText(block))).toEqual(['one', 'gone', 'three'])
    expect(result.doc.content![1].attrs).toMatchObject({ __diff: 'removed', __change: 1 })
    expect(inline(result.doc.content![1])).toEqual([['gone', 'removed', 1]])
  })

  it('diffs an edited paragraph word by word, with the deletion inline', () => {
    const result = diffDocuments(doc(p('The first version has a short paragraph.')), doc(p('The initial version has a short paragraph.')))
    expect(result.changes).toBe(1)
    const block = result.doc.content![0]
    expect(block.attrs).toBeUndefined()
    expect(inline(block)).toEqual([
      ['The ', 'same', undefined],
      ['initial', 'added', 1],
      ['first', 'removed', 1],
      [' version has a short paragraph.', 'same', undefined],
    ])
  })

  it('counts separate runs in one block as separate changes, in document order', () => {
    const result = diffDocuments(doc(p('alpha beta gamma delta epsilon')), doc(p('alpha BETA gamma delta EPSILON zeta')))
    const kinds = inline(result.doc.content![0]).filter(item => item[1] !== 'same')
    expect(result.changes).toBe(3 - 1) // beta->BETA, epsilon->EPSILON zeta
    expect(kinds.map(item => item[2])).toEqual([1, 1, 2, 2])
  })

  it('treats spacing between two changed words as part of one change', () => {
    const result = diffDocuments(doc(p('keep one two keep')), doc(p('keep uno dos keep')))
    expect(result.changes).toBe(1)
  })

  it('numbers block and inline changes sequentially across the document', () => {
    const before = doc(h('Overview'), p('first paragraph here'), p('tail'))
    const after = doc(h('Overview'), p('first paragraph there'), p('tail'), p('brand new'))
    const result = diffDocuments(before, after)
    expect(result.changes).toBe(2)
    expect(inline(result.doc.content![1]).filter(item => item[1] !== 'same').map(item => item[2])).toEqual([1, 1])
    expect(result.doc.content![3].attrs).toMatchObject({ __diff: 'added', __change: 2 })
  })

  it('diffs inside lists item by item', () => {
    const result = diffDocuments(doc(ul('Alpha item', 'Beta item')), doc(ul('Alpha item', 'Beta item two', 'Gamma')))
    expect(result.changes).toBe(2)
    const items = result.doc.content![0].content!
    expect(items).toHaveLength(3)
    expect(items[0].attrs).toBeUndefined()
    expect(inline(items[1].content![0])).toEqual([['Beta item', 'same', undefined], [' two', 'added', 1]])
    expect(items[2].attrs).toMatchObject({ __diff: 'added', __change: 2 })
  })

  it('shows a fully rewritten paragraph as a removal plus an addition', () => {
    const result = diffDocuments(doc(p('alpha beta gamma')), doc(p('one two three four')))
    expect(result.changes).toBe(2)
    expect(result.doc.content![0].attrs).toMatchObject({ __diff: 'removed' })
    expect(result.doc.content![1].attrs).toMatchObject({ __diff: 'added' })
  })

  it('keeps the new text marks on unchanged and added words and the old marks on removed words', () => {
    const bold = [{ type: 'bold' }]
    const before = doc({ type: 'paragraph', content: [text('old ', bold), text('word')] })
    const after = doc({ type: 'paragraph', content: [text('new ', bold), text('word')] })
    const content = diffDocuments(before, after).doc.content![0].content!
    expect(content.find(node => node.text === 'old')?.marks?.map(mark => mark.type)).toEqual(['bold', DIFF_REMOVED_MARK])
    expect(content.find(node => node.text === 'new')?.marks?.map(mark => mark.type)).toEqual(['bold', DIFF_ADDED_MARK])
  })

  it('does not count empty paragraphs as changes', () => {
    expect(diffDocuments(doc(p('a')), doc(p('a'), p(''))).changes).toBe(0)
    expect(diffDocuments(doc(p('a'), p('')), doc(p('a'))).changes).toBe(0)
  })

  it('treats atomic inline nodes (mentions) as one token', () => {
    const mention = (label: string): PMNode => ({ type: 'mention', attrs: { id: label, label, mentionType: 'user' } })
    const before = doc({ type: 'paragraph', content: [text('hi '), mention('ann')] })
    const after = doc({ type: 'paragraph', content: [text('hi '), mention('bob')] })
    const result = diffDocuments(before, after)
    expect(result.changes).toBe(1)
    expect(result.doc.content![0].content!.filter(node => node.type === 'mention').map(node => node.marks?.[0].type)).toEqual([DIFF_ADDED_MARK, DIFF_REMOVED_MARK])
  })

  it('compares tables, quotes and code blocks too', () => {
    const table = (cell: string): PMNode => ({ type: 'table', content: [{ type: 'tableRow', content: [{ type: 'tableCell', content: [p(cell)] }] }] })
    expect(diffDocuments(doc(table('a')), doc(table('a'))).changes).toBe(0)
    expect(diffDocuments(doc(table('a b')), doc(table('a c'))).changes).toBe(1)
    const code = (value: string): PMNode => ({ type: 'codeBlock', content: [text(value)] })
    expect(diffDocuments(doc(code('let a = 1')), doc(code('let a = 2'))).changes).toBe(1)
    const quote = (value: string): PMNode => ({ type: 'blockquote', content: [p(value)] })
    expect(diffDocuments(doc(quote('x y')), doc(quote('x y z'))).changes).toBe(1)
  })

  it('gives up gracefully (whole-block replace) on huge blocks', () => {
    const words = (n: number, tag: string) => Array.from({ length: n }, (_, i) => `${tag}${i}`).join(' ')
    const result = diffDocuments(doc(p(words(2500, 'a'))), doc(p(`${words(2500, 'b')} tail`)))
    expect(result.changes).toBeGreaterThan(0)
  })
})
