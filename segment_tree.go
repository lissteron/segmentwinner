package segmentwinner

import "sync/atomic"

// SegmentTree represents the data structure of a segment tree
type SegmentTree struct {
	tree       []int
	bitmask    []uint64 // Bitmask to track "deleted" values
	n          int
	bitMapSize int
}

// NewSegmentTree creates a new segment tree
func NewSegmentTree(users []User) *SegmentTree {
	var (
		n       = len(users)
		tree    = make([]int, 2*n)
		bitmask = make([]uint64, (n+63)/64) // Initialize the bitmask
		st      = &SegmentTree{tree: tree, bitmask: bitmask, n: n, bitMapSize: 64}
	)

	st.build(users)

	return st
}

// build constructs the segment tree from an array of users
func (st *SegmentTree) build(users []User) {
	// Fill the leaves
	for i := 0; i < st.n; i++ {
		st.tree[st.n+i] = users[i].Points
	}

	// Fill the internal nodes
	for i := st.n - 1; i > 0; i-- {
		st.tree[i] = st.tree[i*2] + st.tree[i*2+1]
	}
}

// Update modifies a value in the segment tree
func (st *SegmentTree) Update(index int, value int) {
	index += st.n
	st.tree[index] = value

	for index > 1 {
		index /= 2
		st.tree[index] = st.tree[index*2] + st.tree[index*2+1]
	}
}

// MarkAsDeleted sets the deletion bit in the bitmask
func (st *SegmentTree) MarkAsDeleted(index int) {
	var (
		word = index / st.bitMapSize
		bit  = index % st.bitMapSize
	)

	atomic.StoreUint64(&st.bitmask[word], st.bitmask[word]|(1<<bit))

	st.Update(index, 0) // Immediately update the tree
}

// IsDeleted checks if an element is deleted
func (st *SegmentTree) IsDeleted(index int) bool {
	var (
		word = index / st.bitMapSize
		bit  = index % st.bitMapSize
	)

	return (st.bitmask[word] & (1 << bit)) != 0
}

// Sum calculates the sum of elements in a given range
func (st *SegmentTree) Sum(left, right int) int {
	left += st.n
	right += st.n

	var sum int
	for left < right {
		if left%2 == 1 {
			sum += st.tree[left]
			left++
		}

		if right%2 == 1 {
			right--
			sum += st.tree[right]
		}

		left /= 2
		right /= 2
	}

	return sum
}

// FindIndex finds the index of a user corresponding to a random sum
func (st *SegmentTree) FindIndex(target int) int {
	index := 1

	for index < st.n {
		if st.tree[index*2] >= target {
			index = index * 2
		} else {
			target -= st.tree[index*2]
			index = index*2 + 1
		}
	}

	return index - st.n
}
