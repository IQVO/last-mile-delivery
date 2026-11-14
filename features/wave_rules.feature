Feature: Delivery wave rules taken from the documentation
  Scenarios derived from the business context, the use cases, the aggregate
  design canvas (DeliveryWave), the ubiquitous language and ADR 0002: the times
  of a wave, its stop capacity at the boundary, the induct cutoff, the policy
  InductOnArrival and its deferrals, CancelWave and the wave state machine.

  Background:
    Given a delivery station "node-ds-01"

  # --- ScheduleWave: invariant inductCutoffAt < windowStart < windowEnd ---

  Scenario Outline: A wave whose times are not strictly ordered is refused
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"<cutoff>","windowStart":"<start>","windowEnd":"<end>","stopCapacity":5}
      """
    Then the response is 422
    And the response problem is "invalid-wave-times"
    And no wave exists
    And exactly 0 "DeliveryWaveScheduled" events were published

    Examples:
      | case                      | cutoff               | start                | end                  |
      | cutoff equals window start | 2026-10-24T12:00:00Z | 2026-10-24T12:00:00Z | 2026-10-24T20:00:00Z |
      | window start equals end   | 2026-10-24T09:00:00Z | 2026-10-24T20:00:00Z | 2026-10-24T20:00:00Z |
      | cutoff after window start | 2026-10-24T13:00:00Z | 2026-10-24T12:00:00Z | 2026-10-24T20:00:00Z |
      | window start after end    | 2026-10-24T09:00:00Z | 2026-10-24T21:00:00Z | 2026-10-24T20:00:00Z |

  Scenario: Times one minute apart are enough
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T09:01:00Z","windowEnd":"2026-10-24T09:02:00Z","stopCapacity":5}
      """
    Then the response is 201
    And the response field "state" is "OPEN"
    And exactly 1 "DeliveryWaveScheduled" event was published

  # --- ScheduleWave: stop capacity 0..2147483647 (A3) ---

  Scenario Outline: A stop capacity inside 0 to 2147483647 is accepted
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":<capacity>}
      """
    Then the response is 201
    And the response field "assignedStops" is "0"

    Examples:
      | capacity   |
      | 0          |
      | 2147483647 |

  Scenario Outline: A stop capacity outside 0 to 2147483647 is refused
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":<capacity>}
      """
    Then the response is 422
    And the response problem is "validation-failed"
    And no wave exists

    Examples:
      | capacity   |
      | -1         |
      | 2147483648 |

  Scenario: A service date that is not a calendar day is refused
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-01","serviceDate":"2026-02-30","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 422
    And the response problem is "validation-failed"
    And no wave exists

  # --- ScheduleWave: the station must be a known DELIVERY_STATION (A1) ---

  Scenario: A wave for a node nobody registered is refused
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-77","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 422
    And the response problem is "unknown-station"
    And no wave exists

  Scenario: A node of another type is not a station: node-topology's other node types are ignored
    Given node-topology registers the node "node-fc-01" as a "FULFILLMENT_CENTER"
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-fc-01","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 422
    And the response problem is "unknown-station"
    And the station view holds no node "node-fc-01"

  Scenario: A station node-topology registered can host waves
    Given node-topology registers the node "node-ds-02" as a "DELIVERY_STATION"
    When the client posts to "/waves" with a fresh key and body:
      """
      {"stationNodeId":"node-ds-02","serviceDate":"2026-10-24","inductCutoffAt":"2026-10-24T09:00:00Z","windowStart":"2026-10-24T12:00:00Z","windowEnd":"2026-10-24T20:00:00Z","stopCapacity":5}
      """
    Then the response is 201
    And the response field "stationNodeId" is "node-ds-02"

  # --- InductPackage: the induct cutoff ---

  Scenario: A package arriving exactly at the induct cutoff misses the wave
    Given a wave "wave-1" with its cutoff in 0 hours and capacity 5
    When the package "TRK-1" arrives at the station at 06:00:00
    Then the package "TRK-1" waits at the station
    And wave "wave-1" holds 0 packages
    And the arrival of "TRK-1" is recorded in the load "load-ev-TRK-1-06:00:00"
    And the event "PackageInductionDeferred" was published for package "TRK-1"

  Scenario: A package arriving one second before the induct cutoff makes the wave
    Given a wave "wave-1" with its cutoff in 0 hours and capacity 5
    When the package "TRK-1" arrives at the station at 05:59:59
    Then the package "TRK-1" is inducted in wave "wave-1"
    And the package "TRK-1" is "INDUCTED"

  Scenario: A package skips an earlier wave whose induction is closed and takes the next open one
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 3 hours and capacity 5
    And induction of wave "wave-1" is closed
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is inducted in wave "wave-2"
    And wave "wave-1" holds 0 packages
    And exactly 0 "PackageInductionDeferred" events were published

  # --- InductPackage: stop capacity at the boundary (ErrWaveFull, A4) ---

  Scenario: Waves with the same induct cutoff are tried in waveId order
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 1
    And a wave "wave-2" with its cutoff in 2 hours and capacity 5
    When the packages "TRK-1" and "TRK-2" arrive at the station
    Then the package "TRK-1" is inducted in wave "wave-1"
    And the package "TRK-2" is inducted in wave "wave-2"

  Scenario: A wave takes stops up to its capacity and the next stop waits
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 2
    And the packages "TRK-1" and "TRK-2" arrive at the station
    And the latest "DeliveryWaveCapacityChanged" event for wave "wave-1" reports 2 assigned and 0 remaining stops
    When the package "TRK-3" arrives at the station
    Then the package "TRK-3" waits at the station
    And wave "wave-1" holds 2 packages
    And exactly 2 "DeliveryWaveCapacityChanged" events were published
    And the event "PackageInductionDeferred" was published for package "TRK-3"

  Scenario: A wave with no stop capacity takes nothing
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 0
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" waits at the station
    And exactly 0 "DeliveryWaveCapacityChanged" events were published

  Scenario: A package of a stop already in a full wave is still accepted, a new stop is not
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 1
    And the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    And the label of "TRK-3" says stop "k-b"
    And the package "TRK-1" arrives at the station
    When the package "TRK-2" arrives at the station
    And the package "TRK-3" arrives at the station
    Then both packages "TRK-1" and "TRK-2" are inducted in wave "wave-1"
    And the package "TRK-3" waits at the station
    And wave "wave-1" has 1 assigned stop and 0 remaining
    And exactly 1 "DeliveryWaveCapacityChanged" event was published

  Scenario: Two packages of one stop raise one capacity event
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    When the packages "TRK-1" and "TRK-2" arrive at the station
    Then exactly 1 "DeliveryWaveCapacityChanged" event was published
    And the latest "DeliveryWaveCapacityChanged" event for wave "wave-1" reports 1 assigned and 4 remaining stops

  # --- InductOnArrival: idempotency and which loads count ---

  Scenario: A package that arrives again in a later load is not inducted twice
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 3 hours and capacity 5
    And the package "TRK-1" arrives at the station
    When the package "TRK-1" arrives again in a later load
    Then wave "wave-1" holds 1 package
    And wave "wave-2" holds 0 packages
    And exactly 1 "DeliveryWaveCapacityChanged" event was published
    And the package "TRK-1" is "INDUCTED"

  Scenario: A load that reached another kind of node is ignored
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When a load of the node type "SORTATION_CENTER" brings the package "TRK-1" to the node "node-ds-01"
    Then the package "TRK-1" is unknown to the station
    And wave "wave-1" holds 0 packages
    And the arrival of "TRK-1" is not recorded in the load "load-ev-TRK-1"

  Scenario: A load without a node type at a known station is acted on
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When a load without a node type brings the package "TRK-1" to the node "node-ds-01"
    Then the package "TRK-1" is inducted in wave "wave-1"
    And the arrival of "TRK-1" is recorded in the load "load-ev-TRK-1"

  Scenario: A load without a node type at an unknown node is ignored
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When a load without a node type brings the package "TRK-1" to the node "node-xd-09"
    Then the package "TRK-1" is unknown to the station
    And wave "wave-1" holds 0 packages

  Scenario: Waiting packages are inducted oldest arrival first until the new wave is full
    Given the package "TRK-A" arrives at the station at 05:50
    And the package "TRK-B" arrives at the station at 05:30
    And the package "TRK-C" arrives at the station at 05:40
    When the planner schedules a wave "wave-1" with its cutoff in 2 hours and capacity 2
    Then the package "TRK-B" is inducted in wave "wave-1"
    And the package "TRK-C" is inducted in wave "wave-1"
    And the package "TRK-A" waits at the station
    And exactly 3 "PackageInductionDeferred" events were published
    And exactly 2 "DeliveryWaveCapacityChanged" events were published
    And the latest "DeliveryWaveCapacityChanged" event for wave "wave-1" reports 2 assigned and 0 remaining stops

  # --- CancelWave (A10, G17) ---

  Scenario Outline: A cancel reason of 1 to 200 characters is accepted
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client cancels wave "wave-1" with a reason of <length> characters
    Then the response is <status>

    Examples:
      | length | status |
      | 1      | 200    |
      | 200    | 200    |

  Scenario: A cancel reason of 201 characters is refused
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client cancels wave "wave-1" with a reason of 201 characters
    Then the response is 422
    And the response problem is "validation-failed"
    And wave "wave-1" is "OPEN"
    And exactly 0 "DeliveryWaveCancelled" events were published

  Scenario: Cancelling a wave re-inducts what fits and defers the rest; the wave keeps its record
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 30 hours and capacity 1
    And the packages "TRK-1" and "TRK-2" arrive at the station
    When the planner cancels wave "wave-1" because "station closed"
    Then the package "TRK-1" is inducted in wave "wave-2"
    And the package "TRK-2" waits at the station
    And exactly 1 "PackageInductionDeferred" event was published
    And wave "wave-1" is "CANCELLED"
    And wave "wave-1" holds 2 packages

  Scenario: A package left without a wave by a cancellation joins the next wave that is scheduled
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the package "TRK-1" arrives at the station
    And the planner cancels wave "wave-1" because "station closed"
    When the planner schedules a wave "wave-2" with its cutoff in 30 hours and capacity 5
    Then the package "TRK-1" is inducted in wave "wave-2"
    And the package "TRK-1" is "INDUCTED"

  Scenario: A cancelled wave reports its reason and cannot be cancelled again
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client posts to "/waves/wave-1/cancellations" with a fresh key and body:
      """
      {"reason":"station closed"}
      """
    Then the response is 200
    And the response field "state" is "CANCELLED"
    And the response field "cancelReason" is "station closed"
    When the client posts to "/waves/wave-1/cancellations" with a fresh key and body:
      """
      {"reason":"station closed"}
      """
    Then the response is 409
    And the response problem is "illegal-transition"
    And wave "wave-1" is "CANCELLED"
    And exactly 1 "DeliveryWaveCancelled" event was published

  # --- The wave state machine: every other (state, command) pair is refused ---

  Scenario: Induction can be closed only once
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And induction of wave "wave-1" is closed
    When the client posts to "/waves/wave-1/induction-closures" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"
    And wave "wave-1" is "INDUCTION_CLOSED"

  Scenario: A wave whose induction is closed cannot be cancelled
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And induction of wave "wave-1" is closed
    When the client posts to "/waves/wave-1/cancellations" with a fresh key and body:
      """
      {"reason":"too late"}
      """
    Then the response is 409
    And the response problem is "illegal-transition"
    And wave "wave-1" is "INDUCTION_CLOSED"
    And exactly 0 "DeliveryWaveCancelled" events were published

  Scenario: A cancelled wave cannot be closed or dispatched
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the planner cancels wave "wave-1" because "station closed"
    When the client posts to "/waves/wave-1/induction-closures" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"
    When the client posts to "/waves/wave-1/dispatches" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"
    And wave "wave-1" is "CANCELLED"

  Scenario: A dispatched wave refuses another dispatch, a close and a cancel
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the client posts to "/waves/wave-1/dispatches" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"
    When the client posts to "/waves/wave-1/induction-closures" with a fresh key
    Then the response is 409
    And the response problem is "illegal-transition"
    When the client posts to "/waves/wave-1/cancellations" with a fresh key and body:
      """
      {"reason":"too late"}
      """
    Then the response is 409
    And the response problem is "illegal-transition"
    And wave "wave-1" is "DISPATCHED"

  Scenario Outline: A command on a wave that does not exist is not found
    When the client posts to "<path>" with a fresh key and body:
      """
      <body>
      """
    Then the response is 404
    And the response problem is "not-found"

    Examples:
      | path                                 | body                         |
      | /waves/wave-404/induction-closures   | {}                           |
      | /waves/wave-404/dispatches           | {}                           |
      | /waves/wave-404/cancellations        | {"reason":"station closed"}  |
      | /waves/wave-404/cost-declarations    | {"costPerStopCents":425}     |

  # --- DeclareCost (ADR 0011): 0 to 10000000 cents, while the wave still runs ---

  Scenario Outline: A declared stop cost from 0 to 10000000 cents is accepted
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client posts to "/waves/wave-1/cost-declarations" with a fresh key and body:
      """
      {"costPerStopCents":<cents>}
      """
    Then the response is 201
    And wave "wave-1" declares <cents> cents per stop

    Examples:
      | cents    |
      | 0        |
      | 10000000 |

  Scenario Outline: A declared stop cost outside 0 to 10000000 cents or missing is refused
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the client posts to "/waves/wave-1/cost-declarations" with a fresh key and body:
      """
      <body>
      """
    Then the response is 422
    And the response problem is "validation-failed"
    And wave "wave-1" declares 0 cents per stop
    And exactly 0 "WaveCostDeclared" events were published

    Examples:
      | body                           |
      | {"costPerStopCents":10000001}  |
      | {"costPerStopCents":-1}        |
      | {}                             |

  Scenario: A later declaration overwrites the earlier one and each is announced
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the planner declares a stop cost of 425 cents for wave "wave-1"
    When the planner declares a stop cost of 510 cents for wave "wave-1"
    Then wave "wave-1" declares 510 cents per stop
    And exactly 2 "WaveCostDeclared" events were published

  Scenario: A wave whose induction is closed still accepts a cost declaration
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And induction of wave "wave-1" is closed
    When the planner declares a stop cost of 425 cents for wave "wave-1"
    Then the request is accepted
    And wave "wave-1" declares 425 cents per stop

  Scenario: A completed wave refuses a cost declaration
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And induction of wave "wave-1" is closed
    And the planner dispatches wave "wave-1"
    And wave "wave-1" is "COMPLETED"
    When the planner declares a stop cost of 425 cents for wave "wave-1"
    Then the request is refused with "illegal transition"
    And exactly 0 "WaveCostDeclared" events were published
