import type { AgentMessage } from "@/types/flow";

/** Time headers between agent messages: shown first and after 12h gaps. */
export function shouldShowAgentTime(messages: AgentMessage[], index: number) {
  if (index === 0) return true;
  const current = Date.parse(messages[index].createdAt);
  const previous = Date.parse(messages[index - 1].createdAt);
  return !Number.isFinite(current) || !Number.isFinite(previous) || current - previous >= 12 * 60 * 60 * 1000;
}

export function formatAgentTime(value: string, todayLabel: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const now = new Date();
  const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
  const time = date.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
  if (sameDay) return `${todayLabel} ${time}`;
  return `${date.toLocaleDateString(undefined, { month: "short", day: "numeric" })} ${time}`;
}
