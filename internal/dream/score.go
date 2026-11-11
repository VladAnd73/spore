package dream

import "sort"

// FlagDeepReads scores every digest and marks at most cap of them for a
// full read. The cap lives here, in Go, so the worker cannot widen it.
func FlagDeepReads(ds []SessionDigest, cap int) {
	for i := range ds {
		ds[i].Score = score(ds[i])
		ds[i].DeepRead = false
	}
	if cap <= 0 {
		return
	}
	order := make([]int, len(ds))
	for i := range order {
		order[i] = i
	}
	// Sorted by index rather than by reordering ds, because callers hold
	// the slice and the digest order is the report order.
	sort.Slice(order, func(a, b int) bool {
		ia, ib := ds[order[a]], ds[order[b]]
		if ia.Score != ib.Score {
			return ia.Score > ib.Score
		}
		// Scores are small integers over hundreds of sessions, so ties
		// are the common case. Slug then path is a total order, which
		// keeps the chosen set stable across runs.
		if ia.Session.Slug != ib.Session.Slug {
			return ia.Session.Slug < ib.Session.Slug
		}
		return ia.Session.Path < ib.Session.Path
	})
	for n, idx := range order {
		if n >= cap || ds[idx].Score == 0 {
			break
		}
		ds[idx].DeepRead = true
	}
}

func score(d SessionDigest) int {
	s := 3*len(d.Failures) + 2*len(d.OperatorMessages) +
		2*len(d.OperatorRefusals) + len(d.RepeatedCommands)
	if d.End == "blocked" || d.End == "tokens" {
		s += 5
	}
	return s
}
