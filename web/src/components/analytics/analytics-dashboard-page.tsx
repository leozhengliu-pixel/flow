import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import {
  BarChart3,
  Check,
  ChevronDown,
  Copy,
  Download,
  Expand,
  LayoutDashboard,
  LoaderCircle,
  Minimize2,
  MoreHorizontal,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import {
  createExport,
  exportDownloadUrl,
  getAnalyticsOverview,
  getExport,
} from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import type { ExportJob } from "@/types/flow";
import { InsightBar, InsightLine } from "./flow-insight-graph";
import "./analytics-dashboard-page.css";

type Overview = {
  issues?: { total?: number; active?: number };
  status?: Record<string, number>;
  team?: Record<string, number>;
  throughput?: Array<{ date: string; count: number }>;
  averageCycleTimeHours?: number;
  projects?: number;
  cycles?: number;
};
type Slice = "Status" | "Team" | "Completed date";
type RangeDays = 7 | 30 | 90 | 365;

export function AnalyticsDashboardPage() {
  const { t } = useI18n();
  const [overview, setOverview] = useState<Overview>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [slice, setSlice] = useState<Slice>("Status");
  const [showArchived, setShowArchived] = useState(false);
  const [rangeDays, setRangeDays] = useState<RangeDays>(30);
  const [exportId, setExportId] = useState<string>();
  const [exportStatus, setExportStatus] = useState<ExportJob["status"]>();
  const [exporting, setExporting] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const workspaceSlug = location.pathname.split("/")[1];
  const load = useCallback(async (days = rangeDays) => {
    setLoading(true);
    setError("");
    try {
      const since = new Date(Date.now() - days * 86_400_000).toISOString();
      setOverview((await getAnalyticsOverview(since)) as Overview);
    } catch {
      setOverview(undefined);
      setError(t("Could not load analytics"));
      throw new Error("analytics request failed");
    } finally {
      setLoading(false);
    }
  }, [rangeDays, t]);
  useEffect(() => {
    void load(rangeDays).catch(() => undefined);
  }, [load, rangeDays]);
  const entries = useMemo(
    () =>
      slice === "Status"
        ? Object.entries(overview?.status ?? {}).filter(([label]) => showArchived || !/archiv/i.test(label))
        : slice === "Team"
          ? Object.entries(overview?.team ?? {})
          : (overview?.throughput ?? []).map(
              (point) => [point.date, point.count] as [string, number],
            ),
    [overview, showArchived, slice],
  );
  const total = entries.reduce((sum, [, count]) => sum + count, 0);
  const points = entries.map(([label, value]) => ({ id: label, label, value }));
  const exportData = async () => {
    if (exporting) return;
    setExporting(true);
    setExportStatus("queued");
    try {
      const job = await createExport("csv", false);
      setExportId(job.id);
      let current = job;
      for (let attempt = 0; attempt < 12 && current.status === "queued"; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 150));
        current = await getExport(job.id);
        setExportStatus(current.status);
      }
      setExportStatus(current.status);
      if (current.status === "completed") toast.success(t("Export ready"));
      else toast.error(current.error || t("Export failed"));
    } catch {
      setExportStatus("failed");
      toast.error(t("Could not export analytics"));
    } finally {
      setExporting(false);
    }
  };
  if (error && !overview)
    return (
      <main className="main-panel insights-page">
        <section className="insights-state insights-state-error" role="alert">
          <BarChart3 />
          <strong>{error}</strong>
          <button type="button" onClick={() => void load().catch(() => undefined)}>
            <RefreshCw />
            {t("Try again")}
          </button>
        </section>
      </main>
    );
  if (loading && !overview)
    return (
      <main className="main-panel insights-page">
        <LoaderCircle className="insights-spin" />
      </main>
    );
  return (
    <main className={`main-panel insights-page${fullscreen ? " is-fullscreen" : ""}`}>
      <header className="insights-header">
        <div>
          <BarChart3 />
          <strong>{total || overview?.issues?.total || 0}</strong>
          <span>{t("issues")}</span>
        </div>
        <nav>
          <button aria-label={t(fullscreen ? "Exit fullscreen" : "Expand to fullscreen")} onClick={() => setFullscreen((value) => !value)} type="button">
            {fullscreen ? <Minimize2 /> : <Expand />}
          </button>
          <DisplayMenu
            showArchived={showArchived}
            onShowArchived={setShowArchived}
          />
          <ActionsMenu
            exportId={exportId}
            exportStatus={exportStatus}
            exporting={exporting}
            onExport={() => void exportData()}
            onRefresh={() => void load().catch(() => undefined)}
          />
          <Link to={`/${workspaceSlug}/dashboards`}>
            <LayoutDashboard />
            {t("Dashboards")}
          </Link>
        </nav>
      </header>
      <section className="insights-controls">
        <Control
          label={t("Measure")}
          value={t("Issue count")}
          options={["Issue count"]}
        />
        <Control
          label={t("Slice")}
          value={t(slice)}
          options={["Status", "Team", "Completed date"]}
          onChange={(value) => setSlice(value as Slice)}
        />
        <Control
          label={t("Segment")}
          value={t("No segment")}
          options={["No segment"]}
        />
        <Control
          label={t("Date range")}
          value={t(rangeDays === 7 ? "Last 7 days" : rangeDays === 90 ? "Last 90 days" : rangeDays === 365 ? "Last year" : "Last 30 days")}
          options={["Last 7 days", "Last 30 days", "Last 90 days", "Last year"]}
          onChange={(value) => setRangeDays(value === "Last 7 days" ? 7 : value === "Last 90 days" ? 90 : value === "Last year" ? 365 : 30)}
        />
      </section>
      <section
        className="insights-chart"
        aria-label={t("Issue count by slice")}
      >
        {!points.length ? (
          <div className="insights-state insights-state-empty">
            <BarChart3 />
            <strong>{t("No data")}</strong>
            <p>{t("Try a different date range or filter.")}</p>
          </div>
        ) : slice === "Completed date" ? (
          <InsightLine points={points} />
        ) : (
          <InsightBar points={points} />
        )}
      </section>
      <section className="insights-table">
        <header>
          <strong>{t(slice)}</strong>
          <strong>{t("Issue count")}</strong>
          <strong>%</strong>
        </header>
        {entries.map(([label, count]) => (
          <div key={label}>
            <span data-i18n-ignore>{label}</span>
            <strong>{count}</strong>
            <span>{total ? Math.round((count / total) * 100) : 0}%</span>
          </div>
        ))}
        {!entries.length && <p>{t("No data")}</p>}
      </section>
    </main>
  );
}
export default AnalyticsDashboardPage;

