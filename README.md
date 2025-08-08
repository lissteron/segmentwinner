# Segment Winner — Fast Weighted Sampling Without Replacement

This library picks winners from a very large user set where each user has a weight (`Points`). A user’s chance to be selected is proportional to their weight. It’s built to scale to **tens of millions** of users with predictable performance and low overhead.

## Why it’s fast

### Hybrid algorithm (O(N log K) / O(N log (N−K)))

We use the Efraimidis–Spirakis trick: for each user with weight `w`, generate a key
`key = Exp(1) / w`. The **smaller** the key, the **more likely** the user is a winner.

* **Small K (K ≪ N)** → *reservoir sampling*: keep a **max-heap** of size K holding the best (smallest-key) candidates ⇒ `O(N log K)`.
* **Large K (e.g., 90% of N)** → pick **losers** instead: keep a **min-heap** of size M = N−K holding the worst (largest-key) candidates ⇒ `O(N log (N−K))` and dramatically less work/memory when K is large.

### Parallel without contention

* Split `users` into chunks; each worker uses a **local RNG** and local heap (no locking).
* Lightweight **quotas** per chunk to avoid memory blow-ups.
* Final winner collection is **two-pass and parallel**:

  1. Count winners per chunk (via a bitset of losers),
  2. prefix-sum to compute write offsets, then **workers write winners directly** into the output slice. No atomics, no locks.

### Memory efficiency

* Heaps store only `(key, idx)` as `(float32, uint32)` → **8 bytes/entry**.
* Loser bitset is `N/8` bytes (≈ 7.5 MB for 60M).
* The biggest cost (when returning full `User` structs) is copying winners into the result slice; if that’s an issue, return **indices** instead (see below).

### RNG optimized for hot loops

* Per-worker **splitmix64** + `-ln(U)` for Exp(1) — faster than `math/rand` + `ExpFloat64()` inside tight loops, and avoids global locks.

## Complexity

* **Small K:** time `O(N log K)`, memory `O(K)` (+ bitset if needed).
* **Large K:** time `O(N log (N−K))`, memory `O(N−K)` (+ bitset).
* Final collection is linear and **parallelized**.

## Correctness

* **Sampling without replacement**, probabilities ∝ weights.
* With a fixed seed (used internally), results are deterministic (handy for tests).

## API

```go
picker := segmentwinner.NewPicker(numWorkers)
winners := picker.Do(users, numWinners)
```

* If `numWorkers <= 0`, it defaults to `runtime.NumCPU()`.
* If all `Points == 0`, the result is empty (or trivial, see code for the edge path).
* Order of winners follows the original order (stable) after the final collect.

### Optional: return indices (faster, less memory)

If you frequently select a huge fraction (e.g., 90%) and copying full structs is expensive, consider adding:

```go
// DoIndices returns indices into the original users slice instead of copying structs.
// Typically saves hundreds of milliseconds and large allocations at 60M/90%.
func (p *Picker) DoIndices(users []User, numWinners int) []int
```

## Benchmarks (example)

Platform: Linux, AMD Ryzen 3 3300X (4C/8T)

```
goos: linux
goarch: amd64
pkg: github.com/lissteron/segmentwinner
cpu: AMD Ryzen 3 3300X 4-Core Processor             
BenchmarkPick60kk8p-8   	       2	 625225184 ns/op	967562208 B/op	      81 allocs/op
PASS
ok  	github.com/lissteron/segmentwinner	2.458s
```

* **Time:** \~0.625 s per call (`ns/op` is the metric to compare)
* **Memory:** \~0.97 GB (mostly copying the \~54M winners)
* **Allocs:** 81

> For apples-to-apples comparisons:
>
> ```bash
> go test -bench '^BenchmarkPick60kk8p$' -benchmem -benchtime=1x -run ^$
> ```
>
> Compare `ns/op`, not the total test runtime.

## Example

```go
package main

import (
	"fmt"
	"math/rand"
	"runtime"

	"github.com/lissteron/segmentwinner"
)

func main() {
	N := 60_000_000
	users := make([]segmentwinner.User, N)
	for i := range users {
		users[i] = segmentwinner.User{
			ID:     i,
			Points: rand.Intn(3000) + 10, // any positive weights
		}
	}

	picker := segmentwinner.NewPicker(runtime.NumCPU())
	numWinners := int(0.90 * float64(len(users)))

	winners := picker.Do(users, numWinners)
	fmt.Println("winners:", len(winners))
}
```

## Tuning tips

* **Workers:** `runtime.NumCPU()` is usually best; you can tune per host.
* **Switch threshold:** currently `K > N/2` flips to the “losers” path; this is a good heuristic for most distributions.
* **Key type:** `float32` improves cache/throughput; use `float64` only if you need extra numerical headroom.
* **GC & repeated calls:** if you call this in tight loops, consider `sync.Pool` for heaps/bitsets to reduce GC pressure (not required for one-off runs).

## Why not a Segment Tree?

Segment trees shine for **many repeated** point queries/updates. Here we do **one large** weighted sample *without* replacement. The `Exp(1)/w` key method needs a single linear scan, parallelizes naturally, and avoids expensive per-pick updates to a global structure. That’s why this approach wins on both **simplicity** and **throughput** at scale.

---

Questions or PRs welcome! If you want a `DoIndices` variant or pools baked in, open an issue and we’ll wire it up.
