import { AgentRichText } from '@/components/agent/agent-rich-text'

/**
 * LS-0738 / B11 MarkdownViewer — dedicated read-only markdown surface.
 * Hydration of linked models runs inside AgentRichText via useHydrateModelsInMarkdown.
 */
export function MarkdownViewer({
  markdown,
  className = 'flow-markdown-viewer',
  'aria-label': ariaLabel = 'AI message',
}: {
  markdown: string
  className?: string
  'aria-label'?: string
}) {
  return <AgentRichText ariaLabel={ariaLabel} className={className} content={markdown} />
}
