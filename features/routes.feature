Feature: Routes and delivery attempts
  A partner runs a route of the stops of a dispatched wave and records one
  attempt per package; a package is delivered at most once.

  Background:
    Given a delivery station "node-ds-01"
    And a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a partner "dsp-1" with a van of 2

  Scenario: A route never exceeds the van capacity
    Given the packages "TRK-1", "TRK-2" and "TRK-3" are inducted in wave "wave-1" and induction is closed
    When the planner plans a route with 3 stops
    Then the request is refused with "route over capacity"

  Scenario: An attempt is recorded only on a started route
    Given a planned route "route-1" with the packages "TRK-1" and "TRK-2"
    When the driver records the package "TRK-1" as delivered
    Then the request is refused with "route not started"

  Scenario: A route starts only when its wave is dispatched
    Given a planned route "route-1" with the packages "TRK-1" and "TRK-2"
    When the driver starts route "route-1"
    Then the request is refused with "illegal transition"

  Scenario: Delivering every package completes the route and the wave
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the driver records the package "TRK-1" as delivered
    And the driver records the package "TRK-2" as delivered
    Then route "route-1" is "COMPLETED"
    And wave "wave-1" is "COMPLETED"

  Scenario: A package is delivered at most once
    Given a started route "route-1" with the packages "TRK-1" and "TRK-2"
    And the driver records the package "TRK-1" as delivered
    When the driver records the package "TRK-1" as delivered
    Then the request is refused with "already delivered"

  Scenario: A failed attempt returns the package to the next wave
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the driver records the package "TRK-1" as failed because "recipient absent"
    Then the package "TRK-1" is inducted in wave "wave-2"

  Scenario: The third failed attempt returns the package to its sender
    Given a wave "wave-2" with its cutoff in 48 hours and capacity 5
    And the package "TRK-1" already failed twice
    And a started route "route-1" with the packages "TRK-1" and "TRK-2"
    When the driver records the package "TRK-1" as failed because "recipient absent"
    Then the package "TRK-1" is "RETURN_INITIATED"
    And the event "PackageReturnInitiated" was published for package "TRK-1"
