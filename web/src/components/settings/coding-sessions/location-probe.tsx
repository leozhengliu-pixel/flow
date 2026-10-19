import { useLocation } from "react-router-dom";

/** Test helper: renders the router's current pathname. */
export function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}
