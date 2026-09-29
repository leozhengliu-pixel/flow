import type { ReactNode } from "react";
import { AgentAnswerText } from "@/components/agent/agent-answer";
import { parseAgentAnswer } from "@/components/agent/agent-answer-content";
import { AgentElicitation } from "@/components/agent/agent-elicitation";
import { AgentWorkGroup } from "@/components/agent/agent-work-group";
import { TeamIcon } from "@/components/issue/issue-icons";
import { useI18n } from "@/i18n/i18n";
import type { AgentMessage, AgentMessagePart, AgentToolCall, BootstrapData, Team } from "@/types/flow";
import { isPublishCall, loopToolResult, splitBuilderReply, type LoopToolResult } from "./loop-data";
import { LoopIcon, loopIconColor } from "./loop-glyph";

export type LoopVisual = { templateId?: string; icon?: string; color?: string };

const toolName = (call: AgentToolCall | undefined) => call?.name.replace(/^mcp__flow\./, "") ?? "";
const isSaveLoop = (part: AgentMessagePart) => part.type === "toolCall" && toolName(part.toolCall) === "save_loop";
const isQuestion = (part: AgentMessagePart) => part.type === "elicitation" && Boolean(part.elicitation);
const isRunning = (status?: string) => status === "running" || status === "pending";

/**
 * Linear's loop-builder reply, in order: intro, each question (chips → quoted answer) with the
 * "Updated workflow definition draft" card it led to, "Worked for N seconds ▸", the final reply and "Created automation".
 */
export function LoopBuilderMessage({
  message,
  streaming,
  entityData,
  teams,
  visual,
  approvalBusy,
  onToolApproval,
  onOpenLoop,
}: {
  message: AgentMessage;
  streaming: boolean;
  entityData?: BootstrapData;
  teams: Team[];
  /** The loop's own icon (template loops use the template's), since `save_loop` results carry none. */
  visual?: LoopVisual;
  approvalBusy?: string;
  onToolApproval: (call: AgentToolCall | undefined, decision: "approve" | "reject") => void;
  onOpenLoop?: (result: LoopToolResult) => void;
}) {
  const parts = message.parts ?? [];
  const hasQuestions = parts.some(isQuestion);
  const { intro, reply } = splitBuilderReply(message.content, hasQuestions);
  const introAnswer = intro ? parseAgentAnswer(intro, entityData) : undefined;
  const replyAnswer = reply ? parseAgentAnswer(reply, entityData) : undefined;
  const work = parts.filter((part) => !isQuestion(part) && !isSaveLoop(part) && (part.type === "reasoning" || part.type === "step" || part.type === "toolCall"));
  // While a question waits for its answer the builder is not working; the chips are the call to action.
  const awaitingAnswer = parts.some((part) => isQuestion(part) && !part.elicitation?.action && part.status !== "completed" && part.status !== "error");
  const running = !awaitingAnswer && (streaming || parts.some((part) => !isQuestion(part) && (isRunning(part.status) || isRunning(part.toolCall?.status))));
  const timeline: ReactNode[] = [];
  const published: ReactNode[] = [];
  for (const part of parts) {
    if (isQuestion(part)) timeline.push(<AgentElicitation key={part.id} part={part} variant="inline" />);
    else if (isSaveLoop(part)) {
      const card = <LoopToolCard key={part.id} call={part.toolCall} teams={teams} visual={visual} onOpen={onOpenLoop} />;
      if (isPublishCall(part.toolCall)) published.push(card);
      else timeline.push(card);
    }
  }
  const showWork = work.length > 0 || Boolean(message.durationMs) || running;
  return (
    <>
      {introAnswer?.markdown && <AgentAnswerText ariaLabel="AI message" className="loops-builder-text" data={entityData} markdown={introAnswer.markdown} />}
      {timeline}
      {showWork && (
        <AgentWorkGroup
          approvalBusy={approvalBusy}
          className="loops-builder-work"
          collapseWhenDone
          message={message}
          onToolApproval={onToolApproval}
          parts={work}
          running={running}
        />
      )}
      {replyAnswer?.markdown && <AgentAnswerText ariaLabel="AI message" className="loops-builder-text" data={entityData} markdown={replyAnswer.markdown} />}
      {published.length > 0 && <div className="loops-agent-cards">{published}</div>}
    </>
  );
}

/** "Updated workflow definition draft" + loop row, or "Created automation" + loop card with runs and team. */
export function LoopToolCard({
  call,
  teams = [],
  visual,
  onOpen,
}: {
  call: AgentToolCall | undefined;
  teams?: Team[];
  visual?: LoopVisual;
  onOpen?: (result: LoopToolResult) => void;
}) {
  const { t } = useI18n();
  const result = loopToolResult(call);
  const publish = isPublishCall(call);
  const pending = isRunning(call?.status);
  const failed = call?.status === "error";
  const heading = pending
    ? publish
      ? "Creating automation…"
      : "Updating workflow definition draft…"
    : failed
      ? "Could not update the loop"
      : (call?.title ?? (publish ? "Created automation" : "Updated workflow definition draft"));
  const source = { templateId: visual?.templateId, icon: result?.icon ?? visual?.icon, color: result?.color ?? visual?.color };
  const team = result?.teamId ? teams.find((item) => item.id === result.teamId) : undefined;
  const teamName = team?.name ?? result?.teamName;
  const runs = result?.runCount30d ?? 0;
  const body = result && (
    <>
      <span className="loops-agent-card-icon" style={{ color: loopIconColor(source) }}>
        <LoopIcon source={source} size={14} />
      </span>
      <span className="loops-agent-card-copy">
        <strong data-i18n-ignore>{result.name || t("Untitled loop")}</strong>
        {publish && (
          <small>
            {t("Ran")} {runs} {t(runs === 1 ? "time (30d)" : "times (30d)")}
          </small>
        )}
      </span>
      {publish && teamName && (
        <span className="loops-agent-card-team">
          {team && <TeamIcon team={team} size={14} />}
          <span data-i18n-ignore>{teamName}</span>
        </span>
      )}
    </>
  );
  const open = onOpen && result && publish ? () => onOpen(result) : undefined;
  return (
    <div className={`loops-agent-card${publish ? " is-published" : " is-draft"}`}>
      <span className={`loops-agent-card-title${pending ? " is-running" : ""}${failed ? " is-error" : ""}`} title={call?.error || undefined}>
        {t(heading)}
      </span>
      {body &&
        (open ? (
          <button className="loops-agent-card-body" type="button" onClick={open}>
            {body}
          </button>
        ) : (
          <div className="loops-agent-card-body">{body}</div>
        ))}
    </div>
  );
}
