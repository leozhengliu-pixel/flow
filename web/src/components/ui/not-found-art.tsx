// Hatching on the computer's base: one hairline every 0.78px across its width.
const HATCH = Array.from({ length: 87 }, (_, index) => `M${(12.9 + index * 0.78).toFixed(2)} 101V106.4`).join('')

/** The retro-computer drawing shown above "Not found" (92x110). */
export function NotFoundArt() {
  const dots: Array<[number, number]> = []
  const rows = [6, 4, 2, 3]
  rows.forEach((count, row) => { for (let col = 0; col < count; col += 1) dots.push([24.1 + col * 7.06, 23 + row * 8]) })
  return (
    <svg aria-hidden="true" className="not-found-art" fill="none" focusable="false" height="110" viewBox="0 0 92 110" width="92">
      <g stroke="var(--set-group-desc)" strokeWidth="1">
        <path d="M10.5.5h71a10 10 0 0 1 10 10v88a1.5 1.5 0 0 1-1.5 1.5H2A1.5 1.5 0 0 1 .5 98.5v-88a10 10 0 0 1 10-10Z"/>
        <rect height="57.6" rx="8" width="70.2" x="10.8" y="9"/>
        <path d="M9.8 100v6.8a2.7 2.7 0 0 0 2.7 2.7h67.8a2.7 2.7 0 0 0 2.7-2.7V100"/>
        <path d={HATCH} strokeWidth=".4"/>
      </g>
      <rect fill="var(--set-group-desc)" height="2.8" rx="1.4" width="19.5" x="54.8" y="77.1"/>
      <rect fill="var(--set-group-desc)" height="2.5" rx="1" width="5.7" x="11.3" y="85.5"/>
      <rect height="49.2" rx="3.5" stroke="var(--set-group-title)" width="60.5" x="15.8" y="13.2"/>
      <g fill="var(--set-group-title)">
        {dots.map(([x, y]) => <rect height="4.2" key={`${x}-${y}`} rx="1" width="3" x={x - 1.5} y={y - 2.1}/>)}
      </g>
      <path d="M56.3 42.4l9.2 9.1M65.5 42.4l-9.2 9.1" stroke="var(--set-group-title)" strokeLinecap="round" strokeWidth="3"/>
    </svg>
  )
}
