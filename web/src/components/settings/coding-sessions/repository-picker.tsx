import { useLatestRef } from '@/hooks/use-latest-ref'
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import * as Popover from "@radix-ui/react-popover";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { useI18n } from "@/i18n/i18n";
import { fetchCodingRepositories } from "@/lib/api";
import type { CodeHostKind, CodeHostRepository } from "@/types/flow";
import { IntegrationBrandIcon } from "../integration-brand-icon";
import { errorMessage } from "./use-coding-settings";

const REPO_PATH = /^[\w.-]+(\/[\w.-]+)+$/;

export type RepositoryHost = { codeHost: CodeHostKind; hostName: string };
export type PickedRepository = Pick<CodeHostRepository, "fullName" | "id" | "defaultBranch"> & RepositoryHost;

const hostKey = (host: RepositoryHost) => `${host.codeHost}\u0000${host.hostName}`;

/**
 * Repository search through `/api/coding/repositories` (the code host's own listing, so only
 * repositories the GitHub App installation or GitLab connection can reach are offered). Several
 * connected hosts are searched together; each option carries its host mark.
 *
 * Measured against the reference app's "New environment" popover: 278px wide, 12px radius, a 37px
 * search row with a bottom border, and 32px options (16px host mark, 8px gap, 13px/450 name).
 */
