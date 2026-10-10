import { createContext, useContext } from "react";

import type { Team } from "@/types/flow";

export type ActiveTeamContextValue = {
  activeTeam: Team | undefined;
  setActiveTeam: (team: Team | undefined) => void;
};

export const ActiveTeamContext = createContext<ActiveTeamContextValue | null>(null);

export function useActiveTeam() {
  const context = useContext(ActiveTeamContext);
  if (!context) {
    throw new Error("useActiveTeam must be used within ActiveTeamProvider");
  }
  return context;
}

export function useOptionalActiveTeam() {
  return useContext(ActiveTeamContext);
}
