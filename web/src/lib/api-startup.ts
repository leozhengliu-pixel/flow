import type { AccountBootstrap, AuthSession } from '@/types/flow'
import { request } from './api-client'
import { rememberChatPlatform } from './chat-platform-cache'

export function fetchAccountBootstrap(): Promise<AccountBootstrap> {
  return request<AccountBootstrap>('/api/account/bootstrap').then(rememberChatPlatform)
}

export function fetchAuthSession(): Promise<AuthSession> {
  return request('/api/auth/session')
}
