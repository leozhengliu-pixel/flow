import { AlignLeft, AtSign, Building2, Clock, Flag, MessageSquareText, Rocket, ShieldCheck, SquarePen, SquarePlus, User as UserIcon } from 'lucide-react'

import { REALTIME_PERMISSION } from '@/lib/oauth-consent'

/** The icon the reference app shows beside each OAuth permission (consent screen and Settings › Applications). */
export function ScopeIcon({ scope }: { scope: string }) {
  if (scope === REALTIME_PERMISSION) return <RealtimeIcon/>
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

/** "Receive realtime updates": a rounded square open at the top right, with a notification dot there (the reference app's glyph). */
function RealtimeIcon() {
  return <svg aria-hidden="true" className="oauth-scope-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round">
    <path d="M11 3H8a5 5 0 0 0-5 5v8a5 5 0 0 0 5 5h8a5 5 0 0 0 5-5v-3"/>
    <circle cx="18.5" cy="5.5" r="2.5" fill="currentColor" stroke="none"/>
  </svg>
}
