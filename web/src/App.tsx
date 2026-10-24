import { Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { RouteLookupScreen, RouteScreen } from "./screens/RouteScreen";
import { WaveStopsRoute } from "./screens/WaveStopsScreen";
import { WavesScreen } from "./screens/WavesScreen";

const SUB_NAV = [
  { to: "waves", label: "Waves" },
  { to: "routes", label: "Routes" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/last-mile-delivery/*">`. A relative `<NavLink>` rendered straight in
 *  that splat route resolves against the FULL current URL (react-router 7 uses
 *  the last path-contributing match's pathname, splat included), so from
 *  /last-mile-delivery/routes the link `waves` would point at
 *  /last-mile-delivery/routes/waves. A pathless layout route does not help
 *  (react-router drops pathless routes from relative resolution); a layout
 *  route WITH path "/" does: its own match is the mount point, under any
 *  prefix and standalone at "/". (Same fix as the other fleet remotes;
 *  App.test.tsx mounts under the host's splat route to prove it.) */
function DeliveryLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Last-mile delivery sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as last_mile_mfe/App via Module Federation. Takes NO props: the
 *  console mounts it under `/last-mile-delivery/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix (in
 *  the shell) or at / (standalone dev, see main.tsx). It calls ONLY this
 *  context's own API (/api/last-mile-delivery) and is READ-ONLY: the three
 *  GETs of the contract are all there is.
 *
 *   - (index): redirects to waves.
 *   - waves: the waves of a station for a service date, by induct cutoff.
 *   - waves/:waveId/stops: the packages of the wave grouped into stops.
 *   - routes: a lookup; routes/:routeId: the route with stops and attempts. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<DeliveryLayout />}>
        <Route index element={<Navigate to="waves" replace />} />
        <Route path="waves" element={<WavesScreen />} />
        <Route path="waves/:waveId/stops" element={<WaveStopsRoute />} />
        <Route path="routes" element={<RouteLookupScreen />} />
        <Route path="routes/:routeId" element={<RouteScreen />} />
      </Route>
    </Routes>
  );
}
