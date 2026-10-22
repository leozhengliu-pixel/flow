import { useMemo, useState } from 'react'
import { pairedWordRanges } from '@/lib/word-diff'
import { ChevronRight, FileCode2, Folder, GitBranch } from 'lucide-react'
import { ReviewCode } from '@/components/reviews/review-code'
import { useI18n } from '@/i18n/i18n'
import type { CodingDiffFile, CodingSession, CodingSessionDiff } from '@/types/coding-sessions'
import { codingDiffTree, useCodingSessionDiff, type CodingDiffTreeNode } from './coding-session-page-model'

/** Files with more lines than this start collapsed, and are not syntax highlighted. */
const LARGE_FILE_LINES = 600

const STATUS_LABELS: Record<CodingDiffFile['status'], string> = { added: 'Added', modified: 'Modified', deleted: 'Deleted', renamed: 'Renamed' }

const fileLines = (file: CodingDiffFile) => file.hunks.reduce((sum, hunk) => sum + hunk.lines.length, 0)
const fileAnchor = (index: number) => `coding-diff-file-${index}`

/**
 * The session's code changes, like the reference app's session page: a file tree with per-file +/- counts beside the
 * unified diff of each file (line numbers, tinted added and removed rows, syntax highlighting, collapse per file).
 * Before the agent changes anything it shows "No changes yet".
 */
export function CodingDiffViewer({ session }: { session: CodingSession }) {
  const { t } = useI18n()
  const { diff, error, loading } = useCodingSessionDiff(session)
  const files = useMemo(() => diff?.files ?? [], [diff])
  const [closedDirs, setClosedDirs] = useState<Set<string>>(() => new Set())
  const [overrides, setOverrides] = useState<Map<string, boolean>>(() => new Map())
  const tree = useMemo(() => codingDiffTree(files), [files])
  // Large files start collapsed until the viewer opens them.
  const isCollapsed = (file: CodingDiffFile) => overrides.get(file.path) ?? fileLines(file) > LARGE_FILE_LINES
  const setFileCollapsed = (path: string, value: boolean) => setOverrides(previous => new Map(previous).set(path, value))
  const allCollapsed = files.length > 0 && files.every(isCollapsed)
  const toggleAll = () => {
    const next = !allCollapsed
    setOverrides(new Map(files.map(file => [file.path, next])))
  }
  const reveal = (index: number) => {
    const file = files[index]
    if (!file) return
    setFileCollapsed(file.path, false)
    requestAnimationFrame(() => document.getElementById(fileAnchor(index))?.scrollIntoView?.({ block: 'start', behavior: 'smooth' }))
  }

  if (!files.length) {
    if (loading && !diff) return <div className="coding-diff is-loading" aria-busy="true" aria-label={t('Loading changes…')}/>
    return <div className="coding-session-empty">
      <CodingChangesIllustration/>
      <strong>{t('No changes yet')}</strong>
      <span>{t('Changes will appear here as the agent works.')}</span>
      {error && <p className="coding-session-error-text" role="alert">{error}</p>}
    </div>
  }
  const renderNode = (node: CodingDiffTreeNode, depth: number) => [...node.children.values()]
    .sort((a, b) => Number(Boolean(a.file)) - Number(Boolean(b.file)) || a.name.localeCompare(b.name))
    .map(child => {
      if (child.file) {
        const { index, data } = child.file
        return <li key={child.path} role="none">
          <button type="button" role="treeitem" className="coding-diff-tree-row" data-status={data.status} style={{ paddingLeft: 8 + depth * 12 }} onClick={() => reveal(index)} title={data.path}>
            <FileCode2 size={14} aria-hidden="true"/>
            <span className="coding-diff-tree-name" data-i18n-ignore>{child.name}</span>
            <span className="coding-diff-counts"><span className="coding-additions">+{data.additions}</span><span className="coding-deletions">−{data.deletions}</span></span>
          </button>
        </li>
      }
      const open = !closedDirs.has(child.path)
      return <li key={child.path} role="none">
        <button type="button" role="treeitem" aria-expanded={open} className="coding-diff-tree-row is-dir" style={{ paddingLeft: 8 + depth * 12 }} onClick={() => setClosedDirs(previous => { const next = new Set(previous); if (open) next.add(child.path); else next.delete(child.path); return next })}>
          <ChevronRight size={12} aria-hidden="true" data-open={open || undefined}/>
          <Folder size={14} aria-hidden="true"/>
          <span className="coding-diff-tree-name" data-i18n-ignore>{child.name}</span>
        </button>
        {open && <ul role="group">{renderNode(child, depth + 1)}</ul>}
      </li>
    })

  return <section className="coding-diff" aria-label={t('Code changes')}>
    <header className="coding-diff-toolbar">
      <strong>{t(files.length === 1 ? '1 file changed' : '{count} files changed').replace('{count}', String(files.length))}</strong>
      <span className="coding-diff-counts"><span className="coding-additions">+{diff?.additions ?? 0}</span><span className="coding-deletions">−{diff?.deletions ?? 0}</span></span>
      {diff?.head && <span className="coding-diff-refs" data-i18n-ignore><GitBranch size={12} aria-hidden="true"/>{diff.head}{diff.base ? ` → ${diff.base}` : ''}</span>}
      <button type="button" className="coding-session-button coding-diff-toggle-all" onClick={toggleAll}>{t(allCollapsed ? 'Expand all' : 'Collapse all')}</button>
    </header>
    {diff?.source === 'summary' && <p className="coding-diff-note" role="note">{t(diff.errorCode ? 'The code host could not be reached; showing the files the agent changed.' : 'Line changes appear here once the agent pushes its branch.')}</p>}
    {diff?.truncated && <p className="coding-diff-note" role="note">{t('Only part of this diff is shown. Open the pull request for the full diff.')}</p>}
    <div className="coding-diff-body">
      <nav className="coding-diff-tree" aria-label={t('Changed files')}><ul role="tree">{renderNode(tree, 0)}</ul></nav>
      <div className="coding-diff-files">
        {files.map((file, index) => <CodingDiffFileView key={file.path} id={fileAnchor(index)} file={file} source={diff?.source ?? 'none'} collapsed={isCollapsed(file)} onToggle={() => setFileCollapsed(file.path, !isCollapsed(file))}/>)}
      </div>
    </div>
  </section>
}

