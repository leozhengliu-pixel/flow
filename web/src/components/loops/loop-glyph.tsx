import type { ComponentType, ReactNode } from "react";
import {
  ArrowRight,
  Bell,
  Bug,
  CalendarClock,
  CalendarDays,
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
import { WorkflowStatusGlyph } from "@/components/issue/issue-icons";
import { TRIAGE_STATUS } from "@/components/triage/triage-model";
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

type TemplateVisual = {
  /** Icon and color the server gives the template (and loops created from it). */
  serverIcon: string;
  serverColor: string;
  color: string;
  render: (size: number, color: string) => ReactNode;
};

function lucide(Icon: ComponentType<LucideProps>) {
  return (size: number, color: string) => <Icon aria-hidden="true" color={color} size={size} strokeWidth={2} />;
}

/** Linear's template icons: the orange Triage glyph, a cyan bug, a speech bubble, a calendar, a document, a red shield. */
const TEMPLATE_VISUALS: Record<string, TemplateVisual> = {
  "triage-agent": {
    serverIcon: "Triage",
    serverColor: "#f2994a",
    color: TRIAGE_STATUS.color,
    render: (size) => <WorkflowStatusGlyph size={size} state={TRIAGE_STATUS} />,
  },
  "autofix-bugs": { serverIcon: "Bug", serverColor: "#eb5757", color: "#26b5ce", render: lucide(Bug) },
  "slack-qa": { serverIcon: "Slack", serverColor: "#4a154b", color: "#9b8afb", render: lucide(MessageSquare) },
  "weekly-wrap": { serverIcon: "Calendar", serverColor: "#5e6ad2", color: "#5e6ad2", render: lucide(CalendarDays) },
  "feature-request-report": { serverIcon: "Lightbulb", serverColor: "#26b5ce", color: "#4cb782", render: lucide(FileText) },
  "security-alerts": { serverIcon: "Shield", serverColor: "#eb5757", color: "#eb5757", render: lucide(ShieldAlert) },
};

/** `template` marks the template card itself, which always shows the template visual. */
type LoopVisualSource = { templateId?: string; icon?: string; color?: string; template?: boolean };

/** The template visual applies while the loop keeps the template's icon (a picked icon wins). */
function templateVisualOf(source: LoopVisualSource) {
  const visual = source.templateId ? TEMPLATE_VISUALS[source.templateId] : undefined;
  if (!visual) return undefined;
  if (source.template) return visual;
  const icon = source.icon ?? "";
  return !icon || icon === "Automation" || icon === visual.serverIcon ? visual : undefined;
}

/** Color for a loop icon's tinted square (template loops use Linear's template colors). */
export function loopIconColor(source: LoopVisualSource) {
  const visual = templateVisualOf(source);
  if (visual) return !source.template && source.color && source.color.toLowerCase() !== visual.serverColor ? source.color : visual.color;
  return source.color || undefined;
}

/** Loop / template icon: template loops show Linear's template glyph, others their picked icon. */
export function LoopIcon({ source, size = 14 }: { source: LoopVisualSource; size?: number }) {
  const visual = templateVisualOf(source);
  if (visual) return <>{visual.render(size, loopIconColor(source) ?? visual.color)}</>;
  return <LoopGlyph icon={source.icon || "Automation"} size={size} />;
}
