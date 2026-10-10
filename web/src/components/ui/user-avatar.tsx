import type { CSSProperties } from 'react'
import { AgentAvatarMark } from '@/components/ui/agent-glyph'
import { isBuiltinAgentAvatarUrl } from '@/components/ui/agent-avatar-url'

/**
 * An avatar image: the built-in agent's avatar (a url the server assigns to the "Flow" agent member) is drawn as the agent
 * avatar tile instead of the raw image, so every picker, list and activity shows the same agent avatar. `className` sizes it.
 */
export function AvatarImage({ alt = '', className, src, ...props }: { alt?: string; className?: string; src: string; 'aria-label'?: string }) {
  if (isBuiltinAgentAvatarUrl(src)) return <AgentAvatarMark className={className} title={alt || props['aria-label']}/>
  return <img alt={alt} className={className} src={src} {...props}/>
}

export function UserAvatar({ avatarUrl, className, color, name, title }: { avatarUrl?: string; className?: string; color?: string; name: string; title?: string }) {
  const initials = name.split(/\s|@/).filter(Boolean).slice(0, 2).map(part => part[0]).join('').toUpperCase() || '?'
  const script = /[㐀-鿿豈-﫿]/.test(initials) ? 'cjk' : 'latin'
  const style = color ? ({ '--avatar': color } as CSSProperties) : undefined
  const agent = isBuiltinAgentAvatarUrl(avatarUrl)
  return <span aria-label={name} className={className} data-agent-avatar-slot={agent ? '' : undefined} data-script={script} data-i18n-ignore style={style} title={title}>{agent ? <AgentAvatarMark/> : avatarUrl ? <img alt="" src={avatarUrl}/> : initials}</span>
}
