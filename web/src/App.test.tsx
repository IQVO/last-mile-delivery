import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { route, wave, WAVE_STOPS } from "./test/fixtures";

// The console mounts the remote under its own splat route; the remote's
// relative links must resolve against the mount point, not the full URL.
function renderUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/last-mile-delivery/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ROUTES = {
  "GET /waves": json({ items: [wave()] }),
  "GET /waves/wave-1/stops": json({ items: WAVE_STOPS }),
  "GET /routes/route-1": json(route()),
};

describe("App", () => {
  it("redirects the mount point to the wave list", async () => {
    mockApi(ROUTES);
    renderUnderHost("/last-mile-delivery");
    expect(await screen.findByRole("heading", { name: "Delivery waves" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Waves" })).toHaveAttribute("href", "/last-mile-delivery/waves");
  });

  it("keeps the sub-nav at the mount point from a deep route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/last-mile-delivery/routes/route-1");
    await screen.findByRole("heading", { name: "Route route-1" });
    expect(screen.getByRole("link", { name: "Waves" })).toHaveAttribute("href", "/last-mile-delivery/waves");
    expect(screen.getByRole("link", { name: "Routes" })).toHaveAttribute("href", "/last-mile-delivery/routes");
  });

  it("navigates from a wave into its stops and on to a route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/last-mile-delivery/waves");
    const user = userEvent.setup();
    // search for the station's waves first
    await user.type(await screen.findByLabelText(/Service date/), "2026-10-24");
    await user.click(screen.getByRole("button", { name: "Show waves" }));
    expect(await screen.findByRole("link", { name: "wave-1" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "wave-1" }));
    expect(await screen.findByRole("heading", { name: "Stops of wave-1" })).toBeInTheDocument();
    expect(await screen.findByText("stop-se-91")).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Routes" }));
    expect(await screen.findByRole("heading", { name: "Routes" })).toBeInTheDocument();
    await user.type(screen.getByLabelText("Route"), "route-1");
    await user.click(screen.getByRole("button", { name: "View route" }));
    expect(await screen.findByRole("heading", { name: "Route route-1" })).toBeInTheDocument();
  });
});
