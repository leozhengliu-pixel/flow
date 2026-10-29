import { useEffect, useRef, useState } from "react";
import { FileText, Plus } from "lucide-react";
import { SelectControl } from "@/components/ui/select-control";
import { useI18n } from "@/i18n/i18n";
import { createDocument, searchDocuments } from "@/lib/api";
import { toast } from "@/lib/toast";
import type { BootstrapData, FlowDocument } from "@/types/flow";
import { activeTeams } from "./loop-data";
import { LoopCommandPicker, type PickerItem } from "./loop-pickers";

const MORE = "__more__";
const CREATE = "__create__";

/**
 * "Choose document…": the workspace's documents searched on the server (title and content, a page
 * at a time, optionally in one team), as the loop's other command pickers look, with "Create a new document" for an
 * output whose document does not exist yet.
 */
export function LoopDocumentPicker({
  data,
  open,
  teamId,
  newTitle,
  onOpenChange,
  onSelect,
}: {
  data: Pick<BootstrapData, "teams">;
  open: boolean;
  /** The team the search starts in (the output's or the loop's). */
  teamId?: string;
  /** The title a new document gets when the search is empty. */
  newTitle?: string;
  onOpenChange: (open: boolean) => void;
  onSelect: (document: Pick<FlowDocument, "id" | "title" | "teamIds">) => void;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [team, setTeam] = useState(teamId ?? "");
  const [documents, setDocuments] = useState<FlowDocument[]>([]);
  const [cursor, setCursor] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [failed, setFailed] = useState(false);
  const request = useRef(0);

  useEffect(() => {
    if (!open) return;
    const id = ++request.current;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setLoading(true);
      searchDocuments({ q: query, teamId: team || undefined }, controller.signal)
        .then((page) => {
          if (id !== request.current) return;
          setDocuments(page.nodes.filter((document) => !document.archivedAt));
          setCursor(page.pageInfo.hasNextPage ? (page.pageInfo.endCursor ?? undefined) : undefined);
          setFailed(false);
        })
        .catch(() => id === request.current && !controller.signal.aborted && setFailed(true))
        .finally(() => id === request.current && setLoading(false));
    }, query ? 200 : 0);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [open, query, team]);

  const loadMore = async () => {
    if (!cursor) return;
    const id = ++request.current;
    setLoading(true);
    try {
      const page = await searchDocuments({ q: query, teamId: team || undefined, after: cursor });
      if (id !== request.current) return;
      setDocuments((current) => [...current, ...page.nodes.filter((document) => !document.archivedAt && !current.some((item) => item.id === document.id))]);
      setCursor(page.pageInfo.hasNextPage ? (page.pageInfo.endCursor ?? undefined) : undefined);
    } catch {
      setFailed(true);
    } finally {
      if (id === request.current) setLoading(false);
    }
  };

  const close = (next: boolean) => {
    if (!next) {
      // The next opening starts over in the output's team.
      setQuery("");
      setTeam(teamId ?? "");
    }
    onOpenChange(next);
  };

  const create = async () => {
    const title = query.trim() || newTitle?.trim() || t("Untitled");
    setCreating(true);
    try {
      const document = await createDocument({ title, teamIds: team ? [team] : [] });
      onSelect(document);
      close(false);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t("Could not create the document"));
    } finally {
      setCreating(false);
    }
  };

  const teamKey = (ids: string[] | undefined) => (ids ?? []).map((id) => data.teams.find((item) => item.id === id)?.key).filter(Boolean).join(", ");
  const typed = query.trim();
  const items: PickerItem[] = [
    ...documents.map((document) => ({ id: document.id, label: document.title || t("Untitled"), entity: true, icon: <FileText size={14} />, detail: teamKey(document.teamIds) || undefined, detailPosition: "after" as const, preview: typed && document.searchSnippet ? <DocumentMatchSnippet text={document.searchSnippet} terms={document.matchedTerms ?? [typed]} /> : undefined })),
    ...(cursor ? [{ id: MORE, label: "Load more", disabled: loading }] : []),
    {
      id: CREATE,
      // Already translated: the typed title is the user's.
      label: typed ? t("Create a new document “{title}”").replace("{title}", () => typed) : t("Create a new document"),
      entity: true,
      icon: <Plus size={14} />,
      disabled: creating,
    },
  ];
  return (
    <LoopCommandPicker
      open={open}
      title="Choose document…"
      placeholder="Search documents…"
      heading={typed ? undefined : "Recent documents"}
      items={items}
      emptyLabel={loading ? "Loading…" : "No documents found."}
      onOpenChange={close}
      onQueryChange={setQuery}
      onSelect={(item) => {
        if (item.id === MORE) return void loadMore();
        if (item.id === CREATE) return void create();
        const document = documents.find((candidate) => candidate.id === item.id);
        if (!document) return;
        onSelect(document);
        close(false);
      }}
      footer={
        <span className="loops-picker-filter">
          <SelectControl
            label={t("Team")}
            value={team}
            onChange={setTeam}
            options={[{ value: "", label: t("All teams") }, ...activeTeams(data.teams).map((item) => ({ value: item.id, label: item.name, entityName: true }))]}
          />
          {loading && <small role="status">{t("Loading…")}</small>}
          {failed && !loading && <small role="alert">{t("Could not load documents")}</small>}
        </span>
      }
    />
  );
}

function DocumentMatchSnippet({ text, terms }: { text: string; terms: string[] }) {
  const escaped = terms.filter(Boolean).map((term) => term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
  if (!escaped.length) return <>{text}</>;
  const pattern = new RegExp(`(${escaped.join("|")})`, "gi");
  return <>{text.split(pattern).map((part, index) => index % 2 ? <mark key={index}>{part}</mark> : part)}</>;
}
