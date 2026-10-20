import { useCallback, useEffect, useRef, useState } from "react";

import { useI18n } from "@/i18n/i18n";
import { fetchCodeHostAccounts } from "@/lib/code-host-accounts-api";
import { toast } from "@/lib/toast";
import type { CodeHostAccountsResponse } from "@/types/code-host-accounts";

/** Loads the viewer's linked code-host accounts and what can be linked. */
export function useCodeHostAccounts() {
  const { t } = useI18n();
  const [data, setData] = useState<CodeHostAccountsResponse | null>(null);
  const [loaded, setLoaded] = useState(false);
  const reload = useCallback(async () => {
    try {
      setData(await fetchCodeHostAccounts());
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not load connected accounts"));
    } finally {
      setLoaded(true);
    }
  }, [t]);
  useEffect(() => {
    void reload();
    // Load once per mount; `t` only changes with the locale.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return { data, loaded, reload };
}

/** Shows a toast for the `codeHostLink` result the server appends after OAuth, then strips it from the URL. */
export function useCodeHostLinkResult() {
  const { t } = useI18n();
  const handled = useRef(false);
  useEffect(() => {
    if (handled.current || typeof window === "undefined") return;
    const params = new URLSearchParams(window.location.search);
    const result = params.get("codeHostLink");
    if (!result) return;
    handled.current = true;
    if (result === "linked") toast.success(t("Account connected"));
    else toast.error(params.get("error") || t("Could not connect the account"));
    params.delete("codeHostLink");
    params.delete("error");
    const query = params.toString();
    window.history.replaceState(
      window.history.state,
      "",
      `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`,
    );
  }, [t]);
}
