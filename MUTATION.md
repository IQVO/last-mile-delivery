# MUTATION.md — mutants gremlins reports that tests cannot kill

Measured 2026-11-15 with gremlins v0.6.0 (`make mutation-full`, then `make mutation`).
The gate in `.gremlins.yaml` sits strictly below the lowest package
(`deliverypoint`: efficacy 97.62%, mutant coverage 89.36%). Nothing here is silenced by
configuration; each entry is a mutant that stays LIVED or NOT COVERED.

## Equivalent (LIVED)

| Package | File:line | Mutator | Why it is equivalent |
|---|---|---|---|
| deliverypoint | observe.go:124:47 | CONDITIONALS_BOUNDARY (`over > 0` -> `over >= 0`) | With exactly `MaxStrikes` strikes `over` is 0 and the mutant only clones the full slice (`slices.Clone(p.strikes[0:])`): same content, same order, nothing observable changes. |

## NOT COVERED (go coverage cannot see the line)

| Package | File:line | Mutator | Why |
|---|---|---|---|
| deliverypoint | observe.go:96:17, 101:16, 103:16 | CONDITIONALS_NEGATION | The case expressions of the tagless `switch` in `tally`. Go's coverage profile gives a `case` clause a block that starts after the colon, so gremlins sees the expression line as uncovered although every branch is executed and asserted by the tests. This is the "tagless switch under-reports" pitfall of HARNESS.md; the remedy there is an if-chain rewrite of production code, which this ratchet did not do. |
| deliverypoint | profile.go:26:20, 26:25 | ARITHMETIC_BASE | `StrikeWindow = 60 * 24 * time.Hour` is a `const` declaration: it has no statement, so go coverage never marks it. The 60-day window is asserted at its boundary by `TestStrikeWindowExpiry`. |
| route | route_eta.go:8:21, 11:18 | ARITHMETIC_BASE | `EtaWindowWidth = 2 * time.Hour` and `MinEtaSpan = 30 * time.Minute`: `const` declarations, same reason. |
