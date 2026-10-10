import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";

import type { Team } from "@/types/flow";
import { ActiveTeamContext, useActiveTeam } from "./active-team-context";

/** LS-0545 — global active team store (Linear SetActiveTeam parity). */
export function ActiveTeamProvider({ children }: { children: ReactNode }) {
  const [activeTeam, setActiveTeamState] = useState<Team | undefined>();
  const setActiveTeam = useCallback((team: Team | undefined) => {
    setActiveTeamState((current) => {
      if (current?.id === team?.id && current?.key === team?.key) return current;
      return team;
    });
  }, []);
  const value = useMemo(
    () => ({ activeTeam, setActiveTeam }),
    [activeTeam, setActiveTeam],
  );
  return (
    <ActiveTeamContext.Provider value={value}>
      {children}
    </ActiveTeamContext.Provider>
  );
}

/**
 * Mount under `/:org/team/:teamKey/*`. Writes the resolved team into the store
 * while the route is active, and clears on leave / team change (URL sync).
 */
export function SetActiveTeam({ team }: { team: Team | undefined }) {
  const { setActiveTeam } = useActiveTeam();
  const pathname =
    typeof window !== "undefined" ? window.location.pathname : "";
  useEffect(() => {
    setActiveTeam(team);
    return () => setActiveTeam(undefined);
  }, [team?.id, team?.key, pathname, setActiveTeam, team]);
  return null;
}
