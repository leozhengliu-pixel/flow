/**
 * Empty-state art for the in-app Releases views, drawn for Flow at the sizes Linear uses
 * (issues 86×80, releases 229×110, changelog 104×90). Strokes and fills come from
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

/** A dashed release-notes page — the Changelog before any notes exist. */
export function ChangelogIllustration() {
  return <svg className="flow-release-art" aria-hidden="true" width="104" height="90" viewBox="0 0 104 90" fill="none">
    <path className="flow-release-art__faint" d="M10 12.5c0-3 2.4-5.5 5.5-5.5H40l7 7h41.5c3 0 5.5 2.5 5.5 5.5V77c0 3-2.5 5.5-5.5 5.5h-73C12.5 82.5 10 80 10 77Z" strokeWidth="1.25" strokeDasharray="3.5 3"/>
    <path className="flow-release-art__base" d="M17 9.5c0-2 1.6-3.5 3.5-3.5H38l6 6h37.5c2 0 3.5 1.6 3.5 3.5V70c0 2-1.6 3.5-3.5 3.5h-61C18.6 73.5 17 72 17 70Z" strokeWidth="1.25" strokeDasharray="3.5 3"/>
    <rect className="flow-release-art__base" x="25" y="20" width="12" height="12" rx="3.5" strokeWidth="1.25"/>
    <path className="flow-release-art__fill-base" d="M31 23.2l2.6 4.3h-5.2Z"/>
    {[38, 45, 52, 59].map((y, index) => <path key={y} className="flow-release-art__muted" d={`M${25 + index * 2} ${y}H${index === 3 ? 52 : 75 - index * 4}`} strokeWidth="1.25" strokeDasharray="2.5 2.5" strokeLinecap="round"/>)}
  </svg>
}
