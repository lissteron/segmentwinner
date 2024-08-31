package segmentwinner

import (
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
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
		groupSize    = len(users) / p.numWorkers
		totalPoints  int32
		winners      = make([]User, 0, numWinners)
		groupWinners = make([][]User, p.numWorkers)
	)

	// Launch goroutines for each group
	for i := 0; i < p.numWorkers; i++ {
		var (
			start = i * groupSize
			end   = start + groupSize
		)

		if i == p.numWorkers-1 {
			end = len(users) // The last group may be larger due to division rounding
		}

		group := users[start:end]
		groupWinners[i] = make([]User, 0, numWinners/p.numWorkers)

		p.wg.Add(1)

		go p.pickWinnersFromGroup(group, numWinners/p.numWorkers, &groupWinners[i], &totalPoints)
	}

	// Wait for all goroutines to complete
	p.wg.Wait()

	// Collect all results
	for _, gw := range groupWinners {
		winners = append(winners, gw...)
	}

	// Ensure the number of winners matches the expected count (numWinners)
	// If necessary, select additional winners from the combined array
	if len(winners) < numWinners {
		var (
			additionalWinnersNeeded = numWinners - len(winners)
			extraWinners            = p.Do(winners, additionalWinnersNeeded)
		)

		winners = append(winners, extraWinners...)
	}

	return winners
}

// pickWinnersFromGroup selects winners from a single group of users using a segment tree
func (p *Picker) pickWinnersFromGroup(users []User, numWinners int, groupWinners *[]User, totalPoints *int32) {
	defer p.wg.Done()

	// Determine the number of subgroups based on the number of users
	var (
		numSubGroups    = max(1, len(users)/5000) // The more users, the more subgroups; minimum 1 subgroup
		subGroupSize    = len(users) / numSubGroups
		subGroupResults = make([][]User, numSubGroups)
	)

	for i := 0; i < numSubGroups; i++ {
		var (
			start = i * subGroupSize
			end   = start + subGroupSize
		)

		if i == numSubGroups-1 {
			end = len(users) // The last subgroup may be larger due to division rounding
		}

		subGroup := users[start:end]
		subGroupResults[i] = make([]User, 0, numWinners/numSubGroups)

		// Process without goroutines to save overhead
		var (
			localSt     = NewSegmentTree(subGroup)
			localPoints = localSt.Sum(0, len(subGroup))
		)

		for len(subGroupResults[i]) < numWinners/numSubGroups && localPoints > 0 {
			var (
				randPoint = rand.Intn(localPoints) + 1
				index     = localSt.FindIndex(randPoint)
			)

			if localSt.IsDeleted(index) || subGroup[index].Points == 0 {
				continue
			}

			subGroupResults[i] = append(subGroupResults[i], subGroup[index])
			localPoints -= subGroup[index].Points

			localSt.MarkAsDeleted(index)
		}

		atomic.AddInt32(totalPoints, int32(localPoints))
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
