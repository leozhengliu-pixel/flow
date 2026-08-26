import type { CSSProperties } from 'react'

export function UserAvatar({ avatarUrl, className, color, name, title }: { avatarUrl?: string; className?: string; color?: string; name: string; title?: string }) {
  const initials = name.split(/\s|@/).filter(Boolean).slice(0, 2).map(part => part[0]).join('').toUpperCase() || '?'
  const script = /[\u3400-\u9fff\uf900-\ufaff]/.test(initials) ? 'cjk' : 'latin'
  const style = color ? ({ '--avatar': color } as CSSProperties) : undefined
  return <span aria-label={name} className={className} data-script={script} data-i18n-ignore style={style} title={title}>{avatarUrl ? <img alt="" src={avatarUrl}/> : initials}</span>
}
