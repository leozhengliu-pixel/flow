export const motionPresets = {
  sidebar: { type: 'spring', stiffness: 420, damping: 38, mass: 1 },
  surface: { type: 'spring', duration: 0.3, bounce: 0 },
  quick: { duration: 0.15, ease: 'easeOut' },
  createEnter: { type: 'spring', duration: 0.35, bounce: 0 },
  createExit: { type: 'spring', duration: 0.4, bounce: 0 },
  popoverEnter: { tension: 1500, friction: 100, precision: 0.01 },
  popoverExit: { tension: 2000, friction: 100, precision: 0.01 },
  milestone: { tension: 2000, friction: 100, precision: 0.01 },
} as const
