/**
 * Empty-state art for Settings › Releases: release markers on the left travel
 * along tracks through a series of stage gates and leave as shipped arrows.
 * Drawn for Flow; strokes follow the text color so it works in both themes.
 */
export function PipelineEmptyIllustration({ className }: { className?: string }) {
  const tracks = [14, 35, 56, 77, 98]
  const ends = [226, 212, 222, 228, 214]
  const gates = [66, 94, 122, 150, 178]
  return <svg className={className} aria-hidden="true" data-illustration="release-pipelines" width="232" height="112" viewBox="0 0 232 112" fill="none">
    {gates.map((x, index) => <ellipse key={x} cx={x} cy="56" rx="15" ry="52" stroke="currentColor" strokeWidth="1.25" opacity={0.35 + index * 0.13}/>)}
    {tracks.map((y, index) => {
      const active = index === 2
      return <g key={y} opacity={active ? 1 : 0.55}>
        <rect x="1.5" y={y - 5.5} width="11" height="11" rx="2.5" stroke="currentColor" strokeWidth="1" fill={active ? 'currentColor' : 'none'} fillOpacity={active ? 0.16 : 0}/>
        <path d={`M7 ${y - 2.5}l2.6 4.2H4.4z`} fill="currentColor" opacity={active ? 1 : 0.8}/>
        <path d={`M17 ${y}H${ends[index] - 1}`} stroke="currentColor" strokeWidth={active ? 1.4 : 1} strokeDasharray={active ? undefined : '10 4'} strokeLinecap="round"/>
        <path d={`M${ends[index] - 5} ${y - 3.5}L${ends[index]} ${y}l-5 3.5`} stroke="currentColor" strokeWidth={active ? 1.4 : 1} strokeLinecap="round" strokeLinejoin="round"/>
      </g>
    })}
  </svg>
}
