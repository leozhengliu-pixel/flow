/** One uploaded file of a message, as the composer and a sent bubble show it. */
export type AgentBlock =
  | { type: 'image'; src: string; alt: string }
  | { type: 'video'; src: string; name: string }
  | { type: 'file'; src: string; name: string; size: number; contentType: string }
  | { type: 'embed'; src: string }

export type AgentContentPart = { type: 'text'; text: string } | AgentBlock

// ![name](url) | ::video[name](url) | ::file[name](url){size="n" type="mime"}, as in the issue editor; ::embed[url](url) is a player / card the author chose to embed (the label keeps it a readable link for the agent and API).
const BLOCK = /!\[([^\]\n]*)\]\(([^)\s]+)\)|::video\[([^\]\n]*)\]\(([^)\s]+)\)|::file\[([^\]\n]*)\]\(([^)\s]+)\)(?:\{size="([^"]*)" type="([^"]*)"\})?|::embed\[[^\]\n]*\]\((https?:\/\/[^)\s]+)\)/g

const label = (name: string) => name.replace(/[[\]\n]/g, '')

/** A block's Markdown, on its own line. */
export function agentBlockMarkdown(block: { kind: 'image' | 'video' | 'file' | 'embed'; name: string; src: string; size?: number | string; type?: string }) {
  const name = label(block.name)
  if (block.kind === 'image') return `\n![${name}](${block.src})\n`
  if (block.kind === 'embed') return `\n::embed[${label(block.src)}](${block.src})\n`
  if (block.kind === 'video') return `\n::video[${name}](${block.src})\n`
  return `\n::file[${name}](${block.src}){size="${Number(block.size) || 0}" type="${(block.type ?? '').replace(/"/g, '')}"}\n`
}

/** A message split into its text and the images, videos and files in it (pasted or attached), in order. */
export function splitAgentBlocks(content: string): AgentContentPart[] {
  const parts: AgentContentPart[] = []
  let last = 0
  for (const match of content.matchAll(BLOCK)) {
    // Only uploads and web addresses become blocks; anything else (a javascript: link) stays plain text.
    if (!/^(\/uploads\/|https?:\/\/)/.test(match[2] ?? match[4] ?? match[6] ?? match[9] ?? '')) continue
    const text = content.slice(last, match.index).trim()
    if (text) parts.push({ type: 'text', text })
    if (match[2] !== undefined) parts.push({ type: 'image', alt: match[1], src: match[2] })
    else if (match[9] !== undefined) parts.push({ type: 'embed', src: match[9] })
    else if (match[4] !== undefined) parts.push({ type: 'video', name: match[3], src: match[4] })
    else parts.push({ type: 'file', name: match[5], src: match[6], size: Number(match[7]) || 0, contentType: match[8] ?? '' })
    last = match.index + match[0].length
  }
  const rest = content.slice(last).trim()
  if (rest || !parts.length) parts.push({ type: 'text', text: rest })
  return parts
}

export function hasAgentBlocks(content: string) {
  return splitAgentBlocks(content).some(part => part.type !== 'text')
}
