import { describe, expect, it } from 'vitest'
import { htmlText } from './html-text'

describe('HTML text extraction', () => {
  it('removes active markup and decodes entities once', () => {
    expect(htmlText('<script>alert(1)</script><img src=x onerror=alert(1)>Hello &amp; goodbye')).toBe('Hello & goodbye')
    expect(htmlText('&amp;lt;script&amp;gt;')).toBe('&lt;script&gt;')
    expect(htmlText('<b>nested</b> &lt;literal&gt;')).toBe('nested <literal>')
  })
})
