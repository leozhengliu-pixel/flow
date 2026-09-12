/* oxlint-disable react/only-export-components -- Provider and hooks share the captured activation origin. */
import { AnimatePresence, m as motion, LazyMotion, domAnimation, MotionConfig, useIsPresent, useReducedMotion, type HTMLMotionProps } from 'motion/react'
import { animated, useTransition } from '@react-spring/web'
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type MutableRefObject, type ReactNode } from 'react'
import { motionPresets } from '@/lib/motion-presets'

let activation: { x: number; y: number; at: number } | undefined

export function FlowMotionProvider({ children }: { children: ReactNode }) {
  useEffect(() => {
    const pointer = (event: PointerEvent) => { if (event.button === 0) activation = { x: event.clientX, y: event.clientY, at: performance.now() } }
    const keyboard = () => { activation = undefined }
    document.addEventListener('pointerdown', pointer, true)
    document.addEventListener('keydown', keyboard, true)
    return () => { document.removeEventListener('pointerdown', pointer, true); document.removeEventListener('keydown', keyboard, true) }
  }, [])
  return <MotionConfig reducedMotion="user"><LazyMotion features={domAnimation}>{children}</LazyMotion></MotionConfig>
}

/** Keep custom portals alive for their CSS exit; Radix portals use native Presence. */
export function useExitPresence(open: boolean, duration = 420) {
  const reduced = useReducedMotion()
  const [present, setPresent] = useState(open)
  useEffect(() => {
    if (open) { setPresent(true); return }
    if (reduced) { setPresent(false); return }
    const timer = setTimeout(() => setPresent(false), duration)
    return () => clearTimeout(timer)
  }, [duration, open, reduced])
  return open || (!reduced && present)
}

export function useCreateMotion(open: boolean) {
  const element = useRef<HTMLElement | null>(null)
  const wasOpen = useRef(false)
  const origin = useRef<typeof activation>(undefined)
  if (open && !wasOpen.current) origin.current = activation && performance.now() - activation.at < 1000 ? activation : undefined
  wasOpen.current = open
  const measure = useCallback((node: HTMLElement | null) => {
    element.current = node
    if (!node) return
    const point = origin.current
    // Measure the final layout, not a partially animated transform.
    const animation = node.style.getPropertyValue('animation'), priority = node.style.getPropertyPriority('animation')
    node.style.setProperty('animation', 'none', 'important')
    const rect = node.getBoundingClientRect()
    const transform = getComputedStyle(node).transform
    if (typeof DOMMatrixReadOnly !== 'undefined') {
      const matrix = new DOMMatrixReadOnly(transform === 'none' ? undefined : transform)
      node.style.transformOrigin = `calc(50% + ${matrix.e}px) calc(50% + ${matrix.f}px)`
    }
    const x = point ? point.x - (rect.left + rect.width / 2) : 0
    const y = point ? point.y - (rect.top + rect.height / 2) : 0
    node.style.setProperty('--flow-create-x', `${x}px`)
    node.style.setProperty('--flow-create-y', `${y}px`)
    node.style.setProperty('--flow-create-enter-scale', point ? '.8' : '.95')
    node.style.setProperty('--flow-create-exit-scale', point ? '.2' : '.95')
    node.style.setProperty('--flow-create-enter-duration', point ? '350ms' : '300ms')
    node.style.setProperty('--flow-create-exit-duration', point ? '400ms' : '300ms')
    if (animation) node.style.setProperty('animation', animation, priority); else node.style.removeProperty('animation')
  }, [])
  useLayoutEffect(() => { measure(element.current) }, [measure, open])
  return measure
}

type CollapseProps = { open: boolean; children: ReactNode; className?: string; axis?: 'height' | 'width' } & Pick<HTMLMotionProps<'div'>, 'onDragOver' | 'onDrop' | 'id'>
export function AnimatedCollapse({ open, ...props }: CollapseProps) {
  return <AnimatePresence initial={false}>{open && <CollapseContent key="content" {...props}/>}</AnimatePresence>
}
function CollapseContent({ children, className, axis = 'height', ...props }: Omit<CollapseProps, 'open'>) {
  const reduced = useReducedMotion()
  const present = useIsPresent()
  const closedSize = axis === 'width' ? { width: 0 } : { height: 0 }
  const openedSize = axis === 'width' ? { width: 'auto' as const } : { height: 'auto' as const }
  return <motion.div {...props} className={className} data-flow-motion="collapse" aria-hidden={!present || undefined} inert={!present || undefined}
      initial={reduced ? false : { ...closedSize, opacity: 0, overflow: 'hidden' }}
      animate={{ ...openedSize, opacity: 1, transitionEnd: { overflow: 'visible' } }} exit={{ ...closedSize, opacity: 0, overflow: 'hidden' }}
      transition={reduced ? { duration: 0 } : motionPresets.surface}
      style={{ minWidth: 0 }}>
      {children}
    </motion.div>
}

function MilestoneMotionRow({ progress, leaving, children }: { progress: import('@react-spring/web').SpringValue<number>; leaving: boolean; children: ReactNode }) {
  const [height, setHeight] = useState(0)
  const inner = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const node = inner.current
    if (!node) return
    const measure = () => setHeight(node.getBoundingClientRect().height)
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure); observer.observe(node)
    return () => observer.disconnect()
  }, [])
  return <animated.div data-flow-motion="milestone" data-exiting={leaving || undefined} aria-hidden={leaving || undefined} inert={leaving || undefined}
    style={{ height: progress.to(value => value === 1 ? 'auto' : value * height), opacity: progress, overflow: progress.to(value => value === 1 ? 'visible' : 'hidden') }}>
    <div ref={inner} style={{ display: 'flow-root' }}>{children}</div>
  </animated.div>
}

export function AnimatedList<T>({ items, getKey, seenKeys, children }: {
  items: T[]
  getKey: (item: T) => string
  seenKeys?: MutableRefObject<Set<string>>
  children: (item: T) => ReactNode
}) {
  const reduced = useReducedMotion()
  const transitions = useTransition(items, {
    keys: getKey,
    initial: item => (!seenKeys || seenKeys.current.has(getKey(item))) ? { progress: 1 } : { progress: 0 },
    from: { progress: 0 },
    enter: { progress: 1 },
    leave: { progress: 0 },
    config: motionPresets.milestone,
    immediate: Boolean(reduced),
  })
  useLayoutEffect(() => {
    if (!seenKeys) return
    for (const item of items) seenKeys.current.add(getKey(item))
  }, [getKey, items, seenKeys])
  return transitions((style, item, state) => <MilestoneMotionRow progress={style.progress} leaving={state.phase === 'leave'}>{children(item)}</MilestoneMotionRow>)
}

export function AnimatedMilestones<T extends { id: string }>({ items, children }: { items: T[]; children: (item: T) => ReactNode }) {
  return <AnimatedList items={items} getKey={item => item.id}>{children}</AnimatedList>
}
