import { RichComment } from '@/components/activity/rich-comment'
import './mention-body.css'

/**
 * A stored body (customer request, update, description history, notification detail) shown read-only with its references as
 * mention chips: links to workspace resources, bare URLs, team-key identifiers and @names, like in a comment.
 * `panel` frames it like the plain-text panels it replaces.
 */
export function MentionBody({ body, className, data, panel = false }: { body: string; className?: string; data?: Record<string, unknown>; panel?: boolean }) {
  return <div className={[className, panel ? 'mention-body--panel' : ''].filter(Boolean).join(' ') || undefined} data-i18n-ignore data-mention-body><RichComment body={body} data={data}/></div>
}
