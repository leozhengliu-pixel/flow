import type { Logger } from './log.js'
import type { BootstrapResponse } from './protocol.js'

// Warm mode (§11): an unbound pool sandbox long-polls GET /bootstrap?wait=60s
// (204 until the session reconciler binds it). It holds no secrets and no
// repo or LLM access until then.

export interface WarmApi {
  bootstrap(waitSeconds?: number, signal?: AbortSignal): Promise<BootstrapResponse | null>
}

export async function waitForBinding(api: WarmApi, logger: Logger, signal?: AbortSignal, waitSeconds = 60): Promise<BootstrapResponse | null> {
  logger.info('warm sandbox waiting to be bound')
  while (!signal?.aborted) {
    const bootstrap = await api.bootstrap(waitSeconds, signal)
    if (bootstrap) {
      logger.info('warm sandbox bound', { sessionId: bootstrap.sessionId, mode: bootstrap.mode })
      return bootstrap
    }
  }
  return null
}
