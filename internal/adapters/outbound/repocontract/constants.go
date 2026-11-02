package repocontract

// Fixture vocabulary shared by the contract cases: tracking numbers, stop
// keys, station node ids and wave ids, plus the shared case description.
// Centralised so the literals stay single-sourced and lint-clean.
const (
	trk1 = "TRK-1"
	trk2 = "TRK-2"
	trk3 = "TRK-3"
	trk4 = "TRK-4"

	stopKeyA = "k-a"
	stopKeyB = "k-b"
	stopKeyC = "k-c"

	station1 = "node-ds-01"
	station2 = "node-ds-02"

	wave1 = "wave-1"
	waveA = "wave-a"
	waveB = "wave-b"
	waveC = "wave-c"
	waveD = "wave-d"
	waveE = "wave-e"

	unknownIDCase = "get of an unknown id is ErrNotFound"
)
