package src

import "slices"

// 目录的比率仅含素因子 2、3、5、7，且 7 的指数总是 5 的指数的 −2 倍。
// 用前三个指数做精确搜索，不用浮点采样率作图节点，也不限制中间率为整数。
type exponents [3]int

func ratioExponents(l, m int) (exponents, bool) {
	var result exponents
	seven := 0
	for i, prime := range []int{2, 3, 5, 7} {
		count := 0
		for l%prime == 0 {
			l /= prime
			count++
		}
		for m%prime == 0 {
			m /= prime
			count--
		}
		if i == 3 {
			seven = count
		} else {
			result[i] = count
		}
	}
	return result, l == 1 && m == 1 && seven == -2*result[2]
}

func (a exponents) sub(b exponents) exponents {
	return exponents{a[0] - b[0], a[1] - b[1], a[2] - b[2]}
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

type stageFactor struct {
	ratio
	powers    exponents
	powerCost int
}

func stageFactors(q Quality) []stageFactor {
	var result []stageFactor
	// 所有升频因子在前、降频因子在后；搜索只遍历非递减下标，避免排列重复。
	for _, reverse := range []bool{false, true} {
		for _, r := range catalog {
			if !catalogRatio(r.l, r.m, q) {
				continue
			}
			if reverse {
				r.l, r.m = r.m, r.l
			}
			v, _ := ratioExponents(r.l, r.m)
			cost := 0
			if v[1] == 0 && v[2] == 0 {
				cost = abs(v[0])
			}
			result = append(result, stageFactor{r, v, cost})
		}
	}
	// 未被合同区分的同成本解，稳定地先选较小的升频因子（如 3/2 而非 3）。
	slices.SortStableFunc(result, func(a, b stageFactor) int {
		upA, upB := a.l > a.m, b.l > b.m
		if upA != upB {
			if upA {
				return -1
			}
			return 1
		}
		return a.l*b.m - b.l*a.m
	})
	return result
}

func catalogStages(from, to int, phase Phase, q Quality) ([]Stage, error) {
	d := gcd(from, to)
	l, m := to/d, from/d
	if l == m {
		return nil, nil
	}
	if catalogRatio(l, m, q) {
		return []Stage{{phase, q, l, m}}, nil
	}
	target, ok := ratioExponents(l, m)
	if !ok || (q == Stage2 && (target[1] != 0 || target[2] != 0)) {
		return nil, ErrUnsupportedRatio
	}
	factors := stageFactors(q)
	makeStages := func(indices []int) []Stage {
		result := make([]Stage, 0, len(indices))
		for _, i := range indices {
			r := factors[i].ratio
			result = append(result, Stage{phase, q, r.l, r.m})
		}
		return result
	}
	// 先保留原设计的“一个跨族核 + 一级 2 的幂”规则，并选最小的幂部分。
	bestPair, bestPower := []int(nil), int(^uint(0)>>1)
	for i, a := range factors {
		if a.powers[2] == 0 {
			continue
		}
		for j, b := range factors {
			if b.powerCost == 0 || target.sub(a.powers) != b.powers || b.powerCost >= bestPower {
				continue
			}
			bestPower = b.powerCost
			if i < j {
				bestPair = []int{i, j}
			} else {
				bestPair = []int{j, i}
			}
		}
	}
	if bestPair != nil {
		return makeStages(bestPair), nil
	}

	// 其余比率按用户裁定扩展：目录核乘积、级数最少，同级数时幂部分最小。
	// 以 160/147、3、2 为生成元可直接构造一个解，因此上界不靠任意搜索深度。
	upper := abs(target[2]) + abs(target[1]+target[2]) + (abs(target[0]-5*target[2])+5)/6
	lower := max((abs(target[0])+7)/8, abs(target[1]), abs(target[2]))
	for depth := max(1, lower); depth <= upper; depth++ {
		if indices := shortestProduct(factors, target, depth); indices != nil {
			return makeStages(indices), nil
		}
	}
	return nil, ErrUnsupportedRatio
}

func shortestProduct(factors []stageFactor, target exponents, depth int) []int {
	// 同一剩余问题只保留较小的幂代价；纯整数状态使选择与平台无关。
	type state struct {
		left, start int
		remainder   exponents
	}
	seen := make(map[state]int)
	bestCost := int(^uint(0) >> 1)
	var best, path []int
	var search func(exponents, int, int, int)
	search = func(rem exponents, left, start, cost int) {
		if left == 0 {
			if rem == (exponents{}) && cost < bestCost {
				best = append([]int(nil), path...)
				bestCost = cost
			}
			return
		}
		if cost >= bestCost || abs(rem[0]) > 8*left || abs(rem[1]) > left || abs(rem[2]) > left {
			return
		}
		key := state{left, start, rem}
		if old, ok := seen[key]; ok && old <= cost {
			return
		}
		seen[key] = cost
		for i := start; i < len(factors); i++ {
			f := factors[i]
			path = append(path, i)
			search(rem.sub(f.powers), left-1, i, cost+f.powerCost)
			path = path[:len(path)-1]
		}
	}
	search(target, depth, 0, 0)
	return best
}
