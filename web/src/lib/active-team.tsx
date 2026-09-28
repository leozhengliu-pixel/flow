import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

import type { Team } from "@/types/flow";

type ActiveTeamContextValue = {
  activeTeam: Team | undefined;
  setActiveTeam: (team: Team | undefined) => void;
};

const ActiveTeamContext = createContext<ActiveTeamContextValue | null>(null);

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
