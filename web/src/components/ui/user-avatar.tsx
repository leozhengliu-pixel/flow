import { useState, type CSSProperties } from 'react'
import { AgentAvatarMark } from '@/components/ui/agent-glyph'
import { isBuiltinAgentAvatarUrl } from '@/components/ui/agent-avatar-url'
import { avatarColor } from '@/lib/avatar-color'
import { avatarInitials } from '@/lib/avatar-initials'
import { FittedInitials } from './fitted-initials'
import './user-avatar.css'

/**
 * An avatar image: the built-in agent's avatar (a url the server assigns to the "Flow" agent member) is drawn as the agent
 * avatar tile instead of the raw image, so every picker, list and activity shows the same agent avatar. `className` sizes it.
 */
export function AvatarImage({ alt = '', className, src, ...props }: { alt?: string; className?: string; src: string; 'aria-label'?: string }) {
  if (isBuiltinAgentAvatarUrl(src)) return <AgentAvatarMark className={className} title={alt || props['aria-label']}/>
  return <img alt={alt} className={className} src={src} {...props}/>
}

/**
 * A person's avatar. Pass `userId` and the initials background is the shared per-person colour (`avatarColor`), the same on
 * every surface; `color` overrides it for an explicit colour (an agent's, a team's).
 */
export function UserAvatar({ avatarUrl, className, color, name, title, userId }: { avatarUrl?: string; className?: string; color?: string; name: string; title?: string; userId?: string }) {
  const initials = avatarInitials(name)
  const script = /[㐀-鿿豈-﫿]/.test(initials) ? 'cjk' : 'latin'
  const background = color ?? (userId ? avatarColor(userId) : undefined)
  const style = background ? ({ '--avatar': background } as CSSProperties) : undefined
  const agent = isBuiltinAgentAvatarUrl(avatarUrl)
  const [imageFailed, setImageFailed] = useState(false)
  const [failedFor, setFailedFor] = useState(avatarUrl)
  if (failedFor !== avatarUrl) {
    setFailedFor(avatarUrl)
    setImageFailed(false)
  }
  const showImage = Boolean(avatarUrl) && !imageFailed
  return <span aria-label={name} className={`user-avatar${className ? ` ${className}` : ''}`} data-user-avatar="" data-agent-avatar-slot={agent ? '' : undefined} data-avatar-fallback={imageFailed ? '' : undefined} data-script={script} data-i18n-ignore style={style} title={title}>{agent ? <AgentAvatarMark/> : showImage ? <img alt="" src={avatarUrl} onError={() => setImageFailed(true)}/> : <FittedInitials className="user-avatar-initials">{initials}</FittedInitials>}</span>
}
