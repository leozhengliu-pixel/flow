export function handleEditorSubmit(event: KeyboardEvent, submit?: () => void) {
  if (!(event.metaKey || event.ctrlKey) || event.key !== 'Enter') return false
  event.preventDefault()
  submit?.()
  return true
}
