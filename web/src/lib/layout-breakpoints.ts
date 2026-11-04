/** The compact drawer and docked desktop shell share one inclusive boundary. */
export const COMPACT_LAYOUT_MAX_WIDTH = 880
export const DESKTOP_LAYOUT_QUERY = `(width > ${COMPACT_LAYOUT_MAX_WIDTH}px)`
export const COMPACT_LAYOUT_QUERY = `(width <= ${COMPACT_LAYOUT_MAX_WIDTH}px)`
