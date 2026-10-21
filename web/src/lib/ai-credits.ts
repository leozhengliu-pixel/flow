import { useEffect, useState } from "react";

import { fetchAICredits, type AICredits } from "@/lib/ai-credits-api";

export type { AICredits };

export function formatCredits(micros: number): string {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2 }).format((micros || 0) / 1_000_000);
}

/** The balance, or undefined while it loads or when it cannot be read (never a made-up number). */
export function useAICredits(): AICredits | undefined {
  const [credits, setCredits] = useState<AICredits>();
  useEffect(() => {
    let active = true;
    fetchAICredits()
      .then((next) => active && setCredits(next))
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  return credits;
}
