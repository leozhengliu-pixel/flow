import { useEffect, useState } from "react";
import { getResolvedTheme, THEME_CHANGE_EVENT, type ResolvedTheme } from "@/lib/theme";

/** The resolved light / dark theme, updated when the interface theme changes. */
export function useResolvedTheme(): ResolvedTheme {
  const [theme, setTheme] = useState<ResolvedTheme>(getResolvedTheme);
  useEffect(() => {
    const update = () => setTheme(getResolvedTheme());
    window.addEventListener(THEME_CHANGE_EVENT, update);
    return () => window.removeEventListener(THEME_CHANGE_EVENT, update);
  }, []);
  return theme;
}
