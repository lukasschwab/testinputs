# Initial performance baseline

Measured on 2026-09-18 with Go 1.27.0, macOS/arm64, Apple M2 Pro. The synthetic
modules exercise helper summaries, temporary joins, unknown paths, and facts.
Package loading is excluded from the timed analyzer loop.

```sh
/usr/bin/time -l go test -run '^$' -bench BenchmarkAnalyzer -benchtime=1x -benchmem ./analyzer
```

| Module size | Analysis time | Allocated bytes | Allocations | Unresolved call boundaries |
| --- | ---: | ---: | ---: | ---: |
| 10 helpers/tests | 10.1 ms | 7,635,152 | 22,701 | 10 |
| 1,000 helpers/tests | 34.0 ms | 38,703,320 | 346,533 | 1,000 |

The complete command took 1.85 seconds wall time. Maximum resident set size
reported by macOS `time` was 262,422,528 bytes (about 250 MiB), including the Go
command, package loading, and the test process. This is not an isolated analyzer
peak-heap measurement. An earlier single-iteration run measured 4.6 ms and 33.5 ms;
single-sample timings are noisy.

Initial review thresholds for a comparable warmed machine are 100 ms / 32 MiB
allocated for the small analysis and 500 ms / 128 MiB allocated for the larger
analysis. These are investigation thresholds, not CI wall-clock tests. Rebaseline
against real repositories before enforcing an overhead SLO.

Algorithmic limits are independent: 12 summary iterations, 128 operations per
function, and provenance trees limited to depth 12 and 128 nodes. Exhaustion is
visible as incomplete coverage and TFS002. Unresolved calls are counted rather
than presumed filesystem-free.