function Control({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: string[];
  onChange?: (value: string) => void;
}) {
  const { t } = useI18n();
  if (options.length <= 1) return <label><span>{label}</span><button aria-label={label} aria-disabled="true" disabled type="button">{value}</button></label>
  return (
    <label>
      <span>{label}</span>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button aria-label={label} type="button">
            {value}
            <ChevronDown />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            className="insights-menu"
            align="start"
            sideOffset={4}
          >
            {options.map((option) => (
              <DropdownMenu.Item key={option} onSelect={() => onChange?.(option)}>
                {t(option)}
                {t(option) === value && <Check />}
              </DropdownMenu.Item>
            ))}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </label>
  );
}

function DisplayMenu({
  showArchived,
  onShowArchived,
}: {
  showArchived: boolean;
  onShowArchived: (value: boolean) => void;
}) {
  const { t } = useI18n();
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button aria-label={t("Insights display options")} type="button">
          <SlidersHorizontal />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="insights-menu"
          align="end"
          sideOffset={4}
        >
          <DropdownMenu.CheckboxItem
            checked={showArchived}
            onCheckedChange={(value) => onShowArchived(value === true)}
            onSelect={(event) => event.preventDefault()}
          >
            {showArchived && <Check />}
            {t("Show archived issues")}
          </DropdownMenu.CheckboxItem>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

function ActionsMenu({
  onExport,
  onRefresh,
  exportId,
  exportStatus,
  exporting,
}: {
  onExport: () => void;
  onRefresh: () => void;
  exportId?: string;
  exportStatus?: ExportJob["status"];
  exporting: boolean;
}) {
  const { t } = useI18n();
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button aria-label={t("Open menu")} type="button">
          <MoreHorizontal />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content
          className="insights-menu"
          align="end"
          sideOffset={4}
        >
          <DropdownMenu.Item
            onSelect={() => void navigator.clipboard.writeText(location.href)}
          >
            <Copy />
            {t("Copy link")}
          </DropdownMenu.Item>
          <DropdownMenu.Item disabled={exporting} onSelect={onExport}>
            <Download />
            {t(exporting ? "Preparing export…" : "Export insights as CSV…")}
          </DropdownMenu.Item>
          {exportId && exportStatus === "completed" && (
            <DropdownMenu.Item asChild>
              <a href={exportDownloadUrl(exportId)} download>
                <Download />
                {t("Download latest")}
              </a>
            </DropdownMenu.Item>
          )}
          <DropdownMenu.Separator />
          <DropdownMenu.Item onSelect={onRefresh}>
            <RefreshCw />
            {t("Refresh")}
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
