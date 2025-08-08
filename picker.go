package segmentwinner

import (
	"math"
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"time"
)

// User — ваш тип
type User struct {
	ID     int
	Points int
}

// Picker — менеджер параллельной выборки
type Picker struct {
	numWorkers int
	seed       int64 // 0 => time.Now().UnixNano(); в тестах можно выставлять напрямую (один пакет)
}

// NewPicker initializes a new Picker with a specified number of workers.
// It sets the number of workers to the maximum number of CPUs available or the specified number.
func NewPicker(numWorkers int) *Picker {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	return &Picker{numWorkers: numWorkers}
}

// Do splits users into groups and selects winners in parallel.
// Алгоритм:
//   - если k > N/2 — выбираем m=N-k "проигравших" (по наибольшим ключам), победители — все остальные;
//   - если k <= N/2 — быстрый reservoir (O(N log k)) по наименьшим ключам.
//
// Ключ = Exp(1)/w, чем МЕНЬШЕ — тем ближе к победителю.
func (p *Picker) Do(users []User, numWinners int) []User {
	n := len(users)
	if n == 0 || numWinners <= 0 {
		return nil
	}
	if numWinners >= n {
		out := make([]User, n)
		copy(out, users)
		return out
	}

	workers := p.numWorkers
	if workers > n {
		workers = n
	}
	chunk := (n + workers - 1) / workers

	baseSeed := p.seed
	if baseSeed == 0 {
		baseSeed = time.Now().UnixNano()
	}

	// Большое k — через "проигравших" (m = N - k)
	if numWinners*2 > n {
		return p.pickViaLosers(users, numWinners, workers, chunk, baseSeed)
	}
	// Малое k — reservoir
	return p.pickReservoirSmallK(users, numWinners, workers, chunk, baseSeed)
}

// ===== splitmix64 RNG (очень быстрый, детерминируемый) =====

type sm64 struct{ s uint64 }

func (r *sm64) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *sm64) f64() float64 { // [0,1)
	return float64(r.next()>>11) * (1.0 / (1 << 53))
}

func (r *sm64) exp1() float64 { // Exp(1) = -ln(U), U∈(0,1]
	u := 1.0 - r.f64()
	if u <= 0 {
		u = math.SmallestNonzeroFloat64
	}
	return -math.Log(u)
}

// ===== минимальные структуры/хелперы =====

type kv struct {
	key float32 // ключ (меньше — «лучше»/ближе к победе)
	idx uint32  // индекс пользователя в исходном слайсе
}

func up[T any](a []T, j int, less func([]T, int, int) bool) {
	for {
		i := (j - 1) >> 1
		if i == j || !less(a, i, j) {
			break
		}
		a[i], a[j] = a[j], a[i]
		j = i
	}
}
func down[T any](a []T, i0, n int, less func([]T, int, int) bool) {
	i := i0
	for {
		j1 := (i << 1) + 1
		if j1 >= n {
			break
		}
		j := j1
		if j2 := j1 + 1; j2 < n && less(a, j1, j2) {
			j = j2
		}
		if !less(a, i, j) {
			break
		}
		a[i], a[j] = a[j], a[i]
		i = j
	}
}
func build[T any](a []T, less func([]T, int, int) bool) {
	for i := (len(a) >> 1) - 1; i >= 0; i-- {
		down(a, i, len(a), less)
	}
}

// ===== путь для БОЛЬШОГО k: выбираем m=N-k проигравших =====

