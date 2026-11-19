# MUTATION.md — mutants gremlins reports that tests cannot kill

Measured 2026-11-15 with gremlins v0.6.0 (`make mutation-full`, then `make mutation`).
The gate in `.gremlins.yaml` sits strictly below the lowest package
(`deliverypoint` measured efficacy 97.62%, mutant coverage 89.36%; ADR 0018 rewrote the tagless
`switch` of `tally`, the ring trim and the 60-day window so that package is now 100% / 100% and has
no entry below). Nothing here is silenced by
configuration; each entry is a mutant that stays LIVED or NOT COVERED.

## Equivalent (LIVED)

None.

## NOT COVERED (go coverage cannot see the line)

| Package | File:line | Mutator | Why |
|---|---|---|---|
| route | route_eta.go:8:21, 11:18 | ARITHMETIC_BASE | `EtaWindowWidth = 2 * time.Hour` and `MinEtaSpan = 30 * time.Minute`: `const` declarations have no statement, so go coverage never marks them. |
