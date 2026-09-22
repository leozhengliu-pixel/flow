/**
 * LS-0651 WelcomeMessageInboxView — inbox split host for workspace welcome
 * notifications (type workspaceWelcome).
 */
import { HandMetal } from 'lucide-react'

import './inbox-hosts.css'

export type WelcomeMessageInboxViewProps = {
  workspaceName: string
  welcomeMessage?: string
  orgLogoUrl?: string
  actorName?: string
  onOpenSettings?: () => void
  onDismiss?: () => void
}

export function WelcomeMessageInboxView({
  workspaceName,
  welcomeMessage,
  orgLogoUrl,
  actorName,
  onOpenSettings,
  onDismiss,
}: WelcomeMessageInboxViewProps) {
  const body = welcomeMessage?.trim()
  return (
    <div className="flow-inbox-host flow-inbox-host--welcome" data-surface="LS-0651">
      <header className="flow-inbox-host__title">
        <div className="flow-inbox-host__entity" role="group" aria-label="Welcome message">
          {orgLogoUrl ? (
            <img alt="" className="flow-inbox-host__swatch flow-inbox-host__swatch--logo" src={orgLogoUrl} />
          ) : (
            <span aria-hidden className="flow-inbox-host__swatch flow-inbox-host__swatch--welcome">
              <HandMetal size={16} />
            </span>
          )}
          <div>
            <small>Welcome</small>
            <strong data-i18n-ignore>Welcome to {workspaceName}</strong>
          </div>
        </div>
        <div className="flow-inbox-host__actions">
          {onOpenSettings ? (
            <button className="flow-inbox-host__ghost" onClick={onOpenSettings} type="button">
              Edit welcome message
            </button>
          ) : null}
          {onDismiss ? (
            <button className="flow-inbox-host__primary" onClick={onDismiss} type="button">
              Got it
            </button>
          ) : null}
        </div>
      </header>

      <section className="flow-inbox-host__welcome-card" aria-label="Welcome message content">
        {body ? (
          <p className="flow-inbox-host__welcome-body">{body}</p>
        ) : (
          <p className="flow-inbox-host__muted">
            Watch an introductory video and access a list of resources below. Your workspace admin can
            customize this welcome message in settings.
          </p>
        )}
        {actorName ? (
          <p className="flow-inbox-host__muted">
            From <strong data-i18n-ignore>{actorName}</strong>
          </p>
        ) : null}
      </section>

      <section className="flow-inbox-host__welcome-links" aria-label="Key features">
        <h3>Key features</h3>
        <ul>
          <li>
            <strong>AI &amp; Agents</strong>
            <span>Automate your product development processes and operations</span>
          </li>
          <li>
            <strong>Integration directory</strong>
            <span>Discover connections from support, design, and engineering tools</span>
          </li>
        </ul>
      </section>
    </div>
  )
}
