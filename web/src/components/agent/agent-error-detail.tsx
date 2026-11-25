/* oxlint-disable react/only-export-components -- the error classifier is shared by the loop run page. */
import { useI18n } from '@/i18n/i18n'
import { RotateCw } from 'lucide-react'
import styles from './agent-error-detail.module.css'

type Translate = (source: string) => string

/** What kind of failure an agent or loop error string describes (see agent_provider.go / agent_stream.go for the server text). */
export type AgentErrorKind = 'stopped' | 'rate_limit' | 'auth' | 'timeout' | 'unavailable' | 'empty' | 'turn_limit' | 'context' | 'not_configured' | 'generic'

/** Friendly, translatable copy by error kind. */
export const AGENT_ERROR_MESSAGES: Record<AgentErrorKind, string> = {
  stopped: 'Generation stopped',
  rate_limit: 'The model provider is rate limiting requests. Wait a moment and try again.',
  auth: "The model provider rejected the server's credentials. Ask an admin to check the Flow Agent settings.",
  timeout: 'The model provider took too long to respond. Try again.',
  unavailable: 'The model provider is unavailable right now. Try again later.',
  empty: 'The model returned an empty response. Try again.',
  turn_limit: 'The agent reached its step limit for one reply. Say "Continue" to pick up where it stopped.',
  context: 'The conversation is too long for the model. Start a new chat or shorten the request.',
  not_configured: 'Flow Agent is not configured on this server.',
  generic: 'The agent ran into an error.',
}

/** A saved agent session whose replies could not be read for now (the server was busy or unreachable); not an agent failure. */
export const AGENT_REPLIES_UNAVAILABLE = "Couldn't load the agent's replies right now. Try again in a moment."

/** A person's follow-up (a reply, a choice or an approval) that did not reach the server; the agent did nothing wrong. */
export const AGENT_FOLLOW_UP_NOT_SENT = "Couldn't send your follow-up."

/** A person's request to retry a failed reply that did not reach the server. */
export const AGENT_RETRY_NOT_SENT = "Couldn't ask the agent to try again."

/** Notices the app words itself about a chat it could not read or follow: shown as they are (translated), never as a provider error. */
export const AGENT_APP_NOTICES: ReadonlySet<string> = new Set([
  "Couldn't load this chat.",
  'This chat is no longer available.',
  AGENT_REPLIES_UNAVAILABLE,
  AGENT_FOLLOW_UP_NOT_SENT,
  AGENT_RETRY_NOT_SENT,
])

/** Classifies raw error text: "Flow Agent provider returned status 429: …", "Flow Agent provider stopped responding"… */
export function classifyAgentError(raw: string): AgentErrorKind {
  const text = raw.toLowerCase()
  if (/^generation stopped\b/.test(text)) return 'stopped'
  if (/\bstatus 429\b|rate.?limit|too many requests|insufficient_quota|quota exceeded/.test(text)) return 'rate_limit'
  if (/\bstatus 40[13]\b|unauthori[sz]ed|invalid (?:x-)?api.?key|incorrect api key|authentication|permission denied|forbidden/.test(text)) return 'auth'
  if (/context length|context window|maximum context|prompt is too long|too many tokens|context budget/.test(text)) return 'context'
  if (/\bstatus (?:408|504)\b|timed? ?out|timeout|stopped responding|deadline exceeded/.test(text)) return 'timeout'
  if (/\bstatus 5\d\d\b|provider is unavailable|overloaded|service unavailable|bad gateway|could not read agent stream|connection (?:reset|refused)|eof\b/.test(text)) return 'unavailable'
  if (/empty response/.test(text)) return 'empty'
  if (/tool turn limit/.test(text)) return 'turn_limit'
  if (/not configured/.test(text)) return 'not_configured'
  return 'generic'
}

/** Raw server text (status codes, JSON bodies, multi-line output) that reads badly as a message of its own. */
export function looksRaw(raw: string) {
  return raw.length > 160 || /\bstatus \d{3}\b|[{[]\s*"|\n/.test(raw)
}

/**
 * A friendly, translated message for an agent error and the raw text to show under "Details".
 * Errors with a translation show it; readable English errors stay as they are for English readers; anything else
 * (status codes and JSON bodies, or English text a Chinese reader would see) becomes the friendly message by kind.
 */
export function describeAgentError(raw: string, t: Translate, _locale: string, kind: AgentErrorKind = classifyAgentError(raw)): { message: string; detail?: string } {
  const text = raw.trim()
  if (kind === 'stopped') return { message: t(AGENT_ERROR_MESSAGES.stopped) }
  if (AGENT_APP_NOTICES.has(text)) return { message: t(text) }
  const translated = t(text)
  if (translated !== text) return { message: translated }
  const message = t(AGENT_ERROR_MESSAGES[kind])
  return { message, detail: text && text !== message ? text : undefined }
}

/** The raw server text behind a friendly error, collapsed under a translated "Details" toggle. */
export function ErrorDetails({ className, detail }: { className?: string; detail: string }) {
  const { t } = useI18n()
  return (
    <details className={`${styles.details}${className ? ` ${className}` : ''}`}>
      <summary>{t('Details')}<svg aria-hidden="true" viewBox="0 0 16 16"><path d="M6.5 5.2v5.6a.4.4 0 0 0 .65.3l3.5-2.8a.4.4 0 0 0 0-.6l-3.5-2.8a.4.4 0 0 0-.65.3Z" fill="currentColor"/></svg></summary>
      <code data-i18n-ignore>{detail}</code>
    </details>
  )
}

/**
 * An agent chat error: the translated message, with the raw provider text under "Details". detail replaces that raw
 * text (an app notice has none of its own); retryLabel, already translated, names what the action does.
 */
export function AgentErrorDetail({ className, error, detail: rawDetail, onRetry, retryLabel }: { className?: string; error: string; detail?: string; onRetry?: () => void; retryLabel?: string }) {
  const { locale, t } = useI18n()
  const described = describeAgentError(error, t, locale)
  const detail = rawDetail ?? described.detail
  return (
    <span className={`${styles.error}${className ? ` ${className}` : ''}`}>
      <span data-i18n-ignore>{described.message}</span>
      {detail && <ErrorDetails detail={detail}/>}
      {onRetry && <button className={styles.retry} onClick={onRetry} type="button"><RotateCw size={12}/>{retryLabel ?? t('Retry')}</button>}
    </span>
  )
}
