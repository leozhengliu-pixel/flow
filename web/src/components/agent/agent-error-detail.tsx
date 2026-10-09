/* oxlint-disable react/only-export-components -- the error classifier is shared by the loop run page. */
import { useI18n } from '@/i18n/i18n'
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
  turn_limit: 'The agent used too many steps and stopped. Try a narrower request.',
  context: 'The conversation is too long for the model. Start a new chat or shorten the request.',
  not_configured: 'Flow Agent is not configured on this server.',
  generic: 'The agent ran into an error.',
}

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
export function describeAgentError(raw: string, t: Translate, locale: string, kind: AgentErrorKind = classifyAgentError(raw)): { message: string; detail?: string } {
  const text = raw.trim()
  if (kind === 'stopped') return { message: t(AGENT_ERROR_MESSAGES.stopped) }
  const translated = t(text)
  if (translated !== text) return { message: translated }
  if (!looksRaw(text) && !locale.startsWith('zh')) return { message: text }
  return { message: t(AGENT_ERROR_MESSAGES[kind]), detail: text || undefined }
}

/** The raw server text behind a friendly error, collapsed under a translated "Details" toggle. */
export function ErrorDetails({ className, detail }: { className?: string; detail: string }) {
  const { t } = useI18n()
  return (
    <details className={`${styles.details}${className ? ` ${className}` : ''}`}>
      <summary>{t('Details')}</summary>
      <code data-i18n-ignore>{detail}</code>
    </details>
  )
}

/** An agent chat error: the translated message, with the raw provider text under "Details". */
export function AgentErrorDetail({ className, error }: { className?: string; error: string }) {
  const { locale, t } = useI18n()
  const { message, detail } = describeAgentError(error, t, locale)
  return (
    <span className={`${styles.error}${className ? ` ${className}` : ''}`}>
      <span data-i18n-ignore>{message}</span>
      {detail && <ErrorDetails detail={detail}/>}
    </span>
  )
}
