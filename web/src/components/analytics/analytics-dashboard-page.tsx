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
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import {
  createExport,
  exportDownloadUrl,
  getAnalyticsOverview,
} from "@/lib/api";
import { useI18n } from "@/i18n/i18n";
import { LinearInsightBar, LinearInsightLine } from "./linear-insight-graph";
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

export function AnalyticsDashboardPage() {
  const { t } = useI18n();
  const [overview, setOverview] = useState<Overview>();
  const [loading, setLoading] = useState(true);
  const [slice, setSlice] = useState<Slice>("Status");
  const [showArchived, setShowArchived] = useState(false);
  const [hideNoPriority, setHideNoPriority] = useState(false);
  const [exportId, setExportId] = useState<string>();
  const [fullscreen, setFullscreen] = useState(false);
  const workspaceSlug = location.pathname.split("/")[1];
  const load = async () => {
    setLoading(true);
    try {
      setOverview((await getAnalyticsOverview()) as Overview);
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void load();
  }, []);
  const entries = useMemo(
    () =>
      slice === "Status"
        ? Object.entries(overview?.status ?? {})
        : slice === "Team"
          ? Object.entries(overview?.team ?? {})
          : (overview?.throughput ?? []).map(
              (point) => [point.date, point.count] as [string, number],
            ),
    [overview, slice],
  );
  const total = entries.reduce((sum, [, count]) => sum + count, 0);
  const points = entries.map(([label, value]) => ({ id: label, label, value }));
  const exportData = async () => {
    const job = await createExport("csv", false);
    setExportId(job.id);
    toast.success(t("Export ready"));
  };
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
            hideNoPriority={hideNoPriority}
            showArchived={showArchived}
            onHideNoPriority={setHideNoPriority}
            onShowArchived={setShowArchived}
          />
          <ActionsMenu
            exportId={exportId}
            onExport={() => void exportData()}
            onRefresh={() => void load()}
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
      </section>
      <section
        className="insights-chart"
        aria-label={t("Issue count by slice")}
      >
        {slice === "Completed date" ? (
          <LinearInsightLine points={points} />
        ) : (
          <LinearInsightBar points={points} />
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
  if (options.length <= 1) return <label><span>{label}</span><button aria-disabled="true" disabled type="button">{value}</button></label>
  return (
    <label>
      <span>{label}</span>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button type="button">
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
  hideNoPriority,
  onShowArchived,
  onHideNoPriority,
}: {
  showArchived: boolean;
  hideNoPriority: boolean;
  onShowArchived: (value: boolean) => void;
  onHideNoPriority: (value: boolean) => void;
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
          <DropdownMenu.CheckboxItem
            checked={hideNoPriority}
            onCheckedChange={(value) => onHideNoPriority(value === true)}
            onSelect={(event) => event.preventDefault()}
          >
            {hideNoPriority && <Check />}
            {t("Hide no priority")}
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
}: {
  onExport: () => void;
  onRefresh: () => void;
  exportId?: string;
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
          <DropdownMenu.Item onSelect={onExport}>
            <Download />
            {t("Export insights as CSV…")}
          </DropdownMenu.Item>
          {exportId && (
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
