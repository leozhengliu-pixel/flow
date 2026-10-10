import { ChevronDown, ChevronUp, ExternalLink, FileCode2, RotateCw } from 'lucide-react'
import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { useI18n } from '@/i18n/i18n'
import { COLLAPSE_THRESHOLD, COLLAPSED_LINES } from '../code-block/code-block-view'
import { repoFileForUrl } from './embed-providers'
import { fetchFilePreview, filePreviewErrorCode, highlightedLines, type FilePreview, type FilePreviewErrorCode } from './file-preview'
import styles from './embed.module.css'
import '../code-block/code-block.css'

const GITHUB_PATH = 'M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27s1.36.09 2 .27c1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8Z'
const GITLAB_PATH = 'm15.73 6.34-.02-.06-2.17-5.66a.57.57 0 0 0-.22-.27.58.58 0 0 0-.66.04.58.58 0 0 0-.19.29l-1.47 4.49H4.99L3.52.69a.57.57 0 0 0-.19-.29.58.58 0 0 0-.66-.04.57.57 0 0 0-.22.27L.28 6.28l-.02.06a4.03 4.03 0 0 0 1.34 4.66l.01.01.02.01 3.31 2.48 1.64 1.24 1 .75a.67.67 0 0 0 .81 0l1-.75 1.64-1.24 3.33-2.5.01-.01a4.03 4.03 0 0 0 1.34-4.65Z'

export function RepoMark({ provider }: { provider: string }) {
  return (
    <svg aria-hidden="true" className={styles.mark} height="16" viewBox="0 0 16 16" width="16">
      <path d={provider === 'gitlab' ? GITLAB_PATH : GITHUB_PATH} fill="currentColor"/>
    </svg>
  )
}

type PreviewState = { status: 'loading' } | { status: 'ready'; preview: FilePreview } | { status: 'error'; code: FilePreviewErrorCode }

function useFilePreview(src: string) {
  const [state, setState] = useState<PreviewState>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let live = true
    setState({ status: 'loading' })
    fetchFilePreview(src, { force: attempt > 0 }).then(
      preview => { if (live) setState({ status: 'ready', preview }) },
      error => { if (live) setState({ status: 'error', code: filePreviewErrorCode(error) }) },
    )
    return () => { live = false }
  }, [src, attempt])
  return { state, retry: () => setAttempt(value => value + 1) }
}

function rangeLabel(start?: number, end?: number) {
  if (!start) return ''
  return end && end !== start ? `L${start}-L${end}` : `L${start}`
}

function integrationSettingsHref(provider: string) {
  const workspace = typeof window === 'undefined' ? '' : window.location.pathname.split('/').filter(Boolean)[0] ?? ''
  return workspace ? `/${workspace}/settings/integrations/${provider}` : `/settings/integrations/${provider}`
}

const ERROR_COPY: Record<FilePreviewErrorCode, { title: string; detail?: string }> = {
  invalid_url: { title: 'This link cannot be previewed' },
  not_connected: { title: 'Connect {provider} to preview this file', detail: 'The file is private or could not be found without the {provider} integration.' },
  no_access: { title: 'No access to this file', detail: 'The file does not exist, or the connected {provider} integration cannot read it.' },
  too_large: { title: 'This file is too large to preview' },
  unsupported: { title: 'Only text files can be previewed' },
  out_of_range: { title: 'The linked lines are outside the file' },
  rate_limited: { title: '{provider} is rate limiting requests', detail: 'Try again in a moment.' },
  upstream: { title: 'Could not load the file preview' },
}

/**
 * Linear's "Embed file preview": a GitHub or GitLab file link drawn as a card with the file's path, repository and ref
 * and its (highlighted, line-numbered) contents, limited to the linked #L range. Long previews collapse like code blocks.
 */
