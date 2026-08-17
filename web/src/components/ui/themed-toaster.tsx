import { useEffect, useState } from "react";
import { Toaster } from "sonner";
import {
  getResolvedTheme,
  THEME_CHANGE_EVENT,
  type ResolvedTheme,
} from "@/lib/theme";

export function ThemedToaster() {
  const [theme, setTheme] = useState<ResolvedTheme>(getResolvedTheme);
  useEffect(() => {
    const update = (event: Event) => setTheme((event as CustomEvent<ResolvedTheme>).detail);
    window.addEventListener(THEME_CHANGE_EVENT, update);
    return () => window.removeEventListener(THEME_CHANGE_EVENT, update);
  }, []);
  return <Toaster theme={theme} position="bottom-right" closeButton richColors />;
}
