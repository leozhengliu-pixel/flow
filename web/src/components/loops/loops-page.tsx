import type { LoopsRouteView } from "@/lib/app-routes";
import type { BootstrapData, Loop } from "@/types/flow";
import { LoopDetail } from "./loop-detail";
import { LoopEditor } from "./loop-editor";
import { LoopList } from "./loop-list";
import { LoopRunPage } from "./loop-run-page";
import "./loops-page.css";

type Props = {
  data: BootstrapData;
  embedded?: boolean;
  loopId?: string;
  draftId?: string;
  pendingCreateId?: string;
  runId?: string;
  /** list (`/loops/my-loops`, `/loops/all`), edit (`/loops/new`, `/loop/:id/edit`), detail (`/loop/:id`), runs (`/loop/:id/run/:runId`). */
  mode?: "list" | "edit" | "detail" | "runs";
  editing: boolean;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  /** Refreshes the workspace's loops; `changed` is applied locally first so the next page sees it at once. */
  onReload: (changed?: Loop) => Promise<void>;
  teamId?: string;
  /** `/team/<key>/loops/new`: the new loop was started from that team's Loops tab. */
  teamKey?: string;
  /** The workspace list's routed view: "My loops" or "All". */
  loopsView?: LoopsRouteView;
};

export function LoopsPage({ data, draftId, pendingCreateId, loopId, runId, mode, editing, onOpenSidebar, onNavigate, onReload, teamKey, loopsView }: Props) {
  const view = mode ?? (editing ? "edit" : loopId ? "detail" : "list");
  if (view === "edit") {
    // `/loops/new` (or a team's `/loops/new`) without a draft: every entry goes through the creation hub first.
    if (!draftId && !loopId) {
      const teamId = teamKey ? data.teams.find((team) => team.key.toLowerCase() === teamKey.toLowerCase())?.id : undefined;
      return <LoopList data={data} embedded={false} createOpen teamId={teamId} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
    }
    return <LoopEditor data={data} draftId={draftId} pendingCreateId={pendingCreateId} loopId={draftId ? undefined : loopId} teamKey={draftId || pendingCreateId ? teamKey : undefined} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
  }
  if (view === "detail" && loopId) return <LoopDetail data={data} loopId={loopId} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
  if (view === "runs" && loopId) return <LoopRunPage data={data} loopId={loopId} runId={runId} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} />;
  return <LoopList data={data} embedded={false} view={loopsView} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
}

export function LoopsDirectory({ data, embedded = false, onNavigate, onOpenSidebar, onReload, teamId }: Omit<Props, "editing" | "loopId">) {
  return <LoopList data={data} embedded={embedded} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onReload={onReload} teamId={teamId} />;
}
