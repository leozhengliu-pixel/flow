/**
 * Inbox detail for an agent's reply in a project comment thread (agentReply on a project): the project, the
 * agent's line, the thread it answered and the reply's first line, with a link to the comment on the project's
 * Activity tab. Replying happens in the project thread itself; it shares the document thread host's layout.
 */
import { ArrowUpRight } from 'lucide-react'

import { RichComment } from '@/components/activity/rich-comment'
import { AppLink } from '@/components/ui/app-link'
import { UserAvatar } from '@/components/ui/user-avatar'
import { ProjectGlyph } from '@/components/views/project-glyph'
import { useI18n } from '@/i18n/i18n'
import type { Notification, Project } from '@/types/flow'
import { agentReplyLine, agentReplyProjectPath } from '../agent-reply-notification'
import './document-inbox-view.css'

export function ProjectAgentReplyInboxView({ workspaceKey, project, notification }: { workspaceKey: string; project: Project; notification: Notification }) {
  const { t, formatDate } = useI18n()
  const comments = project.comments ?? []
  const comment = notification.commentId ? comments.find(item => item.id === notification.commentId) : undefined
  const root = comment?.parentId ? comments.find(item => item.id === comment.parentId) : comment
  const thread = root ? [root, ...comments.filter(item => item.parentId === root.id)] : []
  const excerpt = typeof notification.payload?.excerpt === 'string' ? notification.payload.excerpt : ''
  return <div className="flow-inbox-document">
    <header>
      <span className="flow-inbox-document__icon"><ProjectGlyph project={project}/></span>
      <div>
        <small>{t('Project')}</small>
        <h2 data-i18n-ignore>{project.name}</h2>
      </div>
      <AppLink className="flow-inbox-document__open" href={agentReplyProjectPath(workspaceKey, project, notification.commentId)}>{t('Open project')}<ArrowUpRight size={13}/></AppLink>
    </header>
    <p className="flow-inbox-document__line">
      <UserAvatar avatarUrl={notification.actor.avatarUrl} className="flow-inbox-document__avatar" name={notification.actor.displayName || notification.actor.name}/>
      <span>{agentReplyLine({ ...notification, payload: { ...notification.payload, excerpt: '' } }, t)}</span>
      <time dateTime={notification.updatedAt}>{formatDate(notification.updatedAt, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}</time>
    </p>
    {thread.length > 0 && <div className="flow-inbox-document__thread">{thread.map(item => <article key={item.id} className={item.id === notification.commentId ? 'is-target' : undefined}>
      <header><UserAvatar avatarUrl={item.user.avatarUrl} className="flow-inbox-document__avatar" name={item.user.displayName || item.user.name}/><strong data-i18n-ignore>{item.user.displayName || item.user.name}</strong><time dateTime={item.createdAt}>{formatDate(item.createdAt, { month: 'short', day: 'numeric' })}</time></header>
      <RichComment body={item.body} data={item.bodyData}/>
    </article>)}</div>}
    {excerpt && <p className="flow-inbox-document__excerpt" data-i18n-ignore>{excerpt}</p>}
  </div>
}
