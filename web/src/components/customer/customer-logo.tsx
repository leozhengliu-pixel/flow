import type { CSSProperties } from 'react'
import type { Customer } from '@/types/flow'
import './customer-tile.css'

export type CustomerLogoCustomer = Pick<Customer, 'id' | 'name' | 'logoUrl'>

/**
 * Linear's CustomerLogo: the customer's logo (or its first letter on a muted
 * square, or the building glyph when there is no customer) in a `size`px tile.
 * `withBackground` (the default) draws it 2px smaller on a rounded tile backed
 * by a blurred copy of the logo.
 */
export function CustomerLogo({ customer, size = 16, withBackground = true, radius, className, title }: {
  customer?: CustomerLogoCustomer | null
  size?: number
  withBackground?: boolean
  /** Corner radius of the inner logo (Linear's pile uses 2px). */
  radius?: number
  className?: string
  title?: string
}) {
  const inner = withBackground ? size - 2 : size
  const innerStyle: CSSProperties = { width: inner, height: inner, fontSize: Math.round((2 / 3) * inner), lineHeight: `${inner}px`, ...(radius !== undefined ? { borderRadius: radius } : {}) }
  const content = !customer
    ? <span aria-hidden="true" className="customer-tile__default" style={innerStyle}><CustomerDefaultLogoIcon size={Math.round((2 / 3) * size)}/></span>
    : customer.logoUrl
      ? <img alt={customer.name} className="customer-tile__image" height={inner} src={customer.logoUrl} style={innerStyle} width={inner}/>
      : <span aria-hidden="true" className="customer-tile__letter" data-i18n-ignore style={innerStyle}>{customer.name.slice(0, 1).toUpperCase()}</span>
  const classes = `customer-tile${withBackground ? ' has-background' : ''}${className ? ` ${className}` : ''}`
  if (!withBackground) return <span className={classes} title={title} style={{ width: size, height: size }}>{content}</span>
  return <span className={classes} title={title} style={{ width: size, height: size }}>
    {customer?.logoUrl && <span aria-hidden="true" className="customer-tile__backdrop" style={{ filter: `blur(${size / 2}px)` }}><span style={{ backgroundImage: `url(${JSON.stringify(customer.logoUrl)})` }}/></span>}
    <span className="customer-tile__content">{content}</span>
  </span>
}

/** Linear's CustomerDefaultLogoIcon (a building with a person), drawn with currentColor. */
export function CustomerDefaultLogoIcon({ size = 16 }: { size?: number }) {
  return <svg aria-hidden="true" fill="currentColor" focusable="false" height={size} viewBox="0 0 16 16" width={size}>
    <path clipRule="evenodd" d="M11.0247 12.3333C13.6728 12.3334 14.6225 13.529 14.9606 14.319C15.112 14.6739 14.806 15 14.4046 15H7.59537C7.18784 14.9997 6.8816 14.6641 7.04464 14.3073C7.40663 13.5172 8.38955 12.3333 11.0247 12.3333Z" fillRule="evenodd"/>
    <path clipRule="evenodd" d="M11 7C12.1045 7 12.9998 7.89543 12.9998 9C12.9998 10.1046 12.1045 11 11 11C9.89553 11 9.00018 10.1046 9.00018 9C9.00018 7.89543 9.89553 7 11 7Z" fillRule="evenodd"/>
    <path clipRule="evenodd" d="M10 4.25V3.75C10 3.05964 9.44036 2.5 8.75 2.5H3.75C3.05964 2.5 2.5 3.05964 2.5 3.75V13.25C2.5 13.3881 2.61193 13.5 2.75 13.5H4.25C4.66421 13.5 5 13.8358 5 14.25C5 14.6642 4.66421 15 4.25 15H2.75C1.7835 15 1 14.2165 1 13.25V3.75C1 2.23122 2.23122 1 3.75 1H8.75C10.2688 1 11.5 2.23122 11.5 3.75V4.25C11.5 4.66421 11.1642 5 10.75 5C10.3358 5 10 4.66421 10 4.25Z" fillRule="evenodd"/>
    <path clipRule="evenodd" d="M7.75 4.25C8.16421 4.25 8.5 4.58579 8.5 5C8.5 5.41421 8.16421 5.75 7.75 5.75H4.75C4.33579 5.75 4 5.41421 4 5C4 4.58579 4.33579 4.25 4.75 4.25H7.75Z" fillRule="evenodd"/>
    <path clipRule="evenodd" d="M6.5 7.25C6.91421 7.25 7.25 7.58579 7.25 8C7.25 8.41421 6.91421 8.75 6.5 8.75H4.75C4.33579 8.75 4 8.41421 4 8C4 7.58579 4.33579 7.25 4.75 7.25H6.5Z" fillRule="evenodd"/>
    <path clipRule="evenodd" d="M6.5 10.25C6.91421 10.25 7.25 10.5858 7.25 11C7.25 11.4142 6.91421 11.75 6.5 11.75H4.75C4.33579 11.75 4 11.4142 4 11C4 10.5858 4.33579 10.25 4.75 10.25H6.5Z" fillRule="evenodd"/>
  </svg>
}
