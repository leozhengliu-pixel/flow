/**
 * Empty-state art for the in-app Releases views, drawn for Flow at the sizes the reference app uses
 * (issues 86×80, releases 229×110, changelog 160×122 at 110px). Strokes and fills come from
 * `releases.css` tokens so both themes work.
 */

const COINS = [
  { x: 19, y: 18, glyph: 'backlog' },
  { x: 67, y: 18, glyph: 'todo' },
  { x: 19, y: 60, glyph: 'started' },
  { x: 67, y: 60, glyph: 'done' },
] as const

/** Four status coins — the release Issues tab when no issues are scoped. */
export function ReleaseIssuesIllustration() {
  return <svg className="flow-release-art" aria-hidden="true" width="86" height="80" viewBox="0 0 86 80" fill="none">
    {COINS.map(({ x, y, glyph }) => <g key={glyph}>
      <ellipse className="flow-release-art__surface flow-release-art__faint" cx={x} cy={y + 3} rx="17.25" ry="15.25" strokeWidth="1.5"/>
      <ellipse className="flow-release-art__surface flow-release-art__base" cx={x} cy={y} rx="17.25" ry="15.25" strokeWidth="1.5"/>
      {glyph === 'backlog' && <ellipse className="flow-release-art__base" cx={x} cy={y} rx="10" ry="8" strokeWidth="1.5" strokeDasharray="1.6 2.2" strokeLinecap="round"/>}
      {glyph === 'todo' && <ellipse className="flow-release-art__base" cx={x} cy={y} rx="10" ry="8" strokeWidth="1.5"/>}
      {glyph === 'started' && <>
        <ellipse className="flow-release-art__base" cx={x} cy={y} rx="10" ry="8" strokeWidth="1.5"/>
        <path className="flow-release-art__fill-muted" d={`M${x} ${y - 5.5}c3.9 0 6.9 2.5 6.9 5.5s-3 5.5-6.9 5.5Z`}/>
      </>}
      {glyph === 'done' && <>
        <ellipse className="flow-release-art__fill-base" cx={x} cy={y} rx="10.75" ry="8.75"/>
        <path className="flow-release-art__check" d={`M${x - 4.5} ${y}l3 2.6 6-5.4`} strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"/>
      </>}
    </g>)}
  </svg>
}

const TRACK = [
  { x: 28, kind: 'done' },
  { x: 82, kind: 'done' },
  { x: 136, kind: 'active' },
  { x: 190, kind: 'planned' },
] as const

/** Releases moving along a pipeline track — a scheduled pipeline without releases. */
export function ReleasesIllustration() {
  return <svg className="flow-release-art" aria-hidden="true" width="229" height="110" viewBox="0 0 229 116" fill="none">
    <path className="flow-release-art__faint" d="M6 58H223" strokeWidth="1.5" strokeDasharray="3 5" strokeLinecap="round"/>
    <path className="flow-release-art__muted" d="M6 58H136" strokeWidth="1.5" strokeLinecap="round"/>
    {TRACK.map(({ x, kind }) => <g key={x}>
      <rect className={`flow-release-art__surface ${kind === 'planned' ? 'flow-release-art__faint' : 'flow-release-art__base'}`} x={x - 17} y={kind === 'active' ? 32 : 41} width="34" height="34" rx="10" strokeWidth="1.5" strokeDasharray={kind === 'planned' ? '3 3.5' : undefined}/>
      <path className={kind === 'planned' ? 'flow-release-art__fill-faint' : kind === 'active' ? 'flow-release-art__fill-base' : 'flow-release-art__fill-muted'} d={`M${x} ${(kind === 'active' ? 32 : 41) + 10.5}l6.5 10.5h-13Z`} strokeLinejoin="round"/>
    </g>)}
    <path className="flow-release-art__muted" d="M129 84.5l7 6 7-6" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
    <path className="flow-release-art__faint" d="M136 90.5V104" strokeWidth="1.5" strokeLinecap="round" strokeDasharray="2 4"/>
  </svg>
}

/** A tilted, dashed release-notes folder — the Changelog before any notes exist (the reference app's 160×122 art box at 110px). */
export function ChangelogIllustration() {
  return <svg className="flow-release-art is-tall" aria-hidden="true" width="160" height="122" viewBox="0 0 160 122" fill="none">
    <path className="flow-release-art__faint" d="M4.5 31 22.5 113.5" strokeWidth="1.5" strokeDasharray="4 3" strokeLinecap="round"/>
    <path className="flow-release-art__base" d="M10 25h24c2 0 3.6.9 4.8 2.3l5.6 6.6c1 1.2 2.6 1.9 4.2 1.9H86c3.4 0 6.3 2.3 7.1 5.6l18.6 66.4c.9 3.3-1.4 6.6-4.8 6.9l-71.8 5.1c-2.9.2-5.6-1.8-6.2-4.7L5.2 31.6C4.5 28.2 6.6 25 10 25Z" strokeWidth="1.5" strokeDasharray="4 3" strokeLinejoin="round"/>
    <path className="flow-release-art__surface flow-release-art__muted" d="M26.4 48.6c-.3-1.6.8-3.1 2.4-3.4l8.3-1.4c1.6-.3 3.1.8 3.4 2.4l1.4 8.3c.3 1.6-.8 3.1-2.4 3.4l-8.3 1.4c-1.6.3-3.1-.8-3.4-2.4Z" strokeWidth="1.5"/>
    <path className="flow-release-art__fill-base" d="m33.2 48.5 3.6 5.6-6.6 1.1Z"/>
    {[[30, 70, 86, 69], [32, 78, 73, 77], [33, 86, 76, 85], [35, 94, 78, 93], [36, 102, 67, 101]].map(([x1, y1, x2, y2]) => <path key={y1} className="flow-release-art__base" d={`M${x1} ${y1}L${x2} ${y2}`} strokeWidth="1.5" strokeDasharray="3.5 3" strokeLinecap="round"/>)}
  </svg>
}
