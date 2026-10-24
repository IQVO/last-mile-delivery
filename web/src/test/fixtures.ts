import type { DeliveryWave, Route, WaveStop } from "../types";

export function wave(over: Partial<DeliveryWave> = {}): DeliveryWave {
  return {
    waveId: "wave-1",
    stationNodeId: "node-ds-01",
    serviceDate: "2026-10-24",
    inductCutoffAt: "2026-10-24T08:00:00Z",
    windowStart: "2026-10-24T12:00:00Z",
    windowEnd: "2026-10-24T20:00:00Z",
    stopCapacity: 5,
    assignedStops: 2,
    remainingStops: 3,
    state: "OPEN",
    ...over,
  };
}

export function cancelledWave(): DeliveryWave {
  return wave({ waveId: "wave-2", state: "CANCELLED", cancelReason: "station closed", assignedStops: 0, remainingStops: 5 });
}

export function waveStop(over: Partial<WaveStop> = {}): WaveStop {
  return {
    stopKey: "stop-se-91",
    destinationPostalPrefix: "12345",
    trackingNumbers: ["TRK-91"],
    ...over,
  };
}

export const WAVE_STOPS: WaveStop[] = [
  waveStop(),
  waveStop({ stopKey: "stop-se-92", destinationPostalPrefix: "67890", trackingNumbers: ["TRK-92", "TRK-93"] }),
];

export function route(over: Partial<Route> = {}): Route {
  return {
    routeId: "route-1",
    waveId: "wave-1",
    dspId: "dsp-1",
    stationNodeId: "node-ds-01",
    state: "OUT_FOR_DELIVERY",
    stopCount: 2,
    stops: [
      { stopNo: 1, trackingNumbers: ["TRK-91"], outcome: "DELIVERED" },
      { stopNo: 2, trackingNumbers: ["TRK-92"] },
    ],
    attempts: [{ routeId: "route-1", trackingNumber: "TRK-91", outcome: "DELIVERED", at: "2026-10-24T13:00:00Z" }],
    ...over,
  };
}
