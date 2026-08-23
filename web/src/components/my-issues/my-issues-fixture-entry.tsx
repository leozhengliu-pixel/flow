import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../../styles/tokens.css'
import { MyIssuesVisualFixture } from './my-issues-visual-fixture'
import { I18nProvider } from '@/i18n/i18n'

createRoot(document.getElementById('root')!).render(<StrictMode><I18nProvider><MyIssuesVisualFixture/></I18nProvider></StrictMode>)
