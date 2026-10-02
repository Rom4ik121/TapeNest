// Package algo holds the offline model builders run by the worker's training job
// (and by cmd/reco-eval): item-item co-occurrence, implicit ALS and audio-content
// neighbours. Everything is deterministic for a given seed.
package algo

import (
	"math"
	"math/rand/v2"
	"sort"

	"gonum.org/v1/gonum/mat"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
)

// Interaction is one positive implicit signal (decayed affinity > 0).
type Interaction struct {
	User, Item int
	Value      float64
}

// CoOccurrence computes item-item cosine similarity over the users who liked /
// completed both items, shrunk towards 0 for small supports (sim·co/(co+shrink)),
// keeping topK neighbours per item.
func CoOccurrence(nItems int, inter []Interaction, topK int, shrink float64) [][]model.Neighbor {
	byUser := map[int][]int{}
	count := make([]float64, nItems)
	for _, x := range inter {
		if x.Value <= 0 || x.Item < 0 || x.Item >= nItems {
			continue
		}
		byUser[x.User] = append(byUser[x.User], x.Item)
		count[x.Item]++
	}
	co := make([]map[int]float64, nItems)
	for _, items := range byUser {
		items = dedupe(items)
		if len(items) > 500 { // bound the quadratic step for heavy users
			items = items[:500]
		}
		for a := 0; a < len(items); a++ {
			for b := a + 1; b < len(items); b++ {
				i, j := items[a], items[b]
				if co[i] == nil {
					co[i] = map[int]float64{}
				}
				if co[j] == nil {
					co[j] = map[int]float64{}
				}
				co[i][j]++
				co[j][i]++
			}
		}
	}
	out := make([][]model.Neighbor, nItems)
	for i := range co {
		var ns []model.Neighbor
		for j, c := range co[i] {
			sim := c / math.Sqrt(count[i]*count[j]) * c / (c + shrink)
			ns = append(ns, model.Neighbor{Idx: int32(j), Sim: float32(sim)}) //nolint:gosec // j < nItems
		}
		out[i] = top(ns, topK)
	}
	return out
}

func dedupe(v []int) []int {
	sort.Ints(v)
	out := v[:0]
	for i, x := range v {
		if i == 0 || x != v[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func top(ns []model.Neighbor, k int) []model.Neighbor {
	sort.Slice(ns, func(a, b int) bool {
		if ns[a].Sim != ns[b].Sim {
			return ns[a].Sim > ns[b].Sim
		}
		return ns[a].Idx < ns[b].Idx
	})
	if len(ns) > k {
		ns = ns[:k]
	}
	return ns
}

// ALSParams tune implicit ALS (Hu, Koren, Volinsky 2008).
type ALSParams struct {
	Factors    int
	Iterations int
	Lambda     float64
	Alpha      float64 // confidence = 1 + Alpha·value
	Seed       uint64
}

// DefaultALS is used by the worker.
var DefaultALS = ALSParams{Factors: 16, Iterations: 12, Lambda: 0.1, Alpha: 8, Seed: 42}

// ALS factorizes the implicit feedback matrix; returns user and item factors.
// Rows of users/items without any interaction are nil.
func ALS(nUsers, nItems int, inter []Interaction, p ALSParams) (users, items [][]float32) {
	k := p.Factors
	byUser := make([][]Interaction, nUsers)
	byItem := make([][]Interaction, nItems)
	for _, x := range inter {
		if x.Value <= 0 || x.User < 0 || x.User >= nUsers || x.Item < 0 || x.Item >= nItems {
			continue
		}
		byUser[x.User] = append(byUser[x.User], x)
		byItem[x.Item] = append(byItem[x.Item], x)
	}
	rnd := rand.New(rand.NewPCG(p.Seed, p.Seed^0x9e3779b97f4a7c15)) //nolint:gosec // model init, not security
	U := mat.NewDense(max(nUsers, 1), k, nil)
	V := mat.NewDense(max(nItems, 1), k, nil)
	for i := 0; i < nItems; i++ {
		for f := 0; f < k; f++ {
			V.Set(i, f, (rnd.Float64()-0.5)*0.1)
		}
	}
	for it := 0; it < p.Iterations; it++ {
		solveSide(U, V, byUser, func(x Interaction) int { return x.Item }, p)
		solveSide(V, U, byItem, func(x Interaction) int { return x.User }, p)
	}
	users = toRows(U, nUsers, func(i int) bool { return len(byUser[i]) > 0 })
	items = toRows(V, nItems, func(i int) bool { return len(byItem[i]) > 0 })
	return users, items
}

// solveSide recomputes every row of X given the fixed factors Y.
func solveSide(X, Y *mat.Dense, rows [][]Interaction, other func(Interaction) int, p ALSParams) {
	k := p.Factors
	var YtY mat.SymDense
	YtY.SymOuterK(1, Y.T())
	A := mat.NewSymDense(k, nil)
	b := mat.NewVecDense(k, nil)
	x := mat.NewVecDense(k, nil)
	var chol mat.Cholesky
	for r, obs := range rows {
		if len(obs) == 0 {
			for f := 0; f < k; f++ {
				X.Set(r, f, 0)
			}
			continue
		}
		A.CopySym(&YtY)
		b.Zero()
		for _, o := range obs {
			c := 1 + p.Alpha*o.Value
			y := Y.RowView(other(o))
			A.SymRankOne(A, c-1, y)
			b.AddScaledVec(b, c, y)
		}
		for f := 0; f < k; f++ {
			A.SetSym(f, f, A.At(f, f)+p.Lambda*float64(len(obs)))
		}
		if ok := chol.Factorize(A); !ok {
			continue
		}
		if err := chol.SolveVecTo(x, b); err != nil {
			continue
		}
		for f := 0; f < k; f++ {
			X.Set(r, f, x.AtVec(f))
		}
	}
}

func toRows(M *mat.Dense, n int, keep func(int) bool) [][]float32 {
	_, k := M.Dims()
	out := make([][]float32, n)
	for i := 0; i < n; i++ {
		if !keep(i) {
			continue
		}
		row := make([]float32, k)
		for f := 0; f < k; f++ {
			row[f] = float32(M.At(i, f))
		}
		out[i] = row
	}
	return out
}

// ContentNeighbors returns the topK most similar tracks by embedding cosine
// (tracks without embeddings get none). O(n²·d); fine up to ~20k tracks per run,
// beyond that swap in an ANN index (documented in ADR 0010).
func ContentNeighbors(tracks []model.Track, topK int) [][]model.Neighbor {
	out := make([][]model.Neighbor, len(tracks))
	for i := range tracks {
		if tracks[i].Emb == nil {
			continue
		}
		ns := make([]model.Neighbor, 0, len(tracks))
		for j := range tracks {
			if i == j || tracks[j].Emb == nil {
				continue
			}
			ns = append(ns, model.Neighbor{Idx: int32(j), Sim: float32(model.Dot(tracks[i].Emb, tracks[j].Emb))}) //nolint:gosec // j < len
		}
		out[i] = top(ns, topK)
	}
	return out
}
