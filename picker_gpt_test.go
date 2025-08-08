package segmentwinner

import (
	"math/rand"
	"runtime"
	"sort"
	"testing"
	"time"
)

// ---- helpers ----

func genUsersEqualWeights(n int) []User {
	us := make([]User, n)
	for i := 0; i < n; i++ {
		us[i] = User{ID: i, Points: 1}
	}
	return us
}

func genUsersTwoWeights(n int, w1, w2 int) []User {
	us := make([]User, n)
	half := n / 2
	for i := 0; i < half; i++ {
		us[i] = User{ID: i, Points: w1}
	}
	for i := half; i < n; i++ {
		us[i] = User{ID: i, Points: w2}
	}
	return us
}

func hasDuplicates(ids []int) bool {
	if len(ids) <= 1 {
		return false
	}
	cp := append([]int(nil), ids...)
	sort.Ints(cp)
	for i := 1; i < len(cp); i++ {
		if cp[i] == cp[i-1] {
			return true
		}
	}
	return false
}

func toIDs(us []User) []int {
	out := make([]int, len(us))
	for i := range us {
		out[i] = us[i].ID
	}
	return out
}

// ---- unit tests ----

func TestDo_KZero_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	p := NewPicker(runtime.NumCPU())
	users := genUsersEqualWeights(1000)

	got := p.Do(users, 0)
	if len(got) != 0 {
		t.Fatalf("want 0 winners, got %d", len(got))
	}
}

func TestDo_KEqualsN_ReturnsAll(t *testing.T) {
	t.Parallel()
	p := NewPicker(runtime.NumCPU())
	users := genUsersEqualWeights(1234)

	got := p.Do(users, len(users))
	if len(got) != len(users) {
		t.Fatalf("want %d winners, got %d", len(users), len(got))
	}

	// проверим, что это те же ID (порядок нас не волнует)
	m := make(map[int]struct{}, len(users))
	for _, u := range users {
		m[u.ID] = struct{}{}
	}
	for _, u := range got {
		if _, ok := m[u.ID]; !ok {
			t.Fatalf("unexpected winner id=%d", u.ID)
		}
	}
}

func TestDo_NoDuplicates_SmallK(t *testing.T) {
	t.Parallel()
	p := NewPicker(runtime.NumCPU())
	users := genUsersEqualWeights(10_000)

	k := 777
	got := p.Do(users, k)
	ids := toIDs(got)

	if len(got) != k {
		t.Fatalf("want %d winners, got %d", k, len(got))
	}
	if hasDuplicates(ids) {
		t.Fatalf("winners contain duplicates")
	}
}

func TestDo_NoDuplicates_BigK(t *testing.T) {
	t.Parallel()
	p := NewPicker(runtime.NumCPU())
	users := genUsersEqualWeights(50_000)

	k := int(float64(len(users)) * 0.9)
	got := p.Do(users, k)
	ids := toIDs(got)

	if len(got) != k {
		t.Fatalf("want %d winners, got %d", k, len(got))
	}
	if hasDuplicates(ids) {
		t.Fatalf("winners contain duplicates")
	}
}

func TestDo_Deterministic_WithSeed(t *testing.T) {
	t.Parallel()
	p := NewPicker(runtime.NumCPU())
	// доступ к p.seed есть, т.к. тест в том же пакете
	p.seed = 42

	users := genUsersEqualWeights(20_000)
	k := 5_000

	a := p.Do(users, k)
	b := p.Do(users, k)

	idsA := toIDs(a)
	idsB := toIDs(b)

	if len(idsA) != len(idsB) {
		t.Fatalf("determinism: lengths differ: %d vs %d", len(idsA), len(idsB))
	}
	for i := range idsA {
		if idsA[i] != idsB[i] {
			t.Fatalf("determinism: results differ at %d: %d vs %d", i, idsA[i], idsB[i])
		}
	}
}

func TestDo_WeightedBias_TwoGroups(t *testing.T) {
	t.Parallel()
	// Быстрый статистический sanity-check:
	// половина пользователей с весом 1, половина с весом 5.
	// Выбираем k = 10% от N. Повторяем несколько независимых запусков
	// (с разными seed) и проверяем, что во второй группе победителей
	// на порядок больше (в пересчёте на одного пользователя).
	N := 20_000
	K := N / 10
	users := genUsersTwoWeights(N, 1, 5)

	p := NewPicker(runtime.NumCPU())

	const trials = 30
	var sumA, sumB int // число победителей по группам суммарно за все прогоны
	half := N / 2

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for tcase := 0; tcase < trials; tcase++ {
		p.seed = rng.Int63() // разные сиды
		winners := p.Do(users, K)

		var a, b int
		for _, u := range winners {
			if u.ID < half {
				a++
			} else {
				b++
			}
		}
		sumA += a
		sumB += b
	}

	// ожидаем, что доля "тяжёлой" группы сильно больше.
	// На практике отношение близко к ~5x, но оставим запас.
	// Сравниваем долю победителей на пользователя.
	perCapA := float64(sumA) / float64(half)
	perCapB := float64(sumB) / float64(half)

	if perCapB <= perCapA*1.8 { // 1.8 — мягкий нижний порог, чтобы тест был стабильным
		t.Fatalf("weighted bias seems broken: per-cap heavy=%.3f, light=%.3f", perCapB, perCapA)
	}
}

// ---- benchmarks ----

// ВНИМАНИЕ: этот бенч может требовать много памяти/времени.
// Запускать так:  go test -bench '^BenchmarkPick60kk8p2$' -benchmem -benchtime=1x -run ^$
func BenchmarkPick60kk8p2(b *testing.B) {
	N := 60_000_000
	K := int(float64(N) * 0.90)

	users := make([]User, N)
	for i := 0; i < N; i++ {
		users[i] = User{ID: i, Points: 1}
	}

	p := NewPicker(runtime.NumCPU())
	p.seed = 123 // стабильность между прогонами

	b.ReportAllocs()
	b.ResetTimer()
	var sink []User
	for i := 0; i < b.N; i++ {
		sink = p.Do(users, K)
		if len(sink) != K {
			b.Fatalf("got %d winners, want %d", len(sink), K)
		}
	}
	_ = sink
}
