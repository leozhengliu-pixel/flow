import type { ReactNode } from 'react'
import { FileText } from 'lucide-react'
import { ProjectIcon, StatusIcon } from '@/components/issue/issue-icons'
import { avatarColor } from '@/components/issue/avatar-color'
import { UserAvatar } from '@/components/ui/user-avatar'
import type { BootstrapData } from '@/types/flow'
import type { AgentMention } from './agent-mention-input'
import styles from './agent-mention-input.module.css'

/** Icon for a mention chip or option: status, project, document or avatar. */
export function mentionIcon(mention: Pick<AgentMention, 'type' | 'id'>, data: BootstrapData): ReactNode {
  if (mention.type === 'issue') {
    const issue = data.issues.find(item => item.id === mention.id)
    return issue ? <StatusIcon state={issue.state} size={14}/> : null
  }
  if (mention.type === 'project') {
    const project = data.projects.find(item => item.id === mention.id)
    return <ProjectIcon size={14} style={{ color: project?.color }}/>
  }
  if (mention.type === 'user') {
    const user = data.users.find(item => item.id === mention.id)
    return <UserAvatar className={styles.avatar} avatarUrl={user?.avatarUrl} color={avatarColor(mention.id)} name={user?.displayName ?? '?'}/>
  }
  return <FileText size={14}/>
}
