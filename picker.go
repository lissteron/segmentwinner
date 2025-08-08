package segmentwinner

import (
	"math"
	"math/bits"
	"runtime"
	"sort"
	"sync"
	"time"
)

type User struct {
	ID     int
	Points int
}

type Picker struct {
	numWorkers int
	seed       int64 // 0 => time.Now().UnixNano()
}

func NewPicker(numWorkers int) *Picker {
	if numWorkers <= 0 {
		numWorkers = runtime.NumCPU()
	}
	return &Picker{numWorkers: numWorkers}
}

// ===== splitmix64 RNG =====

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

// ===== минимальный heap без аллокаций на операцию =====

type kv struct {
	key float32
	idx uint32
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

// ===== основной API =====

func (p *Picker) Do(users []User, numWinners int) []User {
	n := len(users)
	if n == 0 || numWinners <= 0 {
		return nil
	}
	if numWinners >= n {
		// тривиальный случай
		out := make([]User, 0, n)
		out = append(out, users...)
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

	// Если k > N/2 — работаем "через проигравших"
	if numWinners*2 > n {
		return p.pickViaLosers(users, numWinners, workers, chunk, baseSeed)
	}

	// (опционально) путь для малого k — можно оставить прежний быстрый reservoir
	return p.pickReservoirSmallK(users, numWinners, workers, chunk, baseSeed)
}

// ---- Быстрый путь для большого k: выбираем m=N-k проигравших ----

func (p *Picker) pickViaLosers(users []User, numWinners, workers, chunk int, baseSeed int64) []User {
	n := len(users)
	m := n - numWinners // сколько надо "выбросить"

	// 1) суммарные веса по чанкам, чтобы дать квоты
	type chunkInfo struct {
		start, end int
		sum        uint64
		posCount   int // число пользователей с Points>0
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
		chunks[i] = chunkInfo{start: start, end: end}
		go func() {
			defer wg.Done()
			var s uint64
			var pc int
			for _, u := range users[start:end] {
				if u.Points > 0 {
					s += uint64(u.Points)
					pc++
				}
			}
			chunks[i].sum = s
			chunks[i].posCount = pc
		}()
	}
	wg.Wait()

	var totalW uint64
	for _, c := range chunks {
		totalW += c.sum
	}
	if totalW == 0 {
		// все веса нулевые — произвольные m "проигравших"
		bitmap := make([]uint64, (n+63)/64)
		// просто первые m как проигравшие
		for i := 0; i < m; i++ {
			word := i >> 6
			bit := uint(i & 63)
			bitmap[word] |= 1 << bit
		}
		// было: последовательный однопоточный проход
		// out := make([]User, 0, numWinners)
		// for i, u := range users {
		//     if (bitmap[i>>6]>>(uint(i)&63))&1 == 0 {
		//         out = append(out, u)
		//     }
		// }

		// стало:
		out := collectWinnersParallel(users, bitmap, workers, numWinners)
		return out
	}

	// 2) квоты проигравших по чанкам пропорционально весам, + guard (12.5%)
	quota := make([]int, workers)
	rems := make([]struct {
		i    int
		frac float64
	}, 0, workers)
	var assigned int
	for i, c := range chunks {
		exact := float64(m) * float64(c.sum) / float64(totalW)
		base := int(exact)
		quota[i] = base
		assigned += base
		rems = append(rems, struct {
			i    int
			frac float64
		}{i: i, frac: exact - float64(base)})
	}
	sort.Slice(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for k := 0; k < m-assigned; k++ {
		quota[rems[k].i]++
	}

	// guard и верхняя граница
	const guardDiv = 8 // 12.5% оверсэмпл
	for i := range quota {
		q := quota[i]
		if q == 0 && chunks[i].posCount > 0 {
			q = 1
		}
		q += q / guardDiv
		if q > chunks[i].posCount {
			q = chunks[i].posCount
		}
		quota[i] = q
	}

	// 3) каждая горутина держит max-heap из quota[i] худших ключей (т.е. локальные "проигравшие"-кандидаты)
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
			less := func(a []kv, i, j int) bool { return a[i].key > a[j].key } // max-heap

			r := sm64{s: uint64(baseSeed) ^ uint64(i+1)*0x9e3779b97f4a7c15}

			// заполняем до kcap
			for idx := c.start; idx < c.end && len(h) < kcap; idx++ {
				if users[idx].Points <= 0 {
					continue
				}
				key := float32(r.exp1() / float64(users[idx].Points)) // меньше — лучше
				h = append(h, kv{key: key, idx: uint32(idx)})
			}
			if len(h) == 0 {
				return
			}
			build(h, less)

			// если заполнено — идём по остальным
			for idx := c.start + len(h); idx < c.end; idx++ {
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

	// 4) глобальный max-heap размера m: берём глобально m наименьших ключей (т.е. проигравших)
	// (в худшем — m = 6e6; элемент 16 байт => ~96 МБ)
	global := make([]kv, 0, m)
	less := func(a []kv, i, j int) bool { return a[i].key > a[j].key } // max-heap
	// build из первых чанков, пока не наберём m
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
		// никто не имел положительных очков
		return nil
	}
	if len(global) < m {
		// добираем оставшимся
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
	build(global, less)
	// теперь регулярно пытаемся улучшить global худший
	for i := range local {
		for _, e := range local[i] {
			if e.key < global[0].key {
				global[0] = e
				down(global, 0, len(global), less)
			}
		}
	}

	// 5) помечаем проигравших битсет-маской и собираем победителей без доп. аллокаций
	bitmap := make([]uint64, (n+63)/64)
	for _, e := range global {
		ii := int(e.idx)
		bitmap[ii>>6] |= 1 << (uint(ii) & 63)
	}

	out := make([]User, 0, numWinners)
	for i, u := range users {
		if (bitmap[i>>6]>>(uint(i)&63))&1 == 0 {
			out = append(out, u)
		}
	}
	return out
}

// ---- путь для малого k (можно оставить простым и быстрым) ----

func (p *Picker) pickReservoirSmallK(users []User, k, workers, chunk int, baseSeed int64) []User {
	n := len(users)
	type heapS struct{ a []kv }
	less := func(a []kv, i, j int) bool { return a[i].key > a[j].key } // max-heap

	// локальные квоты примерно пропорционально числу позитивных в чанке + guard
	type chunkInfo struct {
		start, end int
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
			pos := 0
			for _, u := range users[start:end] {
				if u.Points > 0 {
					pos++
				}
			}
			ch[i].pos = pos
		}()
	}
	wg.Wait()

	// квоты по числу позитивных
	totalPos := 0
	for _, c := range ch {
		totalPos += c.pos
	}
	if totalPos == 0 {
		return nil
	}
	quota := make([]int, workers)
	var assigned int
	rems := make([]struct {
		i    int
		frac float64
	}, 0, workers)
	for i, c := range ch {
		ex := float64(k) * float64(c.pos) / float64(totalPos)
		base := int(ex)
		quota[i] = base
		assigned += base
		rems = append(rems, struct {
			i    int
			frac float64
		}{i: i, frac: ex - float64(base)})
	}
	sort.Slice(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for t := 0; t < k-assigned; t++ {
		quota[rems[t].i]++
	}
	// лёгкий guard
	for i := range quota {
		quota[i] += quota[i] / 8
		if quota[i] > ch[i].pos {
			quota[i] = ch[i].pos
		}
	}

	locals := make([][]kv, workers)
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
			r := sm64{s: uint64(baseSeed) ^ uint64(i+1)*0x9e3779b97f4a7c15}

			// заполнение
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
			locals[i] = h
		}()
	}
	wg.Wait()

	// глобальный max-heap размера k
	global := make([]kv, 0, k)
	for i := range locals {
		for _, e := range locals[i] {
			if len(global) < k {
				global = append(global, e)
			} else if e.key < global[0].key {
				global[0] = e
				down(global, 0, len(global), less)
			}
			if len(global) == k && i == 0 && len(global) == cap(global) {
				// build один раз, если не сделали
				build(global, less)
			}
		}
	}
	if len(global) < k {
		build(global, less)
	}
	// выгрузка в возрастающем порядке ключа
	sort.Slice(global, func(i, j int) bool { return global[i].key < global[j].key })

	out := make([]User, 0, len(global))
	for _, e := range global {
		out = append(out, users[int(e.idx)])
	}
	return out
}

func collectWinnersParallel(users []User, bitmap []uint64, workers, numWinners int) []User {
	n := len(users)
	if workers > n {
		workers = n
	}
	chunk := (n + workers - 1) / workers

	// --- PASS 1: считаем победителей по чанкам ---
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
			// считаем проигравших словом, чтобы меньше условных переходов
			wordStart := start >> 6
			wordEnd := (end + 63) >> 6
			for wi := wordStart; wi < wordEnd; wi++ {
				word := bitmap[wi]
				// маска для краёв чанка
				left := 0
				right := 63
				if wi == wordStart {
					left = start & 63
					word &= ^uint64(0) << left
				}
				if wi == wordEnd-1 {
					right = (end - 1) & 63
					word &= ^uint64(0) >> (63 - right)
				}
				losers += bits.OnesCount64(word)
			}
			counts[w] = (end - start) - losers
		}()
	}
	wg.Wait()

	// префикс-сумма → оффсеты записи
	offsets := make([]int, workers+1)
	for i := 0; i < workers; i++ {
		offsets[i+1] = offsets[i] + counts[i]
	}
	out := make([]User, numWinners)

	// --- PASS 2: параллельная запись победителей ---
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
			// идём блоками по 64, чтобы меньше ветвлений
			i := start
			for i < end {
				wi := i >> 6
				word := bitmap[wi]
				limit := (wi + 1) << 6
				if limit > end {
					limit = end
				}
				// быстрый путь: если слово пустое (нет проигравших), копируем блоком
				if word == 0 && (limit-i) == 64 || (word == 0 && limit-i < 64) {
					// копируем подряд всех 0-битов (т.е. всех победителей в слове)
					// NB: для «частичного» последнего слова тоже сработает
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
