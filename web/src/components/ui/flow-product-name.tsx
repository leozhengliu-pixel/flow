import './flow-product-name.css'

/** A stable name slot keeps inline feature names aligned across product surfaces. */
export function FlowProductName({ emphasis = false }: { emphasis?: boolean }) {
  return <span className="flow-product-name" data-emphasis={emphasis || undefined}>Flow</span>
}
