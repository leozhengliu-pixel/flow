/** The reference app's floating chat: 400×365 by default, resizable from its top and left edges (360–600 wide, 360px to 80% of the window tall), remembered. */
export const FLOAT_DEFAULT_SIZE = { width: 400, height: 365 }
const FLOAT_MIN = 360
const FLOAT_MAX_WIDTH = 600

export function clampFloatSize(size: { width: number; height: number }, viewportHeight = window.innerHeight) {
  const maxHeight = Math.max(FLOAT_MIN, Math.floor(viewportHeight * 0.8))
  return {
    width: Math.round(Math.min(FLOAT_MAX_WIDTH, Math.max(FLOAT_MIN, size.width))),
    height: Math.round(Math.min(maxHeight, Math.max(FLOAT_MIN, size.height))),
  }
}
