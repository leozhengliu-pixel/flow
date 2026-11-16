package meter

import "sort"

func Slice(m *Meter, values any, less func(int, int) bool) {
	m.Reserve(512)
	sort.Slice(values, func(i, j int) bool {
		m.Step()
		return less(i, j)
	})
}

func SliceStable(m *Meter, values any, less func(int, int) bool) {
	m.Reserve(512)
	sort.SliceStable(values, func(i, j int) bool {
		m.Step()
		return less(i, j)
	})
}

func Strings(m *Meter, values []string) {
	m.Reserve(512)
	sort.Slice(values, func(i, j int) bool {
		m.Step()
		// Long common prefixes can dominate key sorting work.
		m.Reserve(uint64(min(len(values[i]), len(values[j]))))
		return values[i] < values[j]
	})
}

func Search(m *Meter, length int, test func(int) bool) int {
	return sort.Search(length, func(i int) bool {
		m.Step()
		return test(i)
	})
}
