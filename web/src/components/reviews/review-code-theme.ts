export const MATCH_INTERFACE_CODE_THEME='Match interface theme'

/** Unset or "Match interface theme" follows the current interface theme. */
export function resolveCodeTheme(value:string|undefined) {
  if(!value||value===MATCH_INTERFACE_CODE_THEME) return document.documentElement.dataset.theme==='light'?'Flow Light':'Flow Dark'
  return value
}
