import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, DataTable } from "@warehouse/ui-kit";
import { listWaves } from "../api";
import { Form, FormRow, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, Stack, WaveStatePill } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { idProblem, serviceDateProblem, stopMeter, utcLabel } from "../lib";
import type { DeliveryWave } from "../types";

/**
 * The waves of one delivery station for one service date: GET
 * /waves?stationNodeId=&serviceDate=, ordered by induct cutoff. A station and
 * date with no wave is an empty list; from each wave the operator can open
 * its stops (what packages it holds, grouped by stopKey).
 */
export function WavesScreen() {
  const [stationNodeId, setStationNodeId] = useState("node-ds-01");
  const [serviceDate, setServiceDate] = useState("");
  const [applied, setApplied] = useState<{ station: string; date: string } | null>(null);

  const station = stationNodeId.trim();
  const stationProblem = idProblem("station", station);
  const dateProblem = serviceDateProblem(serviceDate);
  const search = () => {
    if (stationProblem || dateProblem) return;
    setApplied({ station, date: serviceDate });
  };

  const waves = useRequest(
    applied ? () => listWaves(applied.station, applied.date) : null,
    `waves|${applied?.station}|${applied?.date}`,
  );

  return (
    <Stack gap={5}>
      <PageHeader
        title="Delivery waves"
        subtitle="last-mile-delivery · each wave of a station and service date, its capacity and where it stands"
      />
      <Card title="Find waves">
        <Form label="Wave search" onSubmit={search}>
          <FormRow>
            <TextField
              label="Station"
              required
              value={stationNodeId}
              onChange={setStationNodeId}
              placeholder="node-ds-01"
              hint={stationProblem ?? "The delivery station (node-topology nodeId)"}
            />
            <TextField
              label="Service date"
              required
              type="date"
              value={serviceDate}
              onChange={setServiceDate}
              hint={dateProblem ?? "The station's local service date"}
            />
            <SubmitButton disabled={stationProblem !== null || dateProblem !== null}>Show waves</SubmitButton>
          </FormRow>
        </Form>
      </Card>
      {applied && (
        <Card title={`Waves of ${applied.station} on ${applied.date}`}>
          <RequestView state={waves} what="waves" isEmpty={(p) => p.items.length === 0} empty="No wave is scheduled for this station and date.">
            {(p) => <WavesTable waves={p.items} />}
          </RequestView>
        </Card>
      )}
    </Stack>
  );
}

function WavesTable({ waves }: { waves: DeliveryWave[] }) {
  return (
    <DataTable
      rowKey={(w) => w.waveId}
      rows={waves}
      columns={[
        {
          key: "wave",
          header: "Wave",
          // Relative link: `..` is the parent route (this remote's mount
          // point), so it works under the console's /last-mile-delivery/* and
          // standalone at /.
          render: (w) => (
            <Link to={`../waves/${encodeURIComponent(w.waveId)}/stops`}>{w.waveId}</Link>
          ),
        },
        { key: "state", header: "State", render: (w) => <WaveStatePill state={w.state} /> },
        { key: "cutoff", header: "Induct cutoff", render: (w) => utcLabel(w.inductCutoffAt) },
        { key: "window", header: "Window", render: (w) => `${utcLabel(w.windowStart)} → ${utcLabel(w.windowEnd)}` },
        { key: "stops", header: "Stops", render: (w) => stopMeter(w) },
        { key: "cancel", header: "Cancel reason", render: (w) => w.cancelReason ?? "—" },
      ]}
    />
  );
}
