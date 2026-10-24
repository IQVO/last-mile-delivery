import { Link, useParams } from "react-router-dom";
import { getWaveStops } from "../api";
import { PageHeader, Stack, WaveStopsTable } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";

/** The route element of `waves/:waveId/stops`. */
export function WaveStopsRoute() {
  const { waveId } = useParams();
  return waveId ? <WaveStopsScreen waveId={waveId} /> : null;
}

/**
 * The stops of one wave: GET /waves/{waveId}/stops -- the packages inducted
 * in the wave grouped by the opaque stopKey (the planner builds a route from
 * exactly this list), in first-induction order. No address is ever returned,
 * only the postal prefix when the label data had it.
 */
export function WaveStopsScreen({ waveId }: { waveId: string }) {
  const stops = useRequest(() => getWaveStops(waveId), `wave-stops|${waveId}`);
  return (
    <Stack gap={5}>
      <PageHeader title={`Stops of ${waveId}`} subtitle="The packages this wave holds, grouped into stops by stopKey" />
      <RequestView state={stops} what="the wave stops" isEmpty={(p) => p.items.length === 0} empty="No package is inducted in this wave yet.">
        {(p) => <WaveStopsTable stops={p.items} />}
      </RequestView>
      <Link to="../../waves">← Back to the waves</Link>
    </Stack>
  );
}
