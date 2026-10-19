/** The diff glyph of the page title (the reference app's 16px "code changes" icon). */
export function CodingDiffGlyph({ size = 16 }: { size?: number }) {
  return <svg width={size} height={size} viewBox="0 0 16 16" fill="none" aria-hidden="true" className="coding-diff-glyph">
    <rect x="2.5" y="1.5" width="11" height="13" rx="2" stroke="currentColor"/>
    <path d="M8 4.5v4M6 6.5h4M6 11h4" stroke="currentColor" strokeLinecap="round"/>
  </svg>
}
