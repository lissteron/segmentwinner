package segmentwinner_test

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/lissteron/segmentwinner"
)

func BenchmarkPick60kk8p(b *testing.B) {
	var (
		users  = generateUsers(60_000_000)
		picker = segmentwinner.NewPicker(0)
	)

	b.ResetTimer()

	start := time.Now() // Start timing

	picker.Do(users, int(float64(len(users))*0.9))

	duration := time.Since(start) // Measure execution time

	b.Logf("Execution time: %v", duration)
	b.StopTimer() // Stop timer
}

func TestPickWinners(t *testing.T) {
	var (
		users      = generateUsers(60_000)
		numWinners = int(float64(len(users)) * 0.9)

		picker  = segmentwinner.NewPicker(0)
		winners = picker.Do(users, numWinners)
	)

	if len(winners) != numWinners {
		t.Errorf("Expected %d winners, but got %d", numWinners, len(winners))
	}

	// Check that all winners are unique
	winnerIDs := make(map[int]bool)
	for _, winner := range winners {
		if winnerIDs[winner.ID] {
			t.Errorf("Duplicate winner found with ID %d", winner.ID)
		}
		winnerIDs[winner.ID] = true
	}

	// Check that no ID is repeated
	for _, winner := range winners {
		if !winnerIDs[winner.ID] {
			t.Errorf("Winner ID %d not found", winner.ID)
		}
	}
}

func TestDistributionOfPrizes(t *testing.T) {
	const numSimulations = 2000
	var (
		users      = generateUsers(60_000) // Smaller set for testing
		numWinners = int(float64(len(users)) * 0.1)
		picker     = segmentwinner.NewPicker(0)
	)

	// Initialize a map to track the number of wins for each user
	winCounts := make(map[int]int)
	winSlices := make([][]int, 0, numSimulations)

	// Conduct multiple simulations
	for i := 0; i < numSimulations; i++ {
		ru := make([]segmentwinner.User, len(users))
		copy(ru, users)

		var (
			winners = picker.Do(ru, numWinners)
			ids     = make([]int, 0, len(winners))
		)

		for _, winner := range winners {
			winCounts[winner.ID]++
			ids = append(ids, winner.ID)
		}

		winSlices = append(winSlices, ids)
	}

	for i, user1 := range winSlices {
		for k, user2 := range winSlices {
			if i != k && slices.Compare(user1, user2) == 0 {
				t.Errorf("Users should be unique")
			}
		}
	}

	// Verify that users with higher scores win more frequently
	var (
		highPointsWins = 0
		lowPointsWins  = 0
	)

	for _, user := range users {
		if user.Points >= 1500 { // Count users with high scores
			highPointsWins += winCounts[user.ID]
		} else { // Count users with low scores
			lowPointsWins += winCounts[user.ID]
		}
	}

	fmt.Printf("Total wins with high scores: %d, with low scores: %d\n", highPointsWins, lowPointsWins)

	// Check that the probability of winning is higher for users with higher scores
	if highPointsWins <= lowPointsWins {
		t.Errorf("Users with higher scores should win more often: %d <= %d", highPointsWins, lowPointsWins)
	}
}

// generateUsers generates a list of users with random scores
func generateUsers(n int) []segmentwinner.User {
	users := make([]segmentwinner.User, n)

	for i := 0; i < n; i++ {
		users[i] = segmentwinner.User{
			ID:     i + 1,
			Points: rand.Intn(3000) + 10, // Random number of points from 10 to 3000
		}
	}

	return users
}
