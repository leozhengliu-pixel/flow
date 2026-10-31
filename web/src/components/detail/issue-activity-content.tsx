import type { ReactNode } from 'react'

export function IssueActivityContent({ phone, children, composer }: { phone: boolean; children: ReactNode; composer: ReactNode }) {
  if (!phone) return <>{children}{composer}</>
  return <div className="issue-activity-mobile">{children}<form className="issue-comment-form" onSubmit={event => event.preventDefault()}>{composer}</form></div>
}
