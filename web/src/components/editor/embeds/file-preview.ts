import { ApiError, request } from '@/lib/api-client'
import { highlightCode, type TokenRange } from '../code-block/code-block-highlight'

/** GET /api/integrations/file-preview: the lines of a GitHub / GitLab file (or of its #L range). */
export type FilePreview = {
  provider: 'github' | 'gitlab'
  /** owner/repo (GitHub) or the project path (GitLab). */
  repo: string
  path: string
  ref: string
  /** The code block highlighter's language id; empty when the server could not tell (the client auto-detects). */
  language: string
  lines: string[]
  /** 1-based line numbers of `lines` (0 for an empty file). */
  startLine: number
  endLine: number
  totalLines: number
  /** More lines (or longer lines) exist than the preview returned. */
  truncated: boolean
  htmlUrl: string
}

export type FilePreviewErrorCode = 'invalid_url' | 'not_connected' | 'no_access' | 'too_large' | 'unsupported' | 'rate_limited' | 'out_of_range' | 'upstream'

const KNOWN_CODES = new Set<FilePreviewErrorCode>(['invalid_url', 'not_connected', 'no_access', 'too_large', 'unsupported', 'rate_limited', 'out_of_range', 'upstream'])

/** Matches the server's short cache, so a document with the same link twice (or a re-render) asks once. */
const TTL = 60_000
const cache = new Map<string, { at: number; promise: Promise<FilePreview> }>()

export function fetchFilePreview(src: string, options: { force?: boolean } = {}): Promise<FilePreview> {
  const cached = cache.get(src)
  if (cached && !options.force && Date.now() - cached.at < TTL) return cached.promise
  const promise = request<FilePreview>(`/api/integrations/file-preview?url=${encodeURIComponent(src)}`)
  const entry = { at: Date.now(), promise }
  cache.set(src, entry)
  // Failures are not kept: a retry (or connecting the integration) asks again.
  promise.catch(() => { if (cache.get(src) === entry) cache.delete(src) })
  if (cache.size > 100) cache.delete(cache.keys().next().value as string)
  return promise
}

export function resetFilePreviewCache() {
  cache.clear()
}

export function filePreviewErrorCode(error: unknown): FilePreviewErrorCode {
  const code = error instanceof ApiError ? error.code : undefined
  return code && KNOWN_CODES.has(code as FilePreviewErrorCode) ? code as FilePreviewErrorCode : 'upstream'
}

export type CodeSegment = { text: string; className?: string }

/**
 * Splits highlight.js token ranges over the joined text into per-line segments. Token ranges nest (a title inside a
 * function), so the open tokens are kept on a stack and each segment gets every enclosing class.
 */
export function segmentLines(text: string, ranges: TokenRange[]): CodeSegment[][] {
  const sorted = [...ranges].sort((a, b) => a.from - b.from || b.to - a.to)
  const lines: CodeSegment[][] = [[]]
  const stack: TokenRange[] = []
  let pos = 0
  let next = 0
  const emit = (end: number) => {
    while (pos < end) {
      const newline = text.indexOf('\n', pos)
      const stop = newline >= 0 && newline < end ? newline : end
      if (stop > pos) {
        const className = stack.map(range => range.className).join(' ')
        lines[lines.length - 1].push(className ? { text: text.slice(pos, stop), className } : { text: text.slice(pos, stop) })
      }
      pos = stop
      if (newline >= 0 && newline < end) {
        lines.push([])
        pos = newline + 1
      }
    }
  }
  for (;;) {
    const top = stack[stack.length - 1]
    const nextStart = next < sorted.length ? sorted[next].from : Infinity
    const nextEnd = top ? top.to : Infinity
    if (nextStart === Infinity && nextEnd === Infinity) {
      emit(text.length)
      break
    }
    if (nextEnd <= nextStart) {
      emit(Math.min(nextEnd, text.length))
      stack.pop()
    } else {
      emit(Math.min(nextStart, text.length))
      stack.push(sorted[next++])
    }
  }
  return lines
}

/** The preview's lines with the code block's syntax highlighting (plain lines when the language is unknown or the file is large). */
export function highlightedLines(lines: string[], language: string): CodeSegment[][] {
  const text = lines.join('\n')
  const segments = segmentLines(text, highlightCode(text, language || 'auto'))
  while (segments.length < lines.length) segments.push([])
  return segments.slice(0, lines.length)
}
