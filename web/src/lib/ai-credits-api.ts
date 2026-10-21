import { request } from "@/lib/api-client";

/** The workspace's AI credit balance (GET /api/workspace/ai-credits), in millionths of a dollar. */
export interface AICredits {
  includedMicros: number;
  spentMicros: number;
  remainingMicros: number;
}

export const fetchAICredits = () => request<AICredits>("/api/workspace/ai-credits");
