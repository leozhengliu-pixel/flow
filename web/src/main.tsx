import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import App from './App.tsx'
import { BrowserRouter } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import { initializeTheme } from './lib/theme'
import { ThemedToaster } from './components/ui/themed-toaster'
import './i18n/i18n.css'

initializeTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <BrowserRouter>
        <App />
        <ThemedToaster />
      </BrowserRouter>
    </I18nProvider>
  </StrictMode>,
)
