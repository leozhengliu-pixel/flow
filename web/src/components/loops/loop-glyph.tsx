import type { ComponentType } from "react";
import {
  ArrowRight,
  Bell,
  Bug,
  CalendarClock,
  ChartColumn,
  Clock3,
  Code2,
  FileText,
  Inbox,
  Lightbulb,
  MessageSquare,
  Route,
  Send,
  ShieldAlert,
  Sparkles,
  Timer,
  UserRoundCheck,
  Zap,
  type LucideProps,
} from "lucide-react";
import { ViewGlyph } from "@/components/views/view-icon-picker";
import { loopIconColor, templateVisualOf, type LoopVisualSource } from "./loop-template-visuals";
import { FLOW_VIEW_ICON_ALIASES, FLOW_VIEW_ICON_NAMES } from "@/components/views/flow-view-icon-data";

const VIEW_ICONS = new Set<string>(FLOW_VIEW_ICON_NAMES);

const LUCIDE: Record<string, ComponentType<LucideProps>> = {
  bug: Bug,
  triage: Inbox,
  inbox: Inbox,
  slack: MessageSquare,
  message: MessageSquare,
  chat: MessageSquare,
  comment: MessageSquare,
  reply: MessageSquare,
  calendar: CalendarClock,
  schedule: CalendarClock,
  weekly: CalendarClock,
  hourly: Clock3,
  clock: Clock3,
  timer: Timer,
  report: ChartColumn,
  chart: ChartColumn,
  lightbulb: Lightbulb,
  idea: Lightbulb,
  feature: Lightbulb,
  shield: ShieldAlert,
  security: ShieldAlert,
  alert: Bell,
  bell: Bell,
  code: Code2,
  coding: Code2,
  route: Route,
  owner: UserRoundCheck,
  assign: UserRoundCheck,
  send: Send,
  share: Send,
  post: Send,
  document: FileText,
  doc: FileText,
  change: Zap,
  event: Zap,
  sparkles: Sparkles,
  arrow: ArrowRight,
};

/** Icon names from the server (templates, loops) rendered with Lucide when known, else the Flow view icon set. */
export function LoopGlyph({ icon, color = "currentColor", size = 16, className }: { icon?: string; color?: string; size?: number; className?: string }) {
  // Icons picked in the editor come from the Flow view icon set.
  if (icon && (VIEW_ICONS.has(icon) || FLOW_VIEW_ICON_ALIASES[icon] || icon === "Automation"))
    return <ViewGlyph className={className} color={color} icon={icon} style={{ width: size, height: size }} />;
  const key = (icon ?? "").toLowerCase().replace(/[^a-z]/g, "");
  const match = LUCIDE[key] ?? Object.entries(LUCIDE).find(([name]) => key.includes(name))?.[1];
  if (match) {
    const Icon = match;
    return <Icon aria-hidden="true" className={className} color={color} size={size} />;
  }
  return <ViewGlyph className={className} color={color} icon={icon || "Automation"} style={{ width: size, height: size }} />;
}

/** Loop / template icon: template loops show Linear's template glyph, others their picked icon. */
export function LoopIcon({ source, size = 14 }: { source: LoopVisualSource; size?: number }) {
  const visual = templateVisualOf(source);
  if (visual) return <>{visual.render(size, loopIconColor(source) ?? visual.color)}</>;
  return <LoopGlyph icon={source.icon || "Automation"} size={size} />;
}
