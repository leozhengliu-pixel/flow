import { encodeDirectiveUrl } from '@/lib/directive-url'

const directiveStart = /^(::file\[[^\]\n]*]|::video\[[^\]\n]*]|!\[[^\]\n]*])\(/
const fileAttrs = /^(?:\{size="[^"]*" type="[^"]*"\})?[ \t]*$/
const lineEnd = /^[ \t]*$/
const fenceLine = /^ {0,3}(`{3,}|~{3,})/

/**
 * Directives the server embedded before attachment URLs were encoded carry the raw URL (`…/uploads/x_a b.docx`), which
 * the tokenizers refuse, so a Markdown-only body showed them as text. This writes such a URL in its directive encoding,
 * and only when it is exactly the URL of one of this issue's own attachments, in a directive that fills its line (the
 * shape the server wrote) and outside code blocks. Any other text, and directives saved as escaped text since (they
 * live in the rich-text state as a paragraph), are left as they are.
 */
export function encodeLegacyAttachmentDirectives(markdown: string, attachments: readonly { url: string }[]) {
  if (!markdown) return markdown
  const urls = attachments
    .map(attachment => attachment.url)
    .filter(url => url?.startsWith('/uploads/') && encodeDirectiveUrl(url) !== url && markdown.includes(`](${url})`))
  if (!urls.length) return markdown
  let fence = ''
  return markdown.split('\n').map(line => {
    const marker = line.match(fenceLine)?.[1]
    if (marker) {
      if (!fence) fence = marker
      else if (marker[0] === fence[0] && marker.length >= fence.length) fence = ''
      return line
    }
    if (fence) return line
    const start = line.match(directiveStart)
    if (!start) return line
    const rest = line.slice(start[0].length)
    for (const url of urls) {
      if (!rest.startsWith(`${url})`)) continue
      const tail = rest.slice(url.length + 1)
      if (!(start[1].startsWith('::file[') ? fileAttrs : lineEnd).test(tail)) continue
      return `${start[0]}${encodeDirectiveUrl(url)})${tail}`
    }
    return line
  }).join('\n')
}
