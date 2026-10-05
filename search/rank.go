package search

// Hit identifies one search result: the durable content key and the account it
// belongs to. A copy in two folders is one hit (N8).
type Hit struct {
	AccountID  string
	ContentKey string
}

// DefaultRRFK is the reciprocal-rank-fusion constant. 60 is the value from the
// original paper and is insensitive to the exact number of results.
const DefaultRRFK = 60

// Fuse merges ranked lists with reciprocal rank fusion (ARCHITECTURE.md 6):
// each list contributes 1/(k+rank) per hit, rank starting at 1, and the fused
// order is by descending score. A key in both lists gains from both, which is
// what makes a hybrid result better than either list alone. Ties keep the order
// in which a key was first seen, so the result is deterministic.
func Fuse(lists [][]Hit, k float64, limit int) []Hit {
	if k <= 0 {
		k = DefaultRRFK
	}
	type scored struct {
		hit   Hit
		score float64
		order int
	}
	index := make(map[string]int)
	var all []scored
	for _, list := range lists {
		for rank, h := range list {
			key := h.AccountID + "\x00" + h.ContentKey
			score := 1.0 / (k + float64(rank+1))
			if i, ok := index[key]; ok {
				all[i].score += score
				continue
			}
			index[key] = len(all)
			all = append(all, scored{hit: h, score: score, order: len(all)})
		}
	}
	// Insertion sort by score desc, then first-seen order: the result set is
	// bounded by the caller's limit, so this is small.
	for i := 1; i < len(all); i++ {
		for j := i; j > 0; j-- {
			if all[j].score > all[j-1].score {
				all[j], all[j-1] = all[j-1], all[j]
				continue
			}
			break
		}
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]Hit, len(all))
	for i, s := range all {
		out[i] = s.hit
	}
	return out
}
