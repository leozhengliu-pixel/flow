/** Split a reply into chat prose and the fenced draft block (```update … ```); an unfinished block is hidden too. */
export function splitAgentDraft(content: string, fence?: string): { prose: string; draft?: string } {
  if (!fence) return { prose: content }
  const pattern = new RegExp('```' + fence + '[^\\S\\n]*\\n?([\\s\\S]*?)(```|$)')
  const match = content.match(pattern)
  if (!match) return { prose: content }
  const prose = content.replace(match[0], '').replace(/\n{3,}/g, '\n\n').trim()
  return { prose, draft: match[2] === '```' ? match[1].trim() || undefined : undefined }
}