export function RepositoryPickerPopover({ hosts, value, open, onOpenChange, onPick, children, align = "end" }: { hosts: RepositoryHost[]; value?: string; open: boolean; onOpenChange: (open: boolean) => void; onPick: (repository: PickedRepository) => void; children: ReactNode; align?: "start" | "end" }) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<(CodeHostRepository & RepositoryHost)[]>([]);
  const [cursors, setCursors] = useState<Record<string, string | undefined>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const request = useRef(0);
  const list = useRef<HTMLDivElement>(null);
  const hostsKey = hosts.map(hostKey).join("|");
  // Callers rebuild the hosts array on every render; hostsKey is what identifies it.
  const hostsRef = useLatestRef(hosts);

  useEffect(() => {
    const hosts = hostsRef.current;
    if (!open || !hosts.length) return;
    const id = ++request.current;
    setLoading(true);
    const q = query.trim() || undefined;
    const timer = setTimeout(() => {
      Promise.all(hosts.map((host) => fetchCodingRepositories({ codeHost: host.codeHost, hostName: host.hostName, q }).then((page) => ({ host, page }))))
        .then((pages) => {
          if (id !== request.current) return;
          setResults(pages.flatMap(({ host, page }) => (page?.repositories ?? []).filter((repository) => !repository.archived).map((repository) => ({ ...repository, ...host }))));
          setCursors(Object.fromEntries(pages.map(({ host, page }) => [hostKey(host), page?.nextCursor])));
          setError(undefined);
        })
        .catch((reason) => id === request.current && setError(errorMessage(reason, "Could not load repositories")))
        .finally(() => id === request.current && setLoading(false));
    }, query ? 200 : 0);
    return () => clearTimeout(timer);
  }, [open, query, hostsKey, hostsRef]);

  const more = hosts.filter((host) => cursors[hostKey(host)]);
  const loadMore = async () => {
    if (!more.length) return;
    const id = ++request.current;
    setLoading(true);
    const q = query.trim() || undefined;
    try {
      const pages = await Promise.all(more.map((host) => fetchCodingRepositories({ codeHost: host.codeHost, hostName: host.hostName, q, cursor: cursors[hostKey(host)] }).then((page) => ({ host, page }))));
      if (id !== request.current) return;
      setResults((current) => [...current, ...pages.flatMap(({ host, page }) => (page?.repositories ?? []).filter((repository) => !repository.archived).map((repository) => ({ ...repository, ...host })))]);
      setCursors((current) => ({ ...current, ...Object.fromEntries(pages.map(({ host, page }) => [hostKey(host), page?.nextCursor])) }));
    } catch (reason) {
      setError(errorMessage(reason, "Could not load repositories"));
    } finally {
      if (id === request.current) setLoading(false);
    }
  };

  const close = (next: boolean) => {
    onOpenChange(next);
    if (!next) setQuery("");
  };
  const pick = (repository: PickedRepository) => {
    onPick(repository);
    close(false);
  };
  const typed = query.trim();
  const canUseTyped = hosts.length > 0 && REPO_PATH.test(typed) && !results.some((repository) => repository.fullName.toLowerCase() === typed.toLowerCase());
  const pickTyped = () => pick({ fullName: typed, defaultBranch: "", ...hosts[0] });

  // Arrow keys move between the search field and the options.
  const move = (event: KeyboardEvent, from: number) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    const options = [...(list.current?.querySelectorAll<HTMLButtonElement>(".cs-repo-option") ?? [])];
    if (!options.length) return;
    event.preventDefault();
    const next = event.key === "ArrowDown" ? Math.min(options.length - 1, from + 1) : from - 1;
    if (next < 0) list.current?.parentElement?.querySelector<HTMLInputElement>(".cs-repo-search")?.focus();
    else options[next].focus();
  };

  return (
    <Popover.Root open={open} onOpenChange={close}>
      <Popover.Trigger asChild>{children}</Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" align={align} sideOffset={6} className="cs-repo-popover" data-testid="repository-picker">
          <div className="cs-repo-search-row">
            <input
              autoFocus
              className="cs-repo-search"
              aria-label={t("Search repositories")}
              placeholder={t("Search repositories...")}
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  if (canUseTyped) pickTyped();
                  else if (results[0]) pick(results[0]);
                  return;
                }
                move(event, -1);
              }}
            />
          </div>
          <div className="cs-repo-results" ref={list}>
            {results.map((repository, index) => (
              <button type="button" key={`${hostKey(repository)}:${repository.id ?? repository.fullName}`} className={`cs-repo-option${repository.fullName === value ? " is-selected" : ""}`} onKeyDown={(event) => move(event, index)} onClick={() => pick(repository)}>
                <span className="cs-repo-option-inner">
                  <IntegrationBrandIcon provider={repository.codeHost} size={16} />
                  <span data-i18n-ignore>{repository.fullName}</span>
                </span>
              </button>
            ))}
            {canUseTyped && (
              <button type="button" className="cs-repo-option" onKeyDown={(event) => move(event, results.length)} onClick={pickTyped}>
                <span className="cs-repo-option-inner">
                  <IntegrationBrandIcon provider={hosts[0].codeHost} size={16} />
                  <span>{t("Use {repository}").replace("{repository}", typed)}</span>
                </span>
              </button>
            )}
            {loading && <p className="cs-repo-note">{t("Loading…")}</p>}
            {!loading && error && <p className="cs-repo-note is-error" role="alert">{error}</p>}
            {!loading && !error && !results.length && !canUseTyped && <p className="cs-repo-note">{t("No repositories found")}</p>}
            {more.length > 0 && !loading && (
              <button type="button" className="cs-repo-option is-more" onClick={() => void loadMore()}>
                <span className="cs-repo-option-inner">{t("Load more")}</span>
              </button>
            )}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

/** The editor's repository select (new environments only): a `.feature-select` trigger over the picker. */
export function RepositoryPicker({ codeHost, hostName, value, disabled, onChange }: { codeHost: CodeHostKind; hostName: string; value: string; disabled?: boolean; onChange: (repository: PickedRepository) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <RepositoryPickerPopover hosts={[{ codeHost, hostName }]} value={value} open={open} onOpenChange={setOpen} onPick={onChange}>
      <button type="button" className="feature-select cs-repo-trigger" aria-label={t("Repository")} disabled={disabled}>
        <span className="cs-repo-trigger-label">
          {value && <IntegrationBrandIcon provider={codeHost} size={16} />}
          <span data-i18n-ignore={value ? true : undefined}>{value || t("Select repository…")}</span>
        </span>
        <FlowGlyph name="selectChevron" width={10} height={5} />
      </button>
    </RepositoryPickerPopover>
  );
}
