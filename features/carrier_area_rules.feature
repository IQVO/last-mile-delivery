Feature: Carrier-area exclusion rules taken from ADR 0015
  A package whose destination area node-topology declared CARRIER_HANDOFF never
  enters a station wave: the final leg is carried by a contracted carrier. Refusal
  needs a known CARRIER_HANDOFF declaration for the package's postal prefix;
  every other package is inducted as usual.

  Background:
    Given a delivery station "node-ds-01"

  Scenario: A package bound for a carrier-handoff area never enters a wave
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 1
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30100"
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is unknown to the station
    And wave "wave-1" holds 0 packages
    And wave "wave-1" has 0 assigned stops and 5 remaining
    And exactly 0 "DeliveryWaveCapacityChanged" events were published
    And exactly 0 "PackageInductionDeferred" events were published

  Scenario: A package bound for a network-route area is inducted as usual
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30200" to the station in the delivery mode "NETWORK_ROUTE" as version 1
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30200"
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is inducted in wave "wave-1"

  Scenario: An area assigned without a delivery mode is a network-route area
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30200" to the station without a delivery mode as version 1
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30200"
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is inducted in wave "wave-1"

  Scenario: A package whose prefix has no area row is inducted
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30300"
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is inducted in wave "wave-1"

  Scenario: A package with no label data is inducted
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 1
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is inducted in wave "wave-1"

  Scenario: Reassigning the area to the network route restores induction on the next arrival
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 1
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30100"
    And the package "TRK-1" arrives at the station
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "NETWORK_ROUTE" as version 2
    When the package "TRK-1" arrives again in a later load
    Then the package "TRK-1" is inducted in wave "wave-1"

  Scenario: An older or equal assignment version does not undo the carrier-handoff declaration
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 2
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "NETWORK_ROUTE" as version 1
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "NETWORK_ROUTE" as version 2
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30100"
    When the package "TRK-1" arrives at the station
    Then the package "TRK-1" is unknown to the station
    And wave "wave-1" holds 0 packages

  Scenario: A package already inducted when the area flips stays on its wave
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30100"
    And the package "TRK-1" arrives at the station
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 1
    When the package "TRK-1" arrives again in a later load
    Then the package "TRK-1" is inducted in wave "wave-1"
    And the package "TRK-1" is "INDUCTED"

  Scenario: A package waiting when the area flips is not inducted by the next wave
    Given the label of "TRK-1" says stop "k-a" for the postal prefix "30100"
    And the package "TRK-1" arrives at the station
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 1
    When the planner schedules a wave "wave-1" with its cutoff in 2 hours and capacity 5
    Then the package "TRK-1" waits at the station
    And wave "wave-1" holds 0 packages

  Scenario: Only the carrier-handoff package of a mixed load is refused
    Given a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And node-topology assigns the postal prefix "30100" to the station in the delivery mode "CARRIER_HANDOFF" as version 1
    And node-topology assigns the postal prefix "30200" to the station in the delivery mode "NETWORK_ROUTE" as version 1
    And the label of "TRK-1" says stop "k-a" for the postal prefix "30100"
    And the label of "TRK-2" says stop "k-b" for the postal prefix "30200"
    When the packages "TRK-1" and "TRK-2" arrive at the station
    Then the package "TRK-1" is unknown to the station
    And the package "TRK-2" is inducted in wave "wave-1"
    And wave "wave-1" holds 1 package
