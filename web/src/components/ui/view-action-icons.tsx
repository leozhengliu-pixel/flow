import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement>
const base = { 'aria-hidden': true, focusable: false, height: 16, viewBox: '0 0 16 16', width: 16 } as const

export function FilterIcon(props: IconProps) { return <svg {...base} {...props}><path fillRule="evenodd" d="M14.25 3a.75.75 0 0 1 0 1.5H1.75a.75.75 0 0 1 0-1.5h12.5ZM4 8a.75.75 0 0 1 .75-.75h6.5a.75.75 0 0 1 0 1.5h-6.5A.75.75 0 0 1 4 8Zm2.75 3.5a.75.75 0 0 0 0 1.5h2.5a.75.75 0 0 0 0-1.5h-2.5Z" clipRule="evenodd"/></svg> }
export function DisplayIcon(props: IconProps) { return <svg {...base} {...props}><path fillRule="evenodd" d="M7 2.5c1.119 0 2.066.736 2.385 1.75h5.365a.75.75 0 0 1 0 1.5H9.385A2.501 2.501 0 0 1 4.615 5.75H2.25a.75.75 0 0 1 0-1.5h2.365A2.501 2.501 0 0 1 7 2.5ZM7 4a1 1 0 1 0 0 2 1 1 0 0 0 0-2Zm3 9.5a2.501 2.501 0 0 1-2.385-1.75H2.25a.75.75 0 0 1 0-1.5h5.365a2.501 2.501 0 0 1 4.77 0h2.365a.75.75 0 0 1 0 1.5h-2.365A2.501 2.501 0 0 1 10 13.5Zm0-1.5a1 1 0 1 0 0-2 1 1 0 0 0 0 2Z" clipRule="evenodd"/></svg> }
export function SidebarIcon(props: IconProps) { return <svg {...base} {...props}><path fillRule="evenodd" d="M4.25 2A3.25 3.25 0 0 0 1 5.25v5.5A3.25 3.25 0 0 0 4.25 14h7.5A3.25 3.25 0 0 0 15 10.75v-5.5A3.25 3.25 0 0 0 11.75 2h-7.5ZM2.5 5.5a2 2 0 0 1 2-2h7a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-7a2 2 0 0 1-2-2v-5Z" clipRule="evenodd"/><rect x="7" y="5" width="4.5" height="6" rx=".75"/></svg> }
export function CheckIcon(props: IconProps) { return <svg {...base} {...props} fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5"><path d="m3.5 8 3 3 6-6"/></svg> }
export function ChevronRightIcon(props: IconProps) { return <svg {...base} {...props} fill="none" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.35"><path d="m6.5 4.5 3.5 3.5-3.5 3.5"/></svg> }