func (p *Picker) pickViaLosers(users []User, numWinners, workers, chunk int, baseSeed int64) []User {
	n := len(users)
	m := n - numWinners // число "проигравших" (того, что кладём в кучу)

	// 1) инфо по чанкам (границы, число позитивных весов)
	type chunkInfo struct {
		start, end int
		posCount   int
	}
	chunks := make([]chunkInfo, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		start := i * chunk
		end := start + chunk
		if start >= n {
			wg.Done()
			continue
		}
		if end > n {
			end = n
		}
		chunks[i].start, chunks[i].end = start, end
		go func() {
			defer wg.Done()
			pc := 0
			for _, u := range users[start:end] {
				if u.Points > 0 {
					pc++
				}
			}
			chunks[i].posCount = pc
		}()
	}
	wg.Wait()

	totalPos := 0
	for _, c := range chunks {
		totalPos += c.posCount
	}
	if totalPos == 0 {
		// все веса нулевые — определённости нет, отдадим первые k как победителей
		out := make([]User, numWinners)
		copy(out, users[:numWinners])
		return out
	}

	// 2) квоты для локальных куч пропорционально количеству позитивных
	quota := make([]int, workers)
	type rem struct {
		i    int
		frac float64
	}
	rems := make([]rem, 0, workers)
	var assigned int
	for i, c := range chunks {
		exact := float64(m) * float64(c.posCount) / float64(totalPos)
		base := int(exact)
		quota[i] = base
		assigned += base
		rems = append(rems, rem{i: i, frac: exact - float64(base)})
	}
	sort.Slice(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for k := 0; k < m-assigned; k++ {
		quota[rems[k].i]++
	}
	// без оверсэмплинга ради скорости
	for i := range quota {
		if quota[i] == 0 && chunks[i].posCount > 0 {
			quota[i] = 1
		}
		if quota[i] > chunks[i].posCount {
			quota[i] = chunks[i].posCount
		}
	}

	// 3) локальные min-heap по НАИБОЛЬШИМ ключам (losers)
	local := make([][]kv, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		c := chunks[i]
		go func() {
			defer wg.Done()
			if c.start >= c.end || quota[i] == 0 {
				return
			}
			kcap := quota[i]
			h := make([]kv, 0, kcap)
			// min-heap: корень — минимальный ключ
			less := func(a []kv, i, j int) bool { return a[i].key < a[j].key }

			r := sm64{s: uint64(baseSeed) ^ uint64(i+1)*0x9e3779b97f4a7c15}

			// заполнение до kcap
			for idx := c.start; idx < c.end && len(h) < kcap; idx++ {
				if users[idx].Points <= 0 {
					continue
				}
				key := float32(r.exp1() / float64(users[idx].Points))
				h = append(h, kv{key: key, idx: uint32(idx)})
			}
			if len(h) == 0 {
				return
			}
			build(h, less)

			// поддерживаем kcap НАИБОЛЬШИХ ключей:
			// если новый ключ > корня (минимума) — заменяем
			for idx := c.start + len(h); idx < c.end; idx++ {
				if users[idx].Points <= 0 {
					continue
				}
				key := float32(r.exp1() / float64(users[idx].Points))
				if key > h[0].key {
					h[0] = kv{key: key, idx: uint32(idx)}
					down(h, 0, len(h), less)
				}
			}
			local[i] = h
		}()
	}
	wg.Wait()

	// 4) глобальный min-heap размера m: держим m НАИБОЛЬШИХ ключей (итоговые losers)
	global := make([]kv, 0, m)
	minLess := func(a []kv, i, j int) bool { return a[i].key < a[j].key } // min-heap

	// инициализация глобальной кучи
	for i := range local {
		for _, e := range local[i] {
			if len(global) < m {
				global = append(global, e)
			} else {
				break
			}
		}
		if len(global) >= m {
			break
		}
	}
	if len(global) == 0 {
		out := make([]User, numWinners)
		copy(out, users[:numWinners])
		return out
	}
	if len(global) < m {
		for i := range local {
			if len(global) >= m {
				break
			}
			if i == 0 {
				continue
			}
			for _, e := range local[i] {
				if len(global) < m {
					global = append(global, e)
				} else {
					break
				}
			}
		}
	}
	build(global, minLess)

	// держим m наибольших ключей
	for i := range local {
		for _, e := range local[i] {
			if e.key > global[0].key {
				global[0] = e
				down(global, 0, len(global), minLess)
			}
		}
	}

	// 5) битсет проигравших и параллельная сборка победителей
	bitmap := make([]uint64, (n+63)/64)
	for _, e := range global {
		ii := int(e.idx)
		bitmap[ii>>6] |= 1 << (uint(ii) & 63)
	}
	return collectWinnersParallel(users, bitmap, workers, numWinners)
}

// Параллельная сборка победителей из битсета "проигравших".
func collectWinnersParallel(users []User, bitmap []uint64, workers, numWinners int) []User {
	n := len(users)
	if workers > n {
		workers = n
	}
	chunk := (n + workers - 1) / workers

	// PASS 1: считаем победителей по чанкам
	counts := make([]int, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w
		start := w * chunk
		end := start + chunk
		if start >= n {
			wg.Done()
			continue
		}
		if end > n {
			end = n
		}
		go func() {
			defer wg.Done()
			losers := 0
			wordStart := start >> 6
			wordEnd := (end + 63) >> 6
			for wi := wordStart; wi < wordEnd; wi++ {
				word := bitmap[wi]
				if word == 0 {
					continue
				}
				if wi == wordStart {
					left := start & 63
					word &= ^uint64(0) << left
				}
				if wi == wordEnd-1 {
					right := (end - 1) & 63
					word &= ^uint64(0) >> (63 - right)
				}
				losers += bits.OnesCount64(word)
			}
			counts[w] = (end - start) - losers
		}()
	}
	wg.Wait()

	// префикс-суммы → оффсеты записи
	offsets := make([]int, workers+1)
	for i := 0; i < workers; i++ {
		offsets[i+1] = offsets[i] + counts[i]
	}
	out := make([]User, numWinners)

	// PASS 2: параллельная запись победителей
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w
		start := w * chunk
		end := start + chunk
		if start >= n {
			wg.Done()
			continue
		}
		if end > n {
			end = n
		}
		dst := offsets[w]
		go func() {
			defer wg.Done()
			i := start
			for i < end {
				wi := i >> 6
				word := bitmap[wi]
				limit := (wi + 1) << 6
				if limit > end {
					limit = end
				}
				// быстрый путь: нет проигравших в этом слове — копируем блоком
				if word == 0 {
					for ; i < limit; i++ {
						out[dst] = users[i]
						dst++
					}
					continue
				}
				// иначе проверяем по битам
				for ; i < limit; i++ {
					if (bitmap[i>>6]>>(uint(i)&63))&1 == 0 {
						out[dst] = users[i]
						dst++
					}
				}
			}
		}()
	}
	wg.Wait()
	return out
}

