import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '../../styles/tokens.css'
import { MyIssuesVisualFixture } from './my-issues-visual-fixture'
import { I18nProvider } from '@/i18n/i18n'

const theme = new URLSearchParams(location.search).get('theme')
if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme
const locale = new URLSearchParams(location.search).get('locale')
if (locale === 'en-US' || locale === 'zh-CN') localStorage.setItem('flow:locale', locale)

createRoot(document.getElementById('root')!).render(<StrictMode><I18nProvider><MyIssuesVisualFixture/></I18nProvider></StrictMode>)
