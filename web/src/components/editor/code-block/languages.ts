import hljs from 'highlight.js/lib/common'
import clojure from 'highlight.js/lib/languages/clojure'
import dart from 'highlight.js/lib/languages/dart'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import elixir from 'highlight.js/lib/languages/elixir'
import excel from 'highlight.js/lib/languages/excel'
import haskell from 'highlight.js/lib/languages/haskell'
import nginx from 'highlight.js/lib/languages/nginx'
import powershell from 'highlight.js/lib/languages/powershell'
import protobuf from 'highlight.js/lib/languages/protobuf'
import scala from 'highlight.js/lib/languages/scala'

// The "common" set already carries most languages; these round out the picker.
for (const [name, definition] of Object.entries({ clojure, dart, dockerfile, elixir, excel, haskell, nginx, powershell, protobuf, scala })) {
  if (!hljs.getLanguage(name)) hljs.registerLanguage(name, definition)
}

export interface CodeLanguage {
  /** The highlight.js language name that is stored in the document's `language` attribute. */
  id: string
  /** The proper name shown in the picker (not translated). */
  label: string
  /** Extra search terms. */
  keywords?: string
}

/** The languages offered in the picker, after "Auto detect". "Plain text" is translated by the view. */
const LANGUAGES: CodeLanguage[] = [
  { id: 'plaintext', label: 'Plaintext', keywords: 'plain text txt' },
  { id: 'bash', label: 'Bash', keywords: 'sh zsh' },
  { id: 'shell', label: 'Shell', keywords: 'console terminal' },
  { id: 'c', label: 'C' },
  { id: 'cpp', label: 'C++', keywords: 'cpp cc' },
  { id: 'csharp', label: 'C#', keywords: 'cs csharp dotnet' },
  { id: 'css', label: 'CSS' },
  { id: 'clojure', label: 'Clojure', keywords: 'clj' },
  { id: 'dart', label: 'Dart', keywords: 'flutter' },
  { id: 'diff', label: 'Diff', keywords: 'patch' },
  { id: 'dockerfile', label: 'Dockerfile', keywords: 'docker' },
  { id: 'elixir', label: 'Elixir', keywords: 'ex exs' },
  { id: 'excel', label: 'Excel', keywords: 'xlsx formula' },
  { id: 'go', label: 'Golang', keywords: 'go' },
  { id: 'graphql', label: 'GraphQL', keywords: 'gql' },
  { id: 'xml', label: 'HTML', keywords: 'xml svg' },
  { id: 'haskell', label: 'Haskell', keywords: 'hs' },
  { id: 'ini', label: 'INI, TOML', keywords: 'toml config' },
  { id: 'json', label: 'JSON' },
  { id: 'java', label: 'Java' },
  { id: 'javascript', label: 'JavaScript', keywords: 'js jsx node' },
  { id: 'kotlin', label: 'Kotlin', keywords: 'kt' },
  { id: 'less', label: 'Less' },
  { id: 'lua', label: 'Lua' },
  { id: 'makefile', label: 'Makefile', keywords: 'make mk' },
  { id: 'markdown', label: 'Markdown', keywords: 'md' },
  { id: 'nginx', label: 'Nginx', keywords: 'conf' },
  { id: 'objectivec', label: 'Objective-C', keywords: 'objc' },
  { id: 'php', label: 'PHP' },
  { id: 'perl', label: 'Perl', keywords: 'pl' },
  { id: 'powershell', label: 'PowerShell', keywords: 'ps1' },
  { id: 'protobuf', label: 'Protocol Buffers', keywords: 'proto' },
  { id: 'python', label: 'Python', keywords: 'py' },
  { id: 'r', label: 'R' },
  { id: 'ruby', label: 'Ruby', keywords: 'rb' },
  { id: 'rust', label: 'Rust', keywords: 'rs' },
  { id: 'scss', label: 'SCSS', keywords: 'sass' },
  { id: 'sql', label: 'SQL' },
  { id: 'scala', label: 'Scala' },
  { id: 'swift', label: 'Swift' },
  { id: 'typescript', label: 'TypeScript', keywords: 'ts tsx' },
  { id: 'vbnet', label: 'Visual Basic .NET', keywords: 'vb' },
  { id: 'wasm', label: 'WebAssembly', keywords: 'wat' },
  { id: 'yaml', label: 'YAML', keywords: 'yml' },
]

