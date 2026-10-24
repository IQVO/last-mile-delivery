import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { RouteLookupScreen, RouteScreen } from "./RouteScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { route } from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/routes" element={<RouteLookupScreen />} />
        <Route path="/routes/:routeId" element={<RouteScreen />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("RouteLookupScreen", () => {
  it("navigates to the route once an id is typed", async () => {
    mockApi({ "GET /routes/route-1": json(route()) });
    renderAt("/routes");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Route"), "route-1");
    await user.click(screen.getByRole("button", { name: "View route" }));
    expect(await screen.findByRole("heading", { name: "Route route-1" })).toBeInTheDocument();
  });

  it("disables the button for an empty id", () => {
    renderAt("/routes");
    expect(screen.getByRole("button", { name: "View route" })).toBeDisabled();
  });
});

describe("RouteScreen", () => {
  it("shows the route, its stops and its attempts", async () => {
    mockApi({ "GET /routes/route-1": json(route()) });
    renderAt("/routes/route-1");
    expect(await screen.findByRole("heading", { name: "Route route-1" })).toBeInTheDocument();
    expect(screen.getByText("dsp-1")).toBeInTheDocument();
    expect(screen.getAllByText("TRK-91").length).toBe(2); // once in the stops, once in the attempts
    expect(screen.getByText("pending")).toBeInTheDocument();
    expect(screen.getByText("2026-10-24 13:00:00Z")).toBeInTheDocument();
  });

  it("renders the not-found problem of an unknown route", async () => {
    mockApi({ "GET /routes/route-9": problem(404, "not-found", "Resource not found", "route route-9 does not exist") });
    renderAt("/routes/route-9");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("route route-9 does not exist");
  });

  it("keeps a failed attempt's reason visible", async () => {
    mockApi({
      "GET /routes/route-2": json(
        route({
          routeId: "route-2",
          state: "OUT_FOR_DELIVERY",
          stops: [{ stopNo: 1, trackingNumbers: ["TRK-77"], outcome: "FAILED" }],
          attempts: [{ routeId: "route-2", trackingNumber: "TRK-77", outcome: "FAILED", failureReason: "recipient absent", at: "2026-10-24T15:00:00Z" }],
        }),
      ),
    });
    renderAt("/routes/route-2");
    expect(await screen.findByText("recipient absent")).toBeInTheDocument();
    expect(screen.getAllByText("FAILED").length).toBe(2); // the stop outcome and the attempt
  });
});
