import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import './styles/foundations.css'
import App from './App.tsx'
import './styles/pill-overrides.css'
import './styles/micro-audit-overrides.css'
import './components/ui/tooltip.css'
import { BrowserRouter } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import { initializeTheme } from './lib/theme'
import { ThemedToaster } from './components/ui/themed-toaster'
import { ActionDialogHost } from './components/ui/action-dialogs'
import { TooltipProvider } from './components/ui/tooltip'
import './i18n/i18n.css'
import { FlowMotionProvider } from './components/ui/motion'
import './styles/motion.css'

initializeTheme()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <FlowMotionProvider><I18nProvider>
      <BrowserRouter>
        <TooltipProvider delayDuration={450} skipDelayDuration={300}>
          <App />
        </TooltipProvider>
        <ThemedToaster />
        <ActionDialogHost />
      </BrowserRouter>
    </I18nProvider></FlowMotionProvider>
  </StrictMode>,
)
