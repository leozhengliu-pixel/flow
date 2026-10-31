import { useId } from 'react'
import { useI18n } from '@/i18n/i18n'
import { usePhoneLayout } from '@/hooks/use-phone-layout'
import { SettingsDocsLink } from './settings-docs-link'

export function ApiDocsLink({ file }: { file: string }) {
  const phone = usePhoneLayout()
  const { t } = useI18n()
  if (!phone) return <SettingsDocsLink file={file} />
  return <a className="api-mobile-docs" href={`https://github.com/leozhengliu-pixel/flow/blob/main/docs/${file}`} target="_blank" rel="noreferrer">{t('Docs')} {'\u2197'}</a>
}

export function ApiApplicationPlaceholder() {
  const id = useId()
  return <svg className="api-application-placeholder" viewBox="0 0 256 256" aria-hidden="true">
    <defs><linearGradient id={id} x1="0" y1="0" x2="256" y2="256" gradientUnits="userSpaceOnUse"><stop className="api-placeholder-start"/><stop offset="1" className="api-placeholder-end"/></linearGradient></defs>
    <rect width="256" height="256" fill={`url(#${id})`}/>
    <g className="api-placeholder-lines">
      <path d="M43.5 40.5H85V0H86.5V40.5H128V0H129.5V40.5H171V0H172.5V40.5H214V0H215.5V40.5H256V42H215.5V83.5H256V85H215.5V126.5H256V128H215.5V169.5H256V171H215.5V212.5H256V214H215.5V256H214V214H172.5V256H171V214H129.5V256H128V214H86.5V256H85V214H43.5V256H42V214H0V212.5H42V171H0V169.5H42V128H0V126.5H42V85H0V83.5H42V42H0V40.5H42V0H43.5V40.5ZM86.5 212.5H128V171H86.5V212.5ZM129.5 212.5H171V171H129.5V212.5ZM172.5 212.5H214V171H172.5V212.5ZM43.5 212.5H85V171H43.5V212.5ZM86.5 169.5H128V128H86.5V169.5ZM129.5 169.5H171V128H129.5V169.5ZM172.5 169.5H214V128H172.5V169.5ZM43.5 169.5H85V128H43.5V169.5ZM86.5 126.5H128V85H86.5V126.5ZM129.5 126.5H171V85H129.5V126.5ZM172.5 126.5H214V85H172.5V126.5ZM43.5 126.5H85V85H43.5V126.5ZM86.5 83.5H128V42H86.5V83.5ZM129.5 83.5H171V42H129.5V83.5ZM172.5 83.5H214V42H172.5V83.5ZM43.5 83.5H85V42H43.5V83.5Z"/>
      <circle cx="127.5" cy="128.5" r="31.75"/><circle cx="128" cy="128" r="71.25"/><circle cx="128" cy="128" r="110.25"/>
    </g>
  </svg>
}
