import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles/tokens.css'
import App from './App.tsx'
import { Toaster } from 'sonner'
import { BrowserRouter } from 'react-router-dom'
import { I18nProvider } from './i18n/i18n'
import './i18n/i18n.css'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <BrowserRouter>
        <App />
        <Toaster theme="dark" position="bottom-right" closeButton richColors />
      </BrowserRouter>
    </I18nProvider>
  </StrictMode>,
)
