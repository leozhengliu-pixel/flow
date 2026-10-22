type AppNavigator = (path: string) => void

let navigator: AppNavigator | undefined

/** The mounted router registers itself so code outside React (editor plugins) can navigate in-app without a reload. */
export function registerAppNavigator(next: AppNavigator) {
  navigator = next
  return () => { if (navigator === next) navigator = undefined }
}

/** Navigates through the router; false when no router is mounted. */
export function navigateInApp(path: string) {
  if (!navigator) return false
  navigator(path)
  return true
}
