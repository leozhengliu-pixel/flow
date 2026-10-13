import { AlignLeft, AtSign, Building2, Clock, Flag, MessageSquareText, Rocket, ShieldCheck, SquarePen, SquarePlus, User as UserIcon } from 'lucide-react'

/** The icon the reference app shows beside each OAuth permission (consent screen and Settings › Applications). */
export function ScopeIcon({ scope }: { scope: string }) {
  const Icon = scope === 'read' ? AlignLeft
    : scope === 'write' ? SquarePen
    : scope === 'admin' ? ShieldCheck
    : scope === 'create_issues' ? SquarePlus
    : scope === 'create_comments' ? MessageSquareText
    : scope === 'timeSchedule:write' ? Clock
    : scope === 'app:assignable' ? UserIcon
    : scope === 'app:mentionable' ? AtSign
    : scope.startsWith('initiative:') ? Flag
    : scope.startsWith('customer:') ? Building2
    : scope.startsWith('release') ? Rocket
    : AlignLeft
  return <Icon aria-hidden="true" className="oauth-scope-icon"/>
}
