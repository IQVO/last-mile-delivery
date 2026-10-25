/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

export const WAVE_STATES = ["OPEN", "INDUCTION_CLOSED", "DISPATCHED", "COMPLETED", "CANCELLED"] as const;
export type WaveState = (typeof WAVE_STATES)[number];

export const ROUTE_STATES = ["PLANNED", "OUT_FOR_DELIVERY", "COMPLETED"] as const;
export type RouteState = (typeof ROUTE_STATES)[number];

export const OUTCOMES = ["DELIVERED", "FAILED"] as const;
export type Outcome = (typeof OUTCOMES)[number];

/** A delivery wave as returned by GET /waves and every wave write. */
export interface DeliveryWave {
  waveId: string;
  stationNodeId: string;
  serviceDate: string;
  inductCutoffAt: string;
  windowStart: string;
  windowEnd: string;
  stopCapacity: number;
  assignedStops: number;
  remainingStops: number;
  state: WaveState;
  /** Present only on a CANCELLED wave. */
  cancelReason?: string;
}

/** One stop of a wave: the packages sharing a stopKey (GET /waves/{waveId}/stops). */
export interface WaveStop {
  stopKey: string;
  /** Present when the label data carried it. */
  destinationPostalPrefix?: string;
  trackingNumbers: string[];
}

/** One stop of a route with its derived outcome (empty until decided). */
export interface Stop {
  stopNo: number;
  trackingNumbers: string[];
  outcome?: Outcome;
}

/** One recorded delivery attempt of a route. */
export interface Attempt {
  routeId: string;
  trackingNumber: string;
  outcome: Outcome;
  failureReason?: string;
  at: string;
}

/** A route with its stops and attempts, as returned by GET /routes/{routeId}. */
export interface Route {
  routeId: string;
  waveId: string;
  dspId: string;
  stationNodeId: string;
  state: RouteState;
  stopCount: number;
  stops: Stop[];
  attempts: Attempt[];
}
