package segmentwinner

import (
	"math/rand"
	"runtime"
	"sync"
	"time"
)

// User represents a user with an ID and points
type User struct {
	ID     int
	Points int
}

// Picker is responsible for managing the parallel execution of the winner selection process.
// It uses a segment tree to efficiently select winners from a large list of users.
type Picker struct {
	numWorkers int            // The number of workers (goroutines) to run in parallel
	wg         sync.WaitGroup // WaitGroup to synchronize the completion of all goroutines
}

// NewPicker initializes a new Picker with a specified number of workers.
// It sets the number of workers to the maximum number of CPUs available or the specified number.
func NewPicker(numWorkers int) *Picker {
	return &Picker{numWorkers: runtime.GOMAXPROCS(numWorkers)}
}

// Do splits users into groups and selects winners in parallel
func (p *Picker) Do(users []User, numWinners int) []User {
	var (
		N            = len(users)
		groupSize    = N / p.numWorkers
		winners      = make([]User, 0, numWinners)
		groupWinners = make([][]User, p.numWorkers)
		groupOffset  = 0
		remainder    = numWinners % p.numWorkers
	)

	// Launch goroutines for each group
	for i := 0; i < p.numWorkers; i++ {
		var (
			start           = groupOffset
			end             = start + groupSize
			groupNumWinners = numWinners / p.numWorkers
		)

		if i < remainder {
			groupNumWinners++
		}

		if i == p.numWorkers-1 {
			end = N // The last group may be larger due to division rounding
		}

		groupOffset = end

		group := users[start:end]
		groupWinners[i] = make([]User, 0, groupNumWinners)

		p.wg.Add(1)

		go p.pickWinnersFromGroup(group, groupNumWinners, &groupWinners[i])
	}

	// Wait for all goroutines to complete
	p.wg.Wait()

	// Collect all results
	for _, gw := range groupWinners {
		winners = append(winners, gw...)
	}

	return winners
}

// pickWinnersFromGroup selects winners from a single group of users using a segment tree
func (p *Picker) pickWinnersFromGroup(users []User, numWinners int, groupWinners *[]User) {
	defer p.wg.Done()

	// Use local random source for faster random number generation without global lock
	localRand := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Determine the number of subgroups based on the number of users
	var (
		subGroupTargetSize = 1000                                  // Adjusted for potentially better performance (smaller log N)
		numSubGroups       = max(1, len(users)/subGroupTargetSize) // The more users, the more subgroups; minimum 1 subgroup
		subGroupSize       = len(users) / numSubGroups
		subGroupResults    = make([][]User, numSubGroups)
		subRemainder       = numWinners % numSubGroups
	)

	for i := 0; i < numSubGroups; i++ {
		var (
			start         = i * subGroupSize
			end           = start + subGroupSize
			subNumWinners = numWinners / numSubGroups
		)

		if i < subRemainder {
			subNumWinners++
		}

		if i == numSubGroups-1 {
			end = len(users) // The last subgroup may be larger due to division rounding
		}

		subGroup := users[start:end]
		subGroupResults[i] = make([]User, 0, subNumWinners)

		// Process without goroutines to save overhead
		var (
			localSt     = NewSegmentTree(subGroup)
			localPoints = localSt.Sum(0, len(subGroup))
		)

		for len(subGroupResults[i]) < subNumWinners && localPoints > 0 {
			var (
				randPoint = localRand.Intn(localPoints) + 1
				index     = localSt.FindIndex(randPoint)
			)

			// Removed redundant check for IsDeleted or Points==0, as tree updates should prevent selection of deleted users

			subGroupResults[i] = append(subGroupResults[i], subGroup[index])
			localPoints -= subGroup[index].Points

			localSt.MarkAsDeleted(index)
		}
	}

	// Collect results from all subgroups
	for _, sg := range subGroupResults {
		*groupWinners = append(*groupWinners, sg...)
	}
}

// max returns the maximum of two numbers
func max(a, b int) int {
	if a > b {
		return a
	}

	return b
}
