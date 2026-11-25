import { IntegrationBrandIcon } from '@/components/settings/integration-brand-icon'
import type { LinkBrand } from '@/lib/link-brand'

/** The mark of a Figma file: five rounded shapes in the service's four colours. */
function FigmaMark({ size }: { size: number }) {
  return (
    <svg aria-hidden="true" height={size} viewBox="0 0 16 16" width={size}>
      <path d="M5.33 1h2.67v4.33H5.33a2.17 2.17 0 0 1 0-4.33Z" fill="#F24E1E"/>
      <path d="M8 1h2.67a2.17 2.17 0 0 1 0 4.33H8V1Z" fill="#FF7262"/>
      <path d="M5.33 5.33H8v4.34H5.33a2.17 2.17 0 0 1 0-4.34Z" fill="#A259FF"/>
      <circle cx="10.67" cy="7.5" fill="#1ABCFE" r="2.17"/>
      <path d="M5.33 9.67H8v2.16a2.17 2.17 0 1 1-2.67-2.16Z" fill="#0ACF83"/>
    </svg>
  )
}

export function LinkBrandIcon({ brand, size = 16 }: { brand: LinkBrand; size?: number }) {
  if (brand === 'figma') return <FigmaMark size={size}/>
  return <IntegrationBrandIcon provider={brand} size={size}/>
}
