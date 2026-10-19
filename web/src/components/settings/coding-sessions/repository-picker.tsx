import { useEffect, useRef, useState } from "react";
import * as Popover from "@radix-ui/react-popover";
import { Lock } from "lucide-react";
import { FlowGlyph } from "@/components/ui/menu-glyphs";
import { useI18n } from "@/i18n/i18n";
import { fetchCodingRepositories } from "@/lib/api";
import type { CodeHostKind, CodeHostRepository } from "@/types/flow";
import { errorMessage } from "./use-coding-settings";

const REPO_PATH = /^[\w.-]+(\/[\w.-]+)+$/;

/**
 * Repository search through `/api/coding/repositories` (the code host's own listing, so only
 * repositories the GitHub App installation or GitLab connection can reach are offered).
 */
export function RepositoryPicker({ codeHost, hostName, value, disabled, onChange }: { codeHost: CodeHostKind; hostName: string; value: string; disabled?: boolean; onChange: (repository: Pick<CodeHostRepository, "fullName" | "id" | "defaultBranch">) => void }) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<CodeHostRepository[]>([]);
  const [cursor, setCursor] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const request = useRef(0);

  useEffect(() => {
    if (!open) return;
    const id = ++request.current;
    setLoading(true);
    const timer = setTimeout(() => {
      fetchCodingRepositories({ codeHost, hostName, q: query.trim() || undefined })
        .then((page) => {
          if (id !== request.current) return;
          setResults((page?.repositories ?? []).filter((repository) => !repository.archived));
          setCursor(page?.nextCursor);
          setError(undefined);
        })
        .catch((reason) => id === request.current && setError(errorMessage(reason, "Could not load repositories")))
        .finally(() => id === request.current && setLoading(false));
    }, query ? 200 : 0);
    return () => clearTimeout(timer);
  }, [open, query, codeHost, hostName]);

  const loadMore = async () => {
    if (!cursor) return;
    const id = ++request.current;
    setLoading(true);
    try {
      const page = await fetchCodingRepositories({ codeHost, hostName, q: query.trim() || undefined, cursor });
      if (id !== request.current) return;
      setResults((current) => [...current, ...(page?.repositories ?? []).filter((repository) => !repository.archived)]);
      setCursor(page?.nextCursor);
    } catch (reason) {
      setError(errorMessage(reason, "Could not load repositories"));
    } finally {
      if (id === request.current) setLoading(false);
    }
  };

  const pick = (repository: Pick<CodeHostRepository, "fullName" | "id" | "defaultBranch">) => {
    onChange(repository);
    setOpen(false);
    setQuery("");
  };
  const typed = query.trim();
  const canUseTyped = REPO_PATH.test(typed) && !results.some((repository) => repository.fullName.toLowerCase() === typed.toLowerCase());

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button type="button" className="feature-select cs-repo-trigger" aria-label={t("Repository")} disabled={disabled}>
          <span data-i18n-ignore={value ? true : undefined}>{value || t("Select repository…")}</span>
          <FlowGlyph name="selectChevron" width={10} height={5} />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content data-flow-motion="floating" align="end" sideOffset={6} className="cs-repo-popover">
          <input
            autoFocus
            className="cs-repo-search"
            aria-label={t("Search repositories")}
            placeholder={t("Search repositories…")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && canUseTyped) {
                event.preventDefault();
                pick({ fullName: typed, defaultBranch: "" });
              }
            }}
          />
          <div className="cs-repo-results">
            {results.map((repository) => (
              <button type="button" key={repository.id ?? repository.fullName} className={`cs-repo-option${repository.fullName === value ? " is-selected" : ""}`} onClick={() => pick(repository)}>
                <span data-i18n-ignore>{repository.fullName}</span>
                {repository.private && <Lock size={12} aria-label={t("Private")} />}
              </button>
            ))}
            {canUseTyped && (
              <button type="button" className="cs-repo-option" onClick={() => pick({ fullName: typed, defaultBranch: "" })}>
                <span>{t("Use {repository}").replace("{repository}", typed)}</span>
              </button>
            )}
            {loading && <p className="cs-repo-note">{t("Loading…")}</p>}
            {!loading && error && <p className="cs-repo-note is-error" role="alert">{error}</p>}
            {!loading && !error && !results.length && !canUseTyped && <p className="cs-repo-note">{t("No repositories found")}</p>}
            {cursor && !loading && (
              <button type="button" className="cs-repo-option is-more" onClick={() => void loadMore()}>{t("Load more")}</button>
            )}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
