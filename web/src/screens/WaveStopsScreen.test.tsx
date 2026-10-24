import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { WaveStopsRoute } from "./WaveStopsScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { WAVE_STOPS } from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/waves/:waveId/stops" element={<WaveStopsRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("WaveStopsScreen", () => {
  it("groups the wave's packages into stops by stopKey", async () => {
    mockApi({ "GET /waves/wave-1/stops": json({ items: WAVE_STOPS }) });
    renderAt("/waves/wave-1/stops");
    expect(await screen.findByRole("heading", { name: "Stops of wave-1" })).toBeInTheDocument();
    expect(screen.getByText("stop-se-91")).toBeInTheDocument();
    expect(screen.getByText("stop-se-92")).toBeInTheDocument();
    expect(screen.getByText("12345")).toBeInTheDocument();
    expect(screen.getByText("TRK-92, TRK-93")).toBeInTheDocument();
  });

  it("marks a package with no label data as its own stop", async () => {
    mockApi({ "GET /waves/wave-1/stops": json({ items: [{ stopKey: "pkg:TRK-9", trackingNumbers: ["TRK-9"] }] }) });
    renderAt("/waves/wave-1/stops");
    expect(await screen.findByText("pkg:TRK-9")).toBeInTheDocument();
  });

  it("shows the empty note for a wave that holds nothing", async () => {
    mockApi({ "GET /waves/wave-1/stops": json({ items: [] }) });
    renderAt("/waves/wave-1/stops");
    expect(await screen.findByText("No package is inducted in this wave yet.")).toBeInTheDocument();
  });

  it("renders the not-found problem of an unknown wave", async () => {
    mockApi({ "GET /waves/wave-9/stops": problem(404, "not-found", "Resource not found", "wave wave-9 does not exist") });
    renderAt("/waves/wave-9/stops");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Resource not found");
    expect(alert).toHaveTextContent("wave wave-9 does not exist");
  });
});
