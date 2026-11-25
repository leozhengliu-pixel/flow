import { describe, expect, it } from 'vitest'
import { directiveUrlKey, encodeDirectiveUrl, sameDirectiveUrl } from './directive-url'

describe('directive URL encoding', () => {
  // The same table as api/cmd/server/attachment_directive_url_test.go: both sides write one form.
  const cases: [string, string][] = [
    ['/uploads/attachment_1_notes.txt', '/uploads/attachment_1_notes.txt'],
    ['/uploads/attachment_1_测试 report.docx', '/uploads/attachment_1_测试%20report.docx'],
    ['/uploads/attachment_1_测试报告.docx', '/uploads/attachment_1_测试报告.docx'],
    ['/uploads/attachment_1_report (1).docx', '/uploads/attachment_1_report%20%281%29.docx'],
    ['/uploads/attachment_1_100% done.pdf', '/uploads/attachment_1_100%25%20done.pdf'],
    ['/uploads/attachment_1_a%20b.pdf', '/uploads/attachment_1_a%20b.pdf'],
    ['/uploads/attachment_1_a%2.pdf', '/uploads/attachment_1_a%252.pdf'],
    ['/uploads/attachment_1_end%', '/uploads/attachment_1_end%25'],
    ['/uploads/attachment_1_tab\tnew\nline.txt', '/uploads/attachment_1_tab%09new%0Aline.txt'],
    ['/uploads/attachment_1_全角　空格.txt', '/uploads/attachment_1_全角%E3%80%80空格.txt'],
    ['/uploads/attachment_1_nb sp.txt', '/uploads/attachment_1_nb%C2%A0sp.txt'],
    ['/uploads/attachment_1_<a>`b`\\c.txt', '/uploads/attachment_1_%3Ca%3E%60b%60%5Cc.txt'],
    ['/uploads/attachment_1_[x] {y} "q" é#?.txt', '/uploads/attachment_1_[x]%20{y}%20"q"%20é#?.txt'],
    ['https://cdn.example.test/clip.mp4?w=1&h=2', 'https://cdn.example.test/clip.mp4?w=1&h=2'],
    ['', ''],
  ]

  it.each(cases)('encodes %j once and idempotently', (raw, want) => {
    const encoded = encodeDirectiveUrl(raw)
    expect(encoded).toBe(want)
    expect(encodeDirectiveUrl(encoded)).toBe(encoded)
    expect(encoded).not.toMatch(/[\s()]/)
  })

  it('matches an encoded directive URL to its raw attachment URL', () => {
    for (const raw of ['/uploads/attachment_1_测试 report.docx', '/uploads/attachment_1_report (1).docx', '/uploads/attachment_1_100% done.pdf']) {
      expect(directiveUrlKey(encodeDirectiveUrl(raw))).toBe(raw)
      expect(sameDirectiveUrl(encodeDirectiveUrl(raw), raw)).toBe(true)
    }
    expect(sameDirectiveUrl('/uploads/a.pdf', '/uploads/b.pdf')).toBe(false)
  })
})
