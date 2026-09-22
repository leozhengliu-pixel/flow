/**
 * LS-0434 OAuthClientApprovalInboxView — inbox split host for third-party
 * application approval requests (type oauthClientApprovalCreated).
 */
import { useEffect, useState } from 'react'
import { ShieldCheck } from 'lucide-react'

import { request, jsonRequest } from '@/lib/api-client'

import './inbox-hosts.css'

export type OAuthClientApprovalPolicy = {
  id: string
  name: string
  kind: 'mcp' | 'oauth'
  url?: string
  status: string
  scopes?: string[]
  ownerId?: string
}

export type OAuthClientApprovalInboxViewProps = {
  /** Policy id from the notification source when known. */
  policyId?: string
  /** Fallback display name before policies load. */
  fallbackName?: string
  actorName?: string
  additionalPermissions?: boolean
  onOpenSettings: () => void
  onResolved?: () => void
}

export function OAuthClientApprovalInboxView({
  policyId,
  fallbackName,
  actorName,
  additionalPermissions = false,
  onOpenSettings,
  onResolved,
}: OAuthClientApprovalInboxViewProps) {
  const [policies, setPolicies] = useState<OAuthClientApprovalPolicy[]>([])
  const [selectedId, setSelectedId] = useState(policyId)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [loadError, setLoadError] = useState('')

  const reload = async () => {
    const items = await request<OAuthClientApprovalPolicy[]>('/api/application-policies')
    setPolicies(items)
    return items
  }

  useEffect(() => {
    let active = true
    void reload()
      .then(items => {
        if (!active) return
        if (policyId && !items.some(item => item.id === policyId)) {
          // Keep a stub so Approve/Decline can still target the notification source.
          setPolicies(current => current.some(item => item.id === policyId)
            ? current
            : [...current, {
                id: policyId,
                name: fallbackName || 'Application approval',
                kind: 'oauth',
                status: 'pending',
              }])
        }
      })
      .catch(reason => {
        if (active) setLoadError(String(reason))
      })
    return () => { active = false }
  }, [fallbackName, policyId])

  useEffect(() => {
    setSelectedId(policyId)
  }, [policyId])

  const policy = policies.find(item => item.id === selectedId)
    ?? (selectedId
      ? {
          id: selectedId,
          name: fallbackName || 'Application approval',
          kind: 'oauth' as const,
          status: 'pending',
        }
      : undefined)

  const otherRequests = policies.filter(
    item => item.id !== policy?.id && item.status !== 'approved' && item.status !== 'rejected',
  )

  const approved = policy?.status === 'approved'
  const declined = policy?.status === 'rejected' || policy?.status === 'declined'
  const pending = Boolean(policy) && !approved && !declined

  const run = async (action: 'approve' | 'decline') => {
    if (!policy || busy) return
    setBusy(true)
    setError('')
    try {
      const next = { ...policy, status: action === 'approve' ? 'approved' : 'rejected' }
      await request('/api/application-policies', jsonRequest('PUT', next))
      setPolicies(current => {
        const exists = current.some(item => item.id === next.id)
        return exists ? current.map(item => item.id === next.id ? next : item) : [...current, next]
      })
      onResolved?.()
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setBusy(false)
    }
  }

  const approveLabel = additionalPermissions ? 'Approve additional' : 'Approve'
  const declineLabel = additionalPermissions ? 'Decline additional' : 'Decline'
  const requestCopy = additionalPermissions
    ? ' is requesting additional permissions for Flow.'
    : ' is requesting access to Flow.'

  return (
    <div className="flow-inbox-host flow-inbox-host--oauth" data-surface="LS-0434">
      <header className="flow-inbox-host__title">
        <div className="flow-inbox-host__entity" role="group" aria-label="Third-party application">
          <span aria-hidden className="flow-inbox-host__swatch flow-inbox-host__swatch--oauth">
            <ShieldCheck size={16} />
          </span>
          <div>
            <small>Third-party application</small>
            <strong data-i18n-ignore>{policy?.name ?? fallbackName ?? 'Application approval'}</strong>
          </div>
        </div>
        <div className="flow-inbox-host__actions">
          <button className="flow-inbox-host__ghost" onClick={onOpenSettings} type="button">
            Open in settings
          </button>
        </div>
      </header>

      {loadError && !policy ? (
        <section className="flow-inbox-host__latest-update is-empty" role="alert">
          <p>Unable to show application details</p>
          <p className="flow-inbox-host__muted">
            Something went wrong when fetching information about this third-party application.
          </p>
          <button className="flow-inbox-host__ghost" onClick={onOpenSettings} type="button">
            Open in settings
          </button>
        </section>
      ) : !policy ? (
        <section className="flow-inbox-host__latest-update is-empty">
          <p className="flow-inbox-host__muted">Loading application details…</p>
        </section>
      ) : (
        <>
          <p className="flow-inbox-host__summary">
            <span data-i18n-ignore>{policy.name}</span>
            {requestCopy}
            {actorName ? (
              <>
                {' '}
                Requested by <strong data-i18n-ignore>{actorName}</strong>.
              </>
            ) : null}
          </p>

          <dl className="flow-inbox-host__properties">
            <div>
              <dt>Kind</dt>
              <dd>{policy.kind === 'mcp' ? 'MCP connector' : 'OAuth application'}</dd>
            </div>
            {policy.scopes?.length ? (
              <div>
                <dt>Scopes</dt>
                <dd data-i18n-ignore>{policy.scopes.join(', ')}</dd>
              </div>
            ) : null}
            {policy.url ? (
              <div>
                <dt>URL</dt>
                <dd data-i18n-ignore>{policy.url}</dd>
              </div>
            ) : null}
            <div>
              <dt>Status</dt>
              <dd>
                <span
                  className={`flow-inbox-host__status-pill is-${approved ? 'approved' : declined ? 'declined' : 'pending'}`}
                >
                  {approved ? 'Approved' : declined ? 'Declined' : 'Pending'}
                </span>
              </dd>
            </div>
          </dl>

          {error ? (
            <p className="flow-inbox-host__error" role="alert">
              {error}
            </p>
          ) : null}

          {pending ? (
            <div className="flow-inbox-host__actions flow-inbox-host__actions--bar">
              <button
                className="flow-inbox-host__ghost"
                disabled={busy}
                onClick={() => void run('decline')}
                type="button"
              >
                {declineLabel}
              </button>
              <button
                className="flow-inbox-host__primary"
                disabled={busy}
                onClick={() => void run('approve')}
                type="button"
              >
                {approveLabel}
              </button>
            </div>
          ) : (
            <p className="flow-inbox-host__muted">
              {approved ? 'Already approved' : 'Declined'}
              {additionalPermissions ? ' • additional requested' : null}
            </p>
          )}
        </>
      )}

      {otherRequests.length ? (
        <section className="flow-inbox-host__other-requests" aria-label="Other requests">
          <h3>Other requests</h3>
          <ul>
            {otherRequests.map(item => (
              <li key={item.id}>
                <button
                  className="flow-inbox-host__request-row"
                  onClick={() => setSelectedId(item.id)}
                  type="button"
                >
                  <strong data-i18n-ignore>{item.name}</strong>
                  <span>{item.status}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  )
}
