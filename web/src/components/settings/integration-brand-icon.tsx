import { SlackIcon } from '@/components/issue/issue-icons'
import { isChatProvider } from '@/lib/integration-catalog'
import { useChatPlatform } from '@/lib/chat-platform'

export type IntegrationBrand = 'github' | 'gitlab' | 'slack' | 'jira' | 'google-calendar' | 'notion' | 'feishu' | 'lark'

/** The deployment's chat platform glyph: Slack, Lark or Feishu (FLOW_CHAT_PLATFORM). */
export function ChatPlatformIcon({ size = 16 }: { size?: number }) {
  const { platform } = useChatPlatform()
  return <IntegrationBrandIcon provider={platform} size={size}/>
}

export function IntegrationBrandIcon({ provider, size = 25 }: { provider: IntegrationBrand; size?: number }) {
  if (isChatProvider(provider)) return <img src={`/integrations/${provider}.${provider === 'feishu' ? 'svg' : 'png'}`} alt="" aria-hidden="true" width={size} height={size} style={{ objectFit: 'contain', flexShrink: 0 }}/>
  if (provider === 'slack') return <SlackIcon size={size}/>
  if (provider === 'google-calendar') {
    return (
      <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden="true">
        <path fill="#fff" d="M3.5 1.5h9a2 2 0 0 1 2 2V11l-3.5 3.5H3.5a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2Z"/>
        <path fill="#4285F4" d="M3.5 1.5h9a2 2 0 0 1 2 2v1h-13v-1a2 2 0 0 1 2-2Z"/>
        <path fill="#34A853" d="M1.5 11h9.5v3.5H3.5a2 2 0 0 1-2-2V11Z"/>
        <path fill="#FBBC04" d="M11 4.5h3.5V11H11Z"/>
        <path fill="#EA4335" d="M11 11h3.5L11 14.5Z"/>
        <path fill="#1967D2" d="M1.5 4.5H11V11H1.5Z" opacity=".08"/>
        <text x="6.25" y="9.9" fill="#4285F4" fontFamily="Arial, sans-serif" fontSize="5" fontWeight="700" textAnchor="middle">31</text>
      </svg>
    )
  }
  if (provider === 'notion') {
    return (
      <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden="true">
        <rect x="2" y="1.75" width="12" height="12.5" rx="2" fill="#fff" stroke="#111" strokeWidth="1.2"/>
        <path fill="#111" d="M5.25 11.25v-6.5h1.2l3.2 4.2v-4.2h1.1v6.5h-1.1L6.4 7v4.25Z"/>
      </svg>
    )
  }
  if (provider === 'jira') {
    return (
      <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden="true">
        <path fill="#2684FF" d="M14.04 0H6.68C8.2 0 9.44 1.24 9.44 2.76v1.36L2.84 10.72a2.12 2.12 0 0 0 0 3l1.52 1.52 9.68-9.68V0Z"/>
        <path fill="#2684FF" d="M14.04 0v6.56c0-1.52-1.24-2.76-2.76-2.76h-1.36l6.6 6.6a2.12 2.12 0 0 0 3 0L21 9.0 14.04 0Z" opacity=".4"/>
        <path fill="#2684FF" d="M8.08 7.92 5.4 10.6a1.9 1.9 0 0 0 0 2.68L8.08 16a1.9 1.9 0 0 0 2.68 0l2.68-2.68a1.9 1.9 0 0 0 0-2.68L10.76 7.92a1.9 1.9 0 0 0-2.68 0Z"/>
      </svg>
    )
  }
  return <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" fill="currentColor">
    {provider === 'github' ? <path d="M8 1.4a6.75 6.75 0 0 0-2.13 13.15c.34.06.46-.14.46-.32v-1.3c-1.88.41-2.28-.8-2.28-.8-.31-.78-.75-.98-.75-.98-.61-.42.05-.41.05-.41.68.05 1.03.69 1.03.69.6 1.03 1.57.73 1.95.56.06-.43.24-.73.43-.9-1.5-.17-3.08-.75-3.08-3.34 0-.74.26-1.34.69-1.81-.07-.17-.3-.86.07-1.79 0 0 .57-.18 1.86.69A6.4 6.4 0 0 1 8 4.61c.58 0 1.15.08 1.69.23 1.29-.87 1.85-.69 1.85-.69.37.93.14 1.62.07 1.79.43.47.69 1.07.69 1.81 0 2.6-1.58 3.16-3.09 3.33.24.21.46.63.46 1.27v1.88c0 .18.12.39.46.32A6.75 6.75 0 0 0 8 1.4Z"/> : <>
      <path fill="#E24329" d="m14.767 6.54-.02-.051-1.905-5.163a.496.496 0 0 0-.948.054l-1.287 4.086h-5.21L4.11 1.38a.5.5 0 0 0-.752-.3.516.516 0 0 0-.196.246l-1.91 5.16-.018.053a3.806 3.806 0 0 0-.097 2.326 3.68 3.68 0 0 0 1.27 1.92l.007.005.017.013 2.903 2.257 1.436 1.128.875.686a.575.575 0 0 0 .712 0l.874-.686 1.436-1.128 2.92-2.27.008-.007a3.681 3.681 0 0 0 1.267-1.918 3.807 3.807 0 0 0-.095-2.323Z"/>
      <path fill="#FC6D26" d="m14.767 6.54-.02-.051a6.326 6.326 0 0 0-2.562 1.195L8 10.97c1.425 1.12 2.666 2.092 2.666 2.092l2.92-2.27.008-.007a3.68 3.68 0 0 0 1.269-1.919 3.808 3.808 0 0 0-.096-2.325Z"/>
      <path fill="#FCA326" d="M5.334 13.062 6.77 14.19l.875.686a.575.575 0 0 0 .712 0l.874-.686 1.436-1.128S9.425 12.086 8 10.97c-1.425 1.116-2.666 2.092-2.666 2.092Z"/>
      <path fill="#FC6D26" d="M3.814 7.684c-.758-.59-1.633-1-2.561-1.198l-.02.052a3.806 3.806 0 0 0-.096 2.326 3.68 3.68 0 0 0 1.27 1.92l.007.005.017.013 2.903 2.257L8 10.967 3.814 7.684Z"/>
    </>}
  </svg>
}
