import type { ComponentType, ReactNode } from "react";
import { Bug, CalendarDays, FileText, MessageSquare, ShieldAlert, type LucideProps } from "lucide-react";
import { WorkflowStatusGlyph } from "@/components/issue/issue-icons";
import { TRIAGE_STATUS } from "@/components/triage/triage-model";

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
export type LoopVisualSource = { templateId?: string; icon?: string; color?: string; template?: boolean };

/** The template visual applies while the loop keeps the template's icon (a picked icon wins). */
export function templateVisualOf(source: LoopVisualSource) {
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
