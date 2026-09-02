import { ResponsiveBar } from "@nivo/bar";
import { ResponsiveLine } from "@nivo/line";
import { useMemo } from "react";

import { useI18n } from "@/i18n/i18n";
import "./flow-insight-graph.css";

export type InsightPoint = {
  id: string;
  label: string;
  value: number;
  color?: string;
};

const palette = [
  "var(--accent-primary)",
  "var(--semantic-warning)",
  "var(--semantic-success)",
  "var(--chart-accent)",
  "var(--semantic-danger)",
];

const theme = {
  background: "transparent",
  text: { fill: "var(--theme-text-tertiary)", fontSize: 11 },
  axis: {
    domain: { line: { stroke: "var(--theme-border)", strokeWidth: 1 } },
    ticks: {
      line: { stroke: "transparent" },
      text: { fill: "var(--theme-text-tertiary)", fontSize: 10 },
    },
  },
  grid: {
    line: {
      stroke: "var(--theme-border)",
      strokeWidth: 1,
      strokeDasharray: "3 3",
    },
  },
  tooltip: {
    container: {
      background: "var(--theme-surface-2)",
      color: "var(--theme-text-primary)",
      border: "0.5px solid var(--theme-border)",
      borderRadius: "8px",
      boxShadow: "var(--theme-shadow-popover)",
      fontSize: "12px",
    },
  },
} as const;

export function InsightBar({
  points,
  onSelect,
}: {
  points: InsightPoint[];
  onSelect?: (point: InsightPoint) => void;
}) {
  const { t } = useI18n();
  const data = useMemo(
    () =>
      points.map((point, index) => ({
        id: point.id,
        label: point.label,
        value: point.value,
        color: point.color ?? palette[index % palette.length],
      })),
    [points],
  );
  return (
    <div
      className="flow-insight-graph"
      role="img"
      aria-label={t("Insight bar chart")}
    >
      <ResponsiveBar
        animate={false}
        axisBottom={{ tickSize: 0, tickPadding: 8, truncateTickAt: 12 }}
        axisLeft={null}
        axisRight={{ tickSize: 0, tickPadding: 6, tickValues: 5 }}
        axisTop={null}
        borderRadius={2}
        colors={({ data: row }) => String(row.color)}
        data={data}
        enableGridX={false}
        enableGridY
        enableLabel={false}
        indexBy="label"
        keys={["value"]}
        margin={{ top: 10, right: 34, bottom: 38, left: 4 }}
        padding={0.78}
        role="img"
        theme={theme}
        tooltip={({ indexValue, value, color }) => (
          <div className="flow-insight-tooltip">
            <i style={{ background: color }} />
            <span>{String(indexValue)}</span>
            <strong>{value}</strong>
          </div>
        )}
        onClick={(bar) => {
          const point = points.find(
            (item) => item.label === String(bar.indexValue),
          );
          if (point) onSelect?.(point);
        }}
      />
    </div>
  );
}

export function InsightLine({
  points,
  onSelect,
}: {
  points: InsightPoint[];
  onSelect?: (point: InsightPoint) => void;
}) {
  const { t } = useI18n();
  const data = useMemo(
    () => [
      {
        id: "value",
        color: "var(--accent-primary)",
        data: points.map((point) => ({
          x: point.label,
          y: point.value,
          source: point,
        })),
      },
    ],
    [points],
  );
  return (
    <div
      className="flow-insight-graph"
      role="img"
      aria-label={t("Insight line chart")}
    >
      <ResponsiveLine
        animate={false}
        axisBottom={{ tickSize: 0, tickPadding: 8, truncateTickAt: 10 }}
        axisLeft={null}
        axisRight={{ tickSize: 0, tickPadding: 6, tickValues: 5 }}
        axisTop={null}
        colors={["var(--accent-primary)"]}
        curve="monotoneX"
        data={data}
        enableArea
        enableGridX={false}
        enableGridY
        enablePoints
        lineWidth={1.5}
        margin={{ top: 12, right: 34, bottom: 38, left: 4 }}
        pointBorderColor="var(--theme-surface-0)"
        pointBorderWidth={1}
        pointColor="var(--accent-primary)"
        pointSize={6}
        theme={theme}
        useMesh
        onClick={(point) => {
          if (!("data" in point)) return;
          const source = (point.data as { source?: InsightPoint }).source;
          if (source) onSelect?.(source);
        }}
      />
    </div>
  );
}