function CodingDiffFileView({ id, file, source, collapsed, onToggle }: { id: string; file: CodingDiffFile; source: CodingSessionDiff['source']; collapsed: boolean; onToggle: () => void }) {
  const { t } = useI18n()
  const lines = fileLines(file)
  const highlight = lines <= LARGE_FILE_LINES
  // Changed words inside paired removed/added lines, per hunk (hunk index -> line index -> ranges).
  const wordMarks = useMemo(() => file.hunks.map(hunk => pairedWordRanges(hunk.lines.map(line => ({ kind: line.kind === 'add' ? 'add' as const : line.kind === 'delete' ? 'remove' as const : 'context' as const })), index => hunk.lines[index].text)), [file.hunks])
  const message = file.binary ? 'Binary file not shown.'
    : file.tooLarge ? 'This diff is too large to show here.'
      : !file.hunks.length && source === 'summary' ? 'Line changes appear once the branch is pushed.'
        : !file.hunks.length ? (file.status === 'renamed' ? 'File renamed without changes.' : 'No line changes.') : ''
  return <article id={id} className="coding-diff-file" data-status={file.status} aria-label={file.path}>
    <header>
      <button type="button" className="coding-diff-file-toggle" aria-expanded={!collapsed} onClick={onToggle}>
        <ChevronRight size={14} aria-hidden="true" data-open={!collapsed || undefined}/>
        <span className="coding-diff-file-path" data-i18n-ignore>{file.previousPath && file.previousPath !== file.path ? `${file.previousPath} → ${file.path}` : file.path}</span>
      </button>
      <span className="coding-diff-file-status" data-status={file.status}>{t(STATUS_LABELS[file.status] ?? 'Modified')}</span>
      <span className="coding-diff-counts"><span className="coding-additions">+{file.additions}</span><span className="coding-deletions">−{file.deletions}</span></span>
    </header>
    {!collapsed && (message
      ? <p className="coding-diff-file-message">{t(message)}</p>
      : <div className="coding-diff-table" role="table" aria-label={t('Diff of {path}').replace('{path}', file.path)}>
        {file.hunks.map((hunk, hunkIndex) => <div key={`${hunk.header}-${hunkIndex}`} role="rowgroup">
          <div className="coding-diff-hunk" role="row"><span role="cell" data-i18n-ignore>{hunk.header}</span></div>
          {hunk.lines.map((line, lineIndex) => <div key={lineIndex} className="coding-diff-line" data-kind={line.kind} role="row">
            <span className="coding-diff-number" role="cell" aria-label={line.oldLine ? t('Old line {n}').replace('{n}', String(line.oldLine)) : undefined}>{line.kind === 'note' ? '' : line.oldLine ?? ''}</span>
            <span className="coding-diff-number" role="cell" aria-label={line.newLine ? t('New line {n}').replace('{n}', String(line.newLine)) : undefined}>{line.kind === 'note' ? '' : line.newLine ?? ''}</span>
            <span className="coding-diff-marker" role="cell" aria-hidden="true">{line.kind === 'add' ? '+' : line.kind === 'delete' ? '−' : ''}</span>
            <span className="coding-diff-code" role="cell">{line.kind === 'note' || !highlight ? <code data-i18n-ignore>{line.text}</code> : <ReviewCode content={line.text} path={file.path} marks={wordMarks[hunkIndex]?.get(lineIndex)}/>}</span>
          </div>)}
        </div>)}
      </div>)}
  </article>
}

/** The empty diff illustration (a document with dashed lines), drawn in the current text colour. */
export function CodingChangesIllustration() {
  return <svg className="coding-session-empty-art" width="97" height="97" viewBox="0 0 97 97" fill="none" aria-hidden="true">
    <rect x="1.5" y="1.5" width="94" height="94" rx="6" stroke="currentColor" strokeWidth="1.5"/>
    <path d="M9 13h22M9 18h45" stroke="currentColor" strokeWidth="1.2"/>
    <path d="M12 30v62" stroke="currentColor" strokeWidth="2"/>
    {[30, 37, 44, 51, 58, 65, 72, 79, 86].map((y, index) => <path key={y} d={`M17 ${y}h${[60, 40, 52, 34, 46, 28, 64, 38, 54][index]}`} stroke="currentColor" strokeWidth="1.2" strokeDasharray="3 2"/>)}
  </svg>
}