export function FilePreviewCard({ src, provider, compactHeader }: { src: string; provider: string; /** The embed toolbar floats over the header's right edge and carries Open link, so the header drops its own link. */ compactHeader?: boolean }) {
  const { t } = useI18n()
  const file = repoFileForUrl(src, provider)
  const providerName = provider === 'gitlab' ? 'GitLab' : 'GitHub'
  const { state, retry } = useFilePreview(src)
  const [showAll, setShowAll] = useState(false)
  const preview = state.status === 'ready' ? state.preview : undefined
  const lines = useMemo(() => preview ? highlightedLines(preview.lines, preview.language) : [], [preview])
  const path = preview?.path ?? file?.path ?? src
  const repo = preview?.repo ?? (file ? `${file.owner}/${file.repo}` : providerName)
  const ref = preview?.ref ?? file?.ref ?? ''
  // Only a linked #L range is labelled; the server's (clamped) numbers win once loaded.
  const range = file?.startLine ? rangeLabel(preview?.startLine || file.startLine, preview?.endLine || file.endLine) : ''
  const href = preview?.htmlUrl ?? src
  const collapsible = lines.length > COLLAPSE_THRESHOLD
  const expanded = !collapsible || showAll
  const name = (text: string) => text.replaceAll('{provider}', providerName)
  const copy = state.status === 'error' ? ERROR_COPY[state.code] : undefined

  return (
    <div className={`flow-code-block ${styles.preview}`} data-collapsed={preview && !expanded ? '' : undefined} data-file-preview={state.status === 'error' ? state.code : state.status} data-line-numbers="">
      <div className={compactHeader ? `${styles.previewHeader} ${styles.previewHeaderToolbar}` : styles.previewHeader}>
        <RepoMark provider={provider}/>
        <span className={styles.previewTitle}>
          <span className={styles.previewPath} title={path}>{path}</span>
          <span className={styles.previewMeta}>
            <span className={styles.previewRepo}>{repo}</span>
            {ref && <span className={styles.previewRef} title={ref}>{ref}</span>}
            {range && <span className={styles.cardRange}>{range}</span>}
          </span>
        </span>
        {!compactHeader && <span className={styles.previewActions}>
          <a className={styles.previewOpen} href={href} rel="noopener noreferrer" target="_blank">{t(provider === 'gitlab' ? 'Open in GitLab' : 'Open in GitHub')}<ExternalLink size={12}/></a>
        </span>}
      </div>
      {state.status === 'loading' && (
        <div aria-busy="true" className={styles.previewLoading} role="status">
          <span className="sr-only">{t('Loading file preview…')}</span>
          {[72, 54, 64].map(width => <span aria-hidden="true" className={styles.previewSkeleton} key={width} style={{ width: `${width}%` }}/>)}
        </div>
      )}
      {copy && (
        <div className={styles.previewStatus} role={state.status === 'error' && state.code === 'upstream' ? 'alert' : 'status'}>
          <FileCode2 size={16}/>
          <span className={styles.previewStatusText}>
            <span>{name(t(copy.title))}</span>
            {copy.detail && <span className={styles.previewStatusDetail}>{name(t(copy.detail))}</span>}
          </span>
          {state.status === 'error' && state.code === 'not_connected' && <a className={styles.previewButton} href={integrationSettingsHref(provider)}>{name(t('Connect {provider}'))}</a>}
          {state.status === 'error' && (state.code === 'upstream' || state.code === 'rate_limited') && <button className={styles.previewButton} onClick={retry} type="button"><RotateCw size={12}/>{t('Retry')}</button>}
        </div>
      )}
      {preview && (lines.length ? (
        <>
          <div className="flow-code-block__body" style={{ '--flow-code-lines': COLLAPSED_LINES } as CSSProperties}>
            <div aria-hidden className="flow-code-block__gutter">{lines.map((_, index) => <span key={index}>{preview.startLine + index}</span>)}</div>
            <code className="flow-code-block__code">
              {lines.map((segments, index) => (
                <span className={styles.previewLine} key={index}>
                  {segments.map((segment, part) => segment.className ? <span className={segment.className} key={part}>{segment.text}</span> : segment.text)}
                  {index < lines.length - 1 ? '\n' : ''}
                </span>
              ))}
            </code>
          </div>
          {collapsible && (
            <button className="flow-code-block__expander" onClick={() => setShowAll(value => !value)} type="button">
              {expanded ? <ChevronUp size={14}/> : <ChevronDown size={14}/>}
              {expanded ? t('Collapse') : t('Show all {count} lines').replace('{count}', String(lines.length))}
            </button>
          )}
          {preview.truncated && <div className={styles.previewNote}>{name(t('Preview truncated. Open the file on {provider} to see all of it.'))}</div>}
        </>
      ) : <div className={styles.previewStatus} role="status"><FileCode2 size={16}/><span>{t('This file is empty')}</span></div>)}
    </div>
  )
}
