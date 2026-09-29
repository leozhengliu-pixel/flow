import type { BootstrapData } from "@/types/flow";
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
  runId?: string;
  /** list (`/loops`), edit (`/loops/new`, `/loop/:id/edit`), detail (`/loop/:id`), runs (`/loop/:id/run/:runId`). */
  mode?: "list" | "edit" | "detail" | "runs";
  editing: boolean;
  onOpenSidebar: () => void;
  onNavigate: (path: string) => void;
  onReload: () => Promise<void>;
  teamId?: string;
};

export function LoopsPage({ data, draftId, loopId, runId, mode, editing, onOpenSidebar, onNavigate, onReload }: Props) {
  const view = mode ?? (editing ? "edit" : loopId ? "detail" : "list");
  if (view === "edit") {
    // `/loops/new` without a draft: every entry goes through the creation hub first.
    if (!draftId && !loopId) return <LoopList data={data} embedded={false} createOpen onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
    return <LoopEditor data={data} draftId={draftId} loopId={draftId ? undefined : loopId} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
  }
  if (view === "detail" && loopId) return <LoopDetail data={data} loopId={loopId} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
  if (view === "runs" && loopId) return <LoopRunPage data={data} loopId={loopId} runId={runId} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} />;
  return <LoopList data={data} embedded={false} onOpenSidebar={onOpenSidebar} onNavigate={onNavigate} onReload={onReload} />;
}

export function LoopsDirectory({ data, embedded = false, onNavigate, onOpenSidebar, onReload, teamId }: Omit<Props, "editing" | "loopId">) {
  return <LoopList data={data} embedded={embedded} onNavigate={onNavigate} onOpenSidebar={onOpenSidebar} onReload={onReload} teamId={teamId} />;
}
