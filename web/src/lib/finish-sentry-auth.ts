/**
 * LS-0699 finishSentryAuth — Sentry App external-install is not wired yet.
 * Flow exposes Sentry via MCP connectors only; this helper stays honest.
 */

export type FinishSentryAuthParams = {
  installationId?: string | null
  code?: string | null
  orgSlug?: string | null
}

export type FinishSentryAuthResult =
  | { ok: true; message: string }
  | { ok: false; error: string; mcpOnly: true }

export function parseSentryCallbackSearch(search: string): FinishSentryAuthParams {
  const params = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  return {
    installationId: params.get('installationId') ?? params.get('installation_id'),
    code: params.get('code'),
    orgSlug: params.get('orgSlug') ?? params.get('org_slug'),
  }
}

export async function finishSentryAuth(
  search: string,
): Promise<FinishSentryAuthResult> {
  const parsed = parseSentryCallbackSearch(search)
  if (!parsed.installationId || !parsed.code || !parsed.orgSlug) {
    return {
      ok: false,
      mcpOnly: true,
      error:
        'Sentry App OAuth is not available in Flow. Use Settings → Agents → MCP connectors with https://mcp.sentry.dev/mcp, or ask a workspace admin.',
    }
  }
  // No REST App-install hook yet — do not pretend the mutate succeeded.
  return {
    ok: false,
    mcpOnly: true,
    error:
      'Sentry App OAuth is not configured. Flow currently supports Sentry through MCP only (https://mcp.sentry.dev/mcp).',
  }
}
