import DOMPurify from 'dompurify'

/** Parse sanitized HTML once so encoded markup stays text after entity decoding. */
export function htmlText(value: string): string {
  const safe = DOMPurify.sanitize(value, { ALLOWED_TAGS: [], ALLOWED_ATTR: [] })
  return new DOMParser().parseFromString(safe, 'text/html').body.textContent ?? ''
}
