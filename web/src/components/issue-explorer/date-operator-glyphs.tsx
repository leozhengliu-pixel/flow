import type { SVGProps } from 'react'

/** Flow's own 16px calendar marks for the custom-day operators (on / before / after), drawn for Flow. */
function CalendarFrame(props: SVGProps<SVGSVGElement>) {
  return <svg aria-hidden="true" focusable="false" width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" {...props}/>
}

export function DateOnGlyph(props: SVGProps<SVGSVGElement>) {
  return <CalendarFrame {...props}><rect x="1.75" y="2.25" width="12.5" height="11.5" rx="3"/><path d="M1.75 6h12.5"/></CalendarFrame>
}

export function DateBeforeGlyph(props: SVGProps<SVGSVGElement>) {
  return <CalendarFrame {...props}><path d="M14.25 7.5V5.25a3 3 0 0 0-3-3h-6.5a3 3 0 0 0-3 3v5.5a3 3 0 0 0 3 3H7"/><path d="M1.75 6h12.5"/><path d="M14.25 12h-4.5m1.75-1.75L9.75 12l1.75 1.75"/></CalendarFrame>
}

export function DateAfterGlyph(props: SVGProps<SVGSVGElement>) {
  return <CalendarFrame {...props}><path d="M14.25 7.5V5.25a3 3 0 0 0-3-3h-6.5a3 3 0 0 0-3 3v5.5a3 3 0 0 0 3 3H7"/><path d="M1.75 6h12.5"/><path d="M9.75 12h4.5m-1.75-1.75L14.25 12l-1.75 1.75"/></CalendarFrame>
}

/** Dates ▸ Completed date: a calendar with a check. */
export function DateCompletedGlyph(props: SVGProps<SVGSVGElement>) {
  return <CalendarFrame {...props}><path d="M14.25 7.5V5.25a3 3 0 0 0-3-3h-6.5a3 3 0 0 0-3 3v5.5a3 3 0 0 0 3 3H7"/><path d="M1.75 6h12.5"/><path d="m9.5 12 1.75 1.75 3-3.25"/></CalendarFrame>
}

