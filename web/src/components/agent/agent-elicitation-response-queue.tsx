import "./agent-elicitation-response-queue.css";

export type AgentElicitationResponseQueueProps = {
  answeredCount: number;
  elicitationCount: number;
  isSubmitting?: boolean;
};

/** LS-0029 — multi-elicitation progress chrome ("N of M answered"). */
export function AgentElicitationResponseQueue({
  answeredCount,
  elicitationCount,
  isSubmitting = false,
}: AgentElicitationResponseQueueProps) {
  if (elicitationCount <= 1 && !isSubmitting) return null;
  return (
    <div
      className="agent-elicitation-response-queue"
      aria-live="polite"
      data-testid="agent-elicitation-response-queue"
    >
      <span className="agent-elicitation-response-queue__label">
        {isSubmitting
          ? "Submitting answers…"
          : `${answeredCount} of ${elicitationCount} answered`}
      </span>
    </div>
  );
}

export function summarizeElicitationQueue(
  parts: Array<{ type?: string; status?: string; elicitation?: { action?: string } | null }>,
): { answeredCount: number; elicitationCount: number } {
  const elicitations = parts.filter((part) => part.type === "elicitation");
  const answeredCount = elicitations.filter(
    (part) => Boolean(part.elicitation?.action) || part.status === "completed",
  ).length;
  return { answeredCount, elicitationCount: elicitations.length };
}
