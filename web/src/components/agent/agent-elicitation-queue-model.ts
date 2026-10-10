export function summarizeElicitationQueue(
  parts: Array<{ type?: string; status?: string; elicitation?: { action?: string } | null }>,
): { answeredCount: number; elicitationCount: number } {
  const elicitations = parts.filter((part) => part.type === "elicitation");
  const answeredCount = elicitations.filter(
    (part) => Boolean(part.elicitation?.action) || part.status === "completed",
  ).length;
  return { answeredCount, elicitationCount: elicitations.length };
}
