import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import './styles/foundations.css'
import App from './App.tsx'
import './styles/pill-overrides.css'
import { BrowserRouter } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import { initializeTheme } from './lib/theme'
import { ThemedToaster } from './components/ui/themed-toaster'
import { ActionDialogHost } from './components/ui/action-dialogs'
import './i18n/i18n.css'

initializeTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <BrowserRouter>
        <App />
        <ThemedToaster />
        <ActionDialogHost />
      </BrowserRouter>
    </I18nProvider>
  </StrictMode>,
)