// ===== путь для МАЛОГО k: быстрый reservoir (O(N log k)) =====

func (p *Picker) pickReservoirSmallK(users []User, k, workers, chunk int, baseSeed int64) []User {
	n := len(users)

	// инфо по чанкам: границы и СУММА ВЕСОВ (исправление квот!)
	type chunkInfo struct {
		start, end int
		sumW       uint64
		pos        int
	}
	ch := make([]chunkInfo, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		start := i * chunk
		end := start + chunk
		if start >= n {
			wg.Done()
			continue
		}
		if end > n {
			end = n
		}
		ch[i] = chunkInfo{start: start, end: end}
		go func() {
			defer wg.Done()
			var s uint64
			pos := 0
			for _, u := range users[start:end] {
				if u.Points > 0 {
					s += uint64(u.Points)
					pos++
				}
			}
			ch[i].sumW = s
			ch[i].pos = pos
		}()
	}
	wg.Wait()

	var totalW uint64
	for _, c := range ch {
		totalW += c.sumW
	}
	if totalW == 0 {
		return nil
	}

	// Квоты ∝ сумме весов (исправление)
	quota := make([]int, workers)
	type rem struct {
		i    int
		frac float64
	}
	rems := make([]rem, 0, workers)
	var assigned int
	for i, c := range ch {
		ex := float64(k) * float64(c.sumW) / float64(totalW)
		base := int(ex)
		quota[i] = base
		assigned += base
		rems = append(rems, rem{i: i, frac: ex - float64(base)})
	}
	sort.Slice(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for t := 0; t < k-assigned; t++ {
		quota[rems[t].i]++
	}
	for i := range quota {
		if quota[i] > ch[i].pos { // не больше числа позитивных
			quota[i] = ch[i].pos
		}
	}

	// локальные max-heap: держим kcap ЛУЧШИХ (наименьшие ключи), корень — худший среди лучших
	local := make([][]kv, workers)
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		start, end := ch[i].start, ch[i].end
		go func() {
			defer wg.Done()
			capK := quota[i]
			if capK == 0 || start >= end {
				return
			}
			h := make([]kv, 0, capK)
			// max-heap: корень — максимальный ключ
			less := func(a []kv, i, j int) bool { return a[i].key > a[j].key }

			r := sm64{s: uint64(baseSeed) ^ uint64(i+1)*0x9e3779b97f4a7c15}

			for idx := start; idx < end && len(h) < capK; idx++ {
				if users[idx].Points <= 0 {
					continue
				}
				key := float32(r.exp1() / float64(users[idx].Points))
				h = append(h, kv{key: key, idx: uint32(idx)})
			}
			if len(h) == 0 {
				return
			}
			build(h, less)
			for idx := start + len(h); idx < end; idx++ {
				if users[idx].Points <= 0 {
					continue
				}
				key := float32(r.exp1() / float64(users[idx].Points))
				if key < h[0].key {
					h[0] = kv{key: key, idx: uint32(idx)}
					down(h, 0, len(h), less)
				}
			}
			local[i] = h
		}()
	}
	wg.Wait()

	// глобальный max-heap размера k
	global := make([]kv, 0, k)
	maxLess := func(a []kv, i, j int) bool { return a[i].key > a[j].key } // max-heap

	// начальная загрузка
	for i := range local {
		for _, e := range local[i] {
			if len(global) < k {
				global = append(global, e)
			} else {
				break
			}
		}
		if len(global) >= k {
			break
		}
	}
	if len(global) == 0 {
		return nil
	}
	if len(global) < k {
		for i := range local {
			if len(global) >= k {
				break
			}
			if i == 0 {
				continue
			}
			for _, e := range local[i] {
				if len(global) < k {
					global = append(global, e)
				} else {
					break
				}
			}
		}
	}
	build(global, maxLess)

	for i := range local {
		for _, e := range local[i] {
			if e.key < global[0].key {
				global[0] = e
				down(global, 0, len(global), maxLess)
			}
		}
	}

	// выгружаем в порядке возрастания ключа (не обязательно, но удобно)
	sort.Slice(global, func(i, j int) bool { return global[i].key < global[j].key })

	out := make([]User, len(global))
	for i := range global {
		out[i] = users[int(global[i].idx)]
	}
	return out
}
