Feature: Delivery waves and induction
  A delivery station publishes waves; an arrived package joins the earliest wave
  whose induct cutoff it arrived before and that has stop capacity left.

  Background:
    Given a delivery station "node-ds-01"

  Scenario: A wave must run cutoff, then window start, then window end
    When the planner schedules a wave whose cutoff is after its window start
    Then the request is refused with "invalid wave times"
    And no wave exists

  Scenario: A package joins the earliest wave that still has stop capacity
    Given a wave "wave-1" with its cutoff in 3 hours and capacity 5
    And a wave "wave-2" with its cutoff in 2 hours and capacity 1
    When the packages "TRK-1" and "TRK-2" arrive at the station
    Then the package "TRK-1" is inducted in wave "wave-2"
    And the package "TRK-2" is inducted in wave "wave-1"
    And the event "DeliveryWaveCapacityChanged" was published for wave "wave-2"

  Scenario: Packages of one stop take one stop of capacity
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 1
    And the label of "TRK-1" says stop "k-a"
    And the label of "TRK-2" says stop "k-a"
    When the packages "TRK-1" and "TRK-2" arrive at the station
    Then wave "wave-1" has 1 assigned stop and 0 remaining
    And both packages "TRK-1" and "TRK-2" are inducted in wave "wave-1"

  Scenario: No package joins after induction closes
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And induction of wave "wave-1" is closed
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" waits at the station
    And the event "PackageInductionDeferred" was published for package "TRK-1"

  Scenario: A waiting package is inducted when a new wave is scheduled
    Given the package "TRK-1" arrived and waits at the station
    When the planner schedules a wave "wave-1" with its cutoff in 2 hours and capacity 5
    Then the package "TRK-1" is inducted in wave "wave-1"

  Scenario: Cancelling a wave moves its packages onto the next wave
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 30 hours and capacity 5
    And the package "TRK-1" arrived and is inducted in wave "wave-1"
    When the planner cancels wave "wave-1" because "station closed"
    Then the package "TRK-1" is inducted in wave "wave-2"
    And the event "DeliveryWaveCancelled" was published for wave "wave-1"

  Scenario: Scheduling a wave announces it
    When the planner schedules a wave "wave-1" with its cutoff in 2 hours and capacity 5
    Then the event "DeliveryWaveScheduled" was published for wave "wave-1"
    And the "DeliveryWaveScheduled" event for wave "wave-1" has "stationNodeId" "node-ds-01"

  Scenario: A redelivered LoadArrived event inducts nothing twice
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    When the load event "ev-1" with the package "TRK-1" is delivered twice
    Then wave "wave-1" holds 1 package
