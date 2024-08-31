# Segment Winner Selection Algorithm

This repository provides an implementation of an optimized algorithm for selecting winners from a large set of users based on their accumulated points. The algorithm is designed to efficiently handle probabilistic selection, making it ideal for applications where the selection probability is proportional to the user's contribution (points).

## Key Features and Components

- **Segment Tree Data Structure**: Utilized to store and manage the cumulative points of users, allowing for efficient search and update operations. This is crucial for handling large user sets.

- **Bit Masks for Deletion Tracking**: Bit masks are used to track "deleted" values, helping to manage the state of users efficiently and enabling quick updates in the tree structure.

- **Parallel Processing**: The algorithm employs multithreading to divide the dataset into groups and select winners in parallel. This significantly speeds up computations when working with massive datasets (e.g., 60 million users).

- **Memory and Performance Optimization**: The algorithm is designed to minimize overhead for search and update operations, making it highly efficient for systems where such operations need to be performed frequently and quickly.

## Use Cases

This algorithm is optimal for scenarios where winners need to be selected randomly from a large pool of participants, with the probability of selection being proportional to the number of points accumulated by each participant. It is well-suited for:
- Marketing campaigns and lotteries.
- Games where winning probabilities are dependent on participant contributions.
- Loyalty programs where participants accrue points and wish to participate in prize draws.

## Benefits

- **High Performance**: Efficiently handles large datasets with high performance.
- **Low Memory Consumption**: Optimized for minimal memory usage during update and search operations.
- **Flexibility**: Easily adaptable to various scenarios requiring random selection based on probabilistic distribution.

## Benchmark Results

The benchmark tests were conducted on a Linux system with an AMD Ryzen 3 3300X 4-Core Processor. The results demonstrate the algorithm's efficiency in handling large datasets:

>goos: linux  
>goarch: amd64  
>pkg: github.com/lissteron/segmentwinner  
>cpu: AMD Ryzen 3 3300X 4-Core Processor               
>BenchmarkPick60kk8p-8   	       1	1342137178 ns/op	3604592400 B/op	   48060 allocs/op  
>--- BENCH: BenchmarkPick60kk8p-8  
>    picker_test.go:27: Execution time: 1.342102452s  
>PASS  
>ok  	github.com/lissteron/segmentwinner	2.856s  

The benchmark shows that the algorithm can efficiently handle a dataset of 60 million users, with a total execution time of approximately 1.34 seconds.

### Analysis

- **Execution Time:** 1.34 seconds
- **Memory Usage:** 3.6 GB
- **Number of Allocations:** 48,060

## Example Usage
Here’s a basic example of how to use the Picker struct to select winners:

```go
package main

import (
	"fmt"
	"github.com/lissteron/segmentwinner"
)

func main() {
	// Generate a list of users
	users := generateUsers(60000000)

	// Create a new Picker instance
	picker := segmentwinner.NewPicker(8) // 8 workers

	// Define the number of winners to pick
	numWinners := int(float64(len(users)) * 0.9)

	// Pick the winners
	winners := picker.Do(users, numWinners)

	fmt.Printf("Number of winners: %d\n", len(winners))
}

// Helper function to generate users
func generateUsers(n int) []segmentwinner.User {
	users := make([]segmentwinner.User, n)
	for i := 0; i < n; i++ {
		users[i] = segmentwinner.User{
			ID:     i + 1,
			Points: rand.Intn(3000) + 10,
		}
	}
	return users
}
```

## Conclusion

Use this algorithm for tasks requiring high-performance, optimized selection of winners from large participant pools!

Feel free to explore the code and contribute to further optimizations and improvements!
