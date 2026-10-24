Feature: Dispatching a wave
  Induction closed, the station dispatches its wave: the routes may start, and
  the packages no route holds move to the next wave before it runs (Gap
  Register G17). A wave with no route left to run completes at once (policy
  CompleteWaveOnLastRoute).

  Background:
    Given a delivery station "node-ds-01"
    And a wave "wave-1" with its cutoff in 2 hours and capacity 5
    And a wave "wave-2" with its cutoff in 30 hours and capacity 5
    And a partner "dsp-1" with a van of 2

  Scenario: A package no route holds moves to the next wave at dispatch
    Given the packages "TRK-1" and "TRK-2" are inducted in wave "wave-1" and induction is closed
    And the planner plans a route with 1 stops
    When the planner dispatches wave "wave-1"
    Then wave "wave-1" is "DISPATCHED"
    And the package "TRK-2" is inducted in wave "wave-2"
    And the package "TRK-1" is "INDUCTED"

  Scenario: A wave with no route to run completes at dispatch
    Given the packages "TRK-1" are inducted in wave "wave-1" and induction is closed
    When the planner dispatches wave "wave-1"
    Then wave "wave-1" is "COMPLETED"
    And the package "TRK-1" is inducted in wave "wave-2"

  Scenario: A wave that is not closed cannot be dispatched
    Given the package "TRK-1" arrives at the station
    When the planner dispatches wave "wave-1"
    Then the request is refused with "illegal transition"
    And wave "wave-1" is "OPEN"
