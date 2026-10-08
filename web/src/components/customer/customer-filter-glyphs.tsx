import type { SVGProps } from 'react'

type GlyphProps = SVGProps<SVGSVGElement> & { size?: number }

function Glyph({ size = 16, children, ...props }: GlyphProps) {
  return <svg aria-hidden="true" focusable="false" width={size} height={size} viewBox="0 0 16 16" fill="currentColor" {...props}>{children}</svg>
}

/** Linear's customer glyph (a request sheet with a person): the Customers filter, Customer name and Customer count. */
export function CustomerGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path fillRule="evenodd" clipRule="evenodd" d="M11.0247 12.3333C13.6728 12.3334 14.6225 13.529 14.9606 14.319C15.112 14.6739 14.806 15 14.4046 15H7.59537C7.18784 14.9997 6.8816 14.6641 7.04464 14.3073C7.40663 13.5172 8.38955 12.3333 11.0247 12.3333Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M11 7C12.1045 7 12.9998 7.89543 12.9998 9C12.9998 10.1046 12.1045 11 11 11C9.89553 11 9.00018 10.1046 9.00018 9C9.00018 7.89543 9.89553 7 11 7Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M10 4.25V3.75C10 3.05964 9.44036 2.5 8.75 2.5H3.75C3.05964 2.5 2.5 3.05964 2.5 3.75V13.25C2.5 13.3881 2.61193 13.5 2.75 13.5H4.25C4.66421 13.5 5 13.8358 5 14.25C5 14.6642 4.66421 15 4.25 15H2.75C1.7835 15 1 14.2165 1 13.25V3.75C1 2.23122 2.23122 1 3.75 1H8.75C10.2688 1 11.5 2.23122 11.5 3.75V4.25C11.5 4.66421 11.1642 5 10.75 5C10.3358 5 10 4.66421 10 4.25Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M7.75 4.25C8.16421 4.25 8.5 4.58579 8.5 5C8.5 5.41421 8.16421 5.75 7.75 5.75H4.75C4.33579 5.75 4 5.41421 4 5C4 4.58579 4.33579 4.25 4.75 4.25H7.75Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M6.5 7.25C6.91421 7.25 7.25 7.58579 7.25 8C7.25 8.41421 6.91421 8.75 6.5 8.75H4.75C4.33579 8.75 4 8.41421 4 8C4 7.58579 4.33579 7.25 4.75 7.25H6.5Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M6.5 10.25C6.91421 10.25 7.25 10.5858 7.25 11C7.25 11.4142 6.91421 11.75 6.5 11.75H4.75C4.33579 11.75 4 11.4142 4 11C4 10.5858 4.33579 10.25 4.75 10.25H6.5Z"/>
  </Glyph>
}

/** Linear's "important" triangle, drawn in its orange unless a colour is passed. */
export function ImportantCustomerGlyph({ style, ...props }: GlyphProps) {
  return <Glyph style={{ color: 'var(--customer-important)', ...style }} {...props}>
    <path d="M2.90967 11.4855C2.50976 12.152 2.98987 13 3.76716 13L12.2348 13C13.0121 13 13.4922 12.152 13.0923 11.4855L8.85847 4.42916C8.47006 3.78182 7.53188 3.78182 7.14348 4.42915L2.90967 11.4855Z"/>
  </Glyph>
}

/** Linear's customer owner glyph (a person). */
export function CustomerOwnerGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path fillRule="evenodd" clipRule="evenodd" d="M8.57502 8C10.6434 8.00003 12.4741 9.33858 13.1014 11.3096L13.9647 14.0225C14.0902 14.4171 13.872 14.8392 13.4774 14.9648C13.0827 15.0903 12.6606 14.8721 12.535 14.4775L11.6717 11.7646C11.2426 10.416 9.99026 9.50003 8.57502 9.5H7.42462C6.00944 9.50011 4.75705 10.4161 4.32795 11.7646L3.46466 14.4775C3.33902 14.8722 2.91695 15.0904 2.52228 14.9648C2.12775 14.8392 1.90945 14.4171 2.03498 14.0225L2.89826 11.3096C3.52547 9.33861 5.35628 8.00011 7.42462 8H8.57502Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M7.99982 1C9.5186 1 10.7498 2.23122 10.7498 3.75C10.7498 5.26878 9.5186 6.5 7.99982 6.5C6.48119 6.49982 5.24982 5.26867 5.24982 3.75C5.24982 2.23133 6.48119 1.00018 7.99982 1Z"/>
  </Glyph>
}

/** Linear's DollarBill glyph (Customer revenue). */
export function CustomerRevenueGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M10 8a2 2 0 1 1-4 0 2 2 0 0 1 4 0"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M1 4.25C1 3.56 1.56 3 2.25 3h11.5c.69 0 1.25.56 1.25 1.25v7.5c0 .69-.56 1.25-1.25 1.25H2.25C1.56 13 1 12.44 1 11.75zm1.5 4.78V6.97A1.834 1.834 0 0 0 4 5.167V4.5h8v.667c0 .898.647 1.646 1.5 1.803v2.06c-.853.157-1.5.905-1.5 1.803v.667H4v-.667c0-.898-.647-1.646-1.5-1.803"/>
  </Glyph>
}

/** Linear's customer size glyph (an ID badge). */
export function CustomerSizeGlyph(props: GlyphProps) {
  return <Glyph {...props}>
    <path d="M7.5 1C7.22386 1 7 1.22386 7 1.5V4C7 4.27614 7.22386 4.5 7.5 4.5H8.5C8.77614 4.5 9 4.27614 9 4V1.5C9 1.22386 8.77614 1 8.5 1H7.5Z"/>
    <path fillRule="evenodd" clipRule="evenodd" d="M4.5 3H5.5C5.77614 3 6 3.22386 6 3.5V4H5.75C5.33579 4 5 4.33579 5 4.75C5 5.16421 5.33579 5.5 5.75 5.5H10.25C10.6642 5.5 11 5.16421 11 4.75C11 4.33579 10.6642 4 10.25 4H10V3.5C10 3.22386 10.2239 3 10.5 3H11.5C12.3284 3 13 3.67157 13 4.5V13.5C13 14.3284 12.3284 15 11.5 15H4.5C3.67157 15 3 14.3284 3 13.5V4.5C3 3.67157 3.67157 3 4.5 3ZM9.5 8.5C9.5 9.32843 8.82843 10 8 10C7.17157 10 6.5 9.32843 6.5 8.5C6.5 7.67157 7.17157 7 8 7C8.82843 7 9.5 7.67157 9.5 8.5ZM7 11C5.89543 11 5 11.8954 5 13C5 13.2761 5.22386 13.5 5.5 13.5H10.5C10.7761 13.5 11 13.2761 11 13C11 11.8954 10.1046 11 9 11H7Z"/>
  </Glyph>
}