/** The picker's languages after "Auto detect", alphabetical like Linear's list. */
export const CODE_LANGUAGES: readonly CodeLanguage[] = [...LANGUAGES].sort((a, b) => (a.label < b.label ? -1 : a.label > b.label ? 1 : 0))

const IDS = new Set(CODE_LANGUAGES.map(language => language.id))
const LABELS = new Map(CODE_LANGUAGES.map(language => [language.id, language.label]))

/** Maps a stored or typed language (`js`, `language-ts`, `html`, `Python`) to the picker's id, or undefined when unknown. */
export function resolveLanguage(value: string | null | undefined): string | undefined {
  const name = String(value ?? '').trim().toLowerCase().replace(/^language-/, '')
  if (!name || name === 'auto') return undefined
  if (IDS.has(name)) return name
  const definition = hljs.getLanguage(name)
  if (!definition) return undefined
  // hljs resolves aliases (js, html, sh) to one definition; find which picker entry owns it.
  for (const id of IDS) if (hljs.getLanguage(id) === definition) return id
  return undefined
}

/** The picker label for a stored language; unknown names are shown as typed. */
export function languageLabel(value: string | null | undefined): string {
  const id = resolveLanguage(value)
  return id ? LABELS.get(id) ?? id : String(value ?? '')
}

/** Auto-detection is a few milliseconds per KB, so it only runs on small blocks. */
export const AUTO_DETECT_LIMIT = 10_000
/** Beyond this size nothing is highlighted while typing. */
export const HIGHLIGHT_LIMIT = 50_000

// Languages whose definitions match too loosely to be guessed from a snippet (css claims JSON, TypeScript, Python ...).
const NOT_GUESSED = new Set(['plaintext', 'css', 'scss', 'less', 'shell', 'excel', 'ini', 'markdown', 'makefile', 'wasm', 'nginx', 'vbnet', 'dockerfile', 'haskell', 'clojure', 'elixir'])
export const DETECT_SUBSET = CODE_LANGUAGES.map(language => language.id).filter(id => !NOT_GUESSED.has(id))
/** Snippets shorter than this are never guessed. */
export const DETECT_MIN_LENGTH = 12
/** Weakest highlight.js relevance that is trusted as a language guess. */
export const DETECT_MIN_RELEVANCE = 3

const detected = new Map<string, string | null>()

function isJson(code: string) {
  if (!/^\s*[[{]/.test(code)) return false
  try { return typeof JSON.parse(code) === 'object' } catch { return false }
}

function guess(code: string): string | null {
  if (isJson(code)) return 'json'
  const result = hljs.highlightAuto(code, DETECT_SUBSET)
  const id = result.language ? resolveLanguage(result.language) : undefined
  // Code has punctuation; prose that merely contains a keyword or two does not.
  if (id && result.relevance >= DETECT_MIN_RELEVANCE && /[{}()[\];=<>$]/.test(code)) return id
  return /[^{}\s][^{}]*\{\s*[\w-]+\s*:[^{}]*\}/.test(code) ? 'css' : null
}

/** Guesses the language of a snippet with highlight.js; null when it is short, large or inconclusive (shown as Plaintext). */
export function detectLanguage(code: string): string | null {
  if (code.trim().length < DETECT_MIN_LENGTH || code.length >= AUTO_DETECT_LIMIT) return null
  if (detected.has(code)) return detected.get(code) ?? null
  const id = guess(code)
  if (detected.size >= 50) detected.delete(detected.keys().next().value as string)
  detected.set(code, id)
  return id
}

export { hljs }
