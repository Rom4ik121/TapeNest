package eval

import (
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/tapenest/tapenest/services/reco-service/internal/algo"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
	"github.com/tapenest/tapenest/services/reco-service/internal/profile"
	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
)

// Trained is everything the recommenders need, built from the training split.
type Trained struct {
	Model   *model.Model
	Users   []*rank.User
	Likes   []map[int]bool
	Plays   []float64
	LikeCnt []float64
	Now     time.Time
}

// Train builds profiles and models exactly as the worker does.
func Train(w *World, train []Event, cutoff time.Time) *Trained {
	m := model.New(w.Catalog(train, cutoff))
	tr := &Trained{Model: m, Now: cutoff, Plays: make([]float64, len(m.Tracks)), LikeCnt: make([]float64, len(m.Tracks))}
	states := make([]map[int]*profile.TrackState, len(w.Users))
	tastes := make([]*profile.Taste, len(w.Users))
	tr.Likes = make([]map[int]bool, len(w.Users))
	for u := range w.Users {
		states[u] = map[int]*profile.TrackState{}
		tastes[u] = profile.NewTaste()
		tr.Likes[u] = map[int]bool{}
	}
	for _, e := range train {
		st := states[e.User][e.Track]
		if st == nil {
			st = &profile.TrackState{}
			states[e.User][e.Track] = st
		}
		d := st.Apply(profile.Event{Kind: e.Kind, PositionSec: e.Position, Completed: e.Completed, At: e.At})
		t := &m.Tracks[e.Track]
		tastes[e.User].Add(profile.KeysFor(t.ArtistID.String(), t.AlbumID.String(), t.Genre, t.Tags), d, e.At)
		switch e.Kind {
		case profile.KindPlay:
			tr.Plays[e.Track]++
		case profile.KindLike:
			tr.LikeCnt[e.Track]++
			tr.Likes[e.User][e.Track] = true
		}
	}
	var inter []algo.Interaction
	tr.Users = make([]*rank.User, len(w.Users))
	for u := range w.Users {
		ru := &rank.User{Taste: tastes[u].Snapshot(cutoff), Tracks: map[int]rank.UserTrack{}}
		for i, st := range states[u] {
			aff := profile.Decay(st.Affinity, st.At, cutoff)
			ru.Tracks[i] = rank.UserTrack{
				Affinity: aff, Plays: st.Plays, Completes: st.Completes, EarlySkips: st.EarlySkips,
				Skips: st.Skips, Liked: st.Liked, LastPlayed: st.LastPlayed,
			}
			if aff > 0 {
				inter = append(inter, algo.Interaction{User: u, Item: i, Value: aff})
			}
		}
		tr.Users[u] = ru
	}
	m.CF = algo.CoOccurrence(len(m.Tracks), inter, 20, 2)
	uf, itf := algo.ALS(len(w.Users), len(m.Tracks), inter, algo.DefaultALS)
	m.ItemFactors = itf
	for u := range tr.Users {
		tr.Users[u].Factors = uf[u]
	}
	m.Content = algo.ContentNeighbors(m.Tracks, 20)
	return tr
}

// Recommender returns top-k track indices for a user given an exclusion set.
type Recommender struct {
	Name string
	Rec  func(tr *Trained, u int, s *rank.Session, k int, rnd *rand.Rand) []int
	// OnFeedback lets session-aware heuristics learn (nil: feedback goes via Session).
}

// Heuristic reimplements music-service's ADR 0009 §6 wave score.
func Heuristic() Recommender {
	return Recommender{Name: "heuristic (ADR 0009 §6)", Rec: func(tr *Trained, u int, s *rank.Session, k int, rnd *rand.Rand) []int {
		m := tr.Model
		likedArtist := map[[16]byte]bool{}
		for i := range tr.Likes[u] {
			likedArtist[m.Tracks[i].ArtistID] = true
		}
		la, sk := map[[16]byte]int{}, map[[16]byte]int{}
		for _, fb := range s.Feedback {
			if fb.Action == "like" {
				la[m.Tracks[fb.Track].ArtistID]++
			} else {
				sk[m.Tracks[fb.Track].ArtistID]++
			}
		}
		type sc struct {
			i int
			v float64
		}
		var pool []sc
		for i := range m.Tracks {
			if s.Exclude[i] {
				continue
			}
			a := m.Tracks[i].ArtistID
			v := math.Log1p(tr.Plays[i] + 3*tr.LikeCnt[i])
			if tr.Likes[u][i] {
				v += 1.5
			}
			if likedArtist[a] {
				v += 0.7
			}
			v += float64(la[a]) - 1.5*float64(sk[a])
			if ut, ok := tr.Users[u].Tracks[i]; ok && !ut.LastPlayed.IsZero() {
				switch ago := tr.Now.Sub(ut.LastPlayed); {
				case ago < 2*time.Hour:
					v -= 3
				case ago < 24*time.Hour:
					v--
				}
			}
			pool = append(pool, sc{i, v + rnd.Float64()})
		}
		sort.SliceStable(pool, func(a, b int) bool { return pool[a].v > pool[b].v })
		var out []int
		used := map[int]bool{}
		for len(out) < k && len(out) < len(pool) {
			pick := -1
			for j, p := range pool {
				if used[j] {
					continue
				}
				if pick < 0 {
					pick = j
				}
				if len(out) == 0 || m.Tracks[p.i].ArtistID != m.Tracks[out[len(out)-1]].ArtistID {
					pick = j
					break
				}
			}
			used[pick] = true
			out = append(out, pool[pick].i)
		}
		return out
	}}
}

// Popularity is the non-personalized baseline.
func Popularity() Recommender {
	return Recommender{Name: "popularity only", Rec: func(tr *Trained, _ int, s *rank.Session, k int, _ *rand.Rand) []int {
		idx := make([]int, 0, len(tr.Model.Tracks))
		for i := range tr.Model.Tracks {
			if !s.Exclude[i] {
				idx = append(idx, i)
			}
		}
		sort.SliceStable(idx, func(a, b int) bool { return tr.Plays[idx[a]] > tr.Plays[idx[b]] })
		if len(idx) > k {
			idx = idx[:k]
		}
		return idx
	}}
}

// Reco wraps rank.Rank with the given weights.
func Reco(name string, w rank.Weights) Recommender {
	return Recommender{Name: name, Rec: func(tr *Trained, u int, s *rank.Session, k int, rnd *rand.Rand) []int {
		picks := rank.Rank(tr.Model, tr.Users[u], s, w, k, rnd, tr.Now)
		out := make([]int, len(picks))
		for i, p := range picks {
			out[i] = p.Track
		}
		return out
	}}
}

// Suite is the default comparison set (full blend + ablations).
func Suite() []Recommender {
	full := rank.DefaultWeights()
	abl := func(name string, f func(*rank.Weights)) Recommender {
		w := full
		f(&w)
		return Reco(name, w)
	}
	return []Recommender{
		Heuristic(),
		Popularity(),
		Reco("reco: full blend", full),
		abl("reco − CF (co-occurrence)", func(w *rank.Weights) { w.CF = 0 }),
		abl("reco − ALS", func(w *rank.Weights) { w.ALS = 0 }),
		abl("reco − audio content", func(w *rank.Weights) { w.Content = 0 }),
		abl("reco − taste profile", func(w *rank.Weights) { w.Profile = 0 }),
		abl("reco − MMR/diversity", func(w *rank.Weights) { w.MMRLambda = 1; w.MaxPerArtist = 100 }),
		abl("reco − exploration", func(w *rank.Weights) { w.Epsilon = 0; w.Thompson = false }),
	}
}

// Row is one offline metrics line.
type Row struct {
	Name                              string
	Precision, Recall, NDCG, HitRate  float64
	Coverage, ILD, ArtistDiv, Novelty float64
	ColdRecall                        float64
}

// SessRow is one closed-loop session line.
type SessRow struct {
	Name                         string
	LikeRate, SkipRate, MeanUtil float64
	ArtistRepeats, RepeatsInSess float64
}

// Report is the full evaluation output.
type Report struct {
	Cfg      Config
	Offline  []Row
	Sessions []SessRow
	Stats    map[string]int
	Seeds    int
}

// Run executes the whole evaluation.
func Run(cfg Config, recs []Recommender) Report {
	w := NewWorld(cfg)
	sp := w.Holdout()
	tr := Train(w, sp.Train, sp.Cutoff)
	rep := Report{Cfg: cfg, Stats: map[string]int{"events": len(w.Events), "train": len(sp.Train), "testUsers": len(sp.Test)}}
	newCnt := 0
	for _, t := range w.Tracks {
		if t.New {
			newCnt++
		}
	}
	rep.Stats["newTracks"] = newCnt
	for _, r := range recs {
		rep.Offline = append(rep.Offline, offline(w, tr, sp, r, cfg.K))
		rep.Sessions = append(rep.Sessions, sessions(w, tr, r, cfg))
	}
	return rep
}

func offline(w *World, tr *Trained, sp Split, r Recommender, k int) Row {
	rnd := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // simulation
	row := Row{Name: r.Name}
	covered := map[int]bool{}
	users := 0
	var coldHit, coldTot float64
	logN := math.Log2(float64(len(tr.Model.Tracks)))
	for u := range w.Users {
		test := sp.Test[u]
		if len(test) == 0 {
			continue
		}
		users++
		ex := map[int]bool{}
		for i := range tr.Users[u].Tracks {
			ex[i] = true
		}
		recs := r.Rec(tr, u, &rank.Session{Exclude: ex}, k, rnd)
		hits, dcg, idcg := 0.0, 0.0, 0.0
		for pos, i := range recs {
			covered[i] = true
			if test[i] {
				hits++
				dcg += 1 / math.Log2(float64(pos)+2)
			}
		}
		for pos := 0; pos < min(len(test), k); pos++ {
			idcg += 1 / math.Log2(float64(pos)+2)
		}
		row.Precision += hits / float64(k)
		row.Recall += hits / float64(len(test))
		row.NDCG += dcg / idcg
		if hits > 0 {
			row.HitRate++
		}
		row.ILD += ild(tr.Model, recs)
		row.ArtistDiv += artistDiv(tr.Model, recs)
		for _, i := range recs {
			row.Novelty += (logN - math.Log2(1+tr.Plays[i])) / float64(len(recs))
		}
		for i := range test {
			if w.Tracks[i].New {
				coldTot++
				for _, x := range recs {
					if x == i {
						coldHit++
					}
				}
			}
		}
	}
	n := float64(max(users, 1))
	row.Precision /= n
	row.Recall /= n
	row.NDCG /= n
	row.HitRate /= n
	row.ILD /= n
	row.ArtistDiv /= n
	row.Novelty /= n
	row.Coverage = float64(len(covered)) / float64(len(tr.Model.Tracks))
	if coldTot > 0 {
		row.ColdRecall = coldHit / coldTot
	}
	return row
}

// sessions runs a closed-loop wave: 3 batches of 10 with like/skip feedback.
func sessions(w *World, tr *Trained, r Recommender, cfg Config) SessRow {
	rnd := rand.New(rand.NewPCG(3, 4))   //nolint:gosec // simulation
	react := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // simulation
	row := SessRow{Name: r.Name}
	var served, likes, skips float64
	users := min(len(w.Users), 150)
	for u := 0; u < users; u++ {
		s := &rank.Session{Exclude: map[int]bool{}}
		var order []int
		for b := 0; b < 3; b++ {
			recs := r.Rec(tr, u, s, cfg.K, rnd)
			for _, i := range recs {
				if s.Exclude[i] {
					row.RepeatsInSess++
				}
				if len(order) > 0 && tr.Model.Tracks[order[len(order)-1]].ArtistID == tr.Model.Tracks[i].ArtistID {
					row.ArtistRepeats++
				}
				order = append(order, i)
				s.Exclude[i] = true
				s.Recent = append(s.Recent, i)
				ut := w.Utility(u, i)
				row.MeanUtil += ut
				served++
				th := w.Users[u].Threshold
				if react.Float64() < sigmoid(3*(ut-th)) {
					if react.Float64() < sigmoid(4*(ut-th-0.7)) {
						likes++
						s.Feedback = append(s.Feedback, rank.Feedback{Track: i, Action: "like"})
					}
				} else {
					skips++
					s.Feedback = append(s.Feedback, rank.Feedback{Track: i, Action: "skip"})
				}
			}
		}
	}
	row.LikeRate = likes / served
	row.SkipRate = skips / served
	row.MeanUtil /= served
	row.ArtistRepeats /= served
	row.RepeatsInSess /= served
	return row
}

func ild(m *model.Model, recs []int) float64 {
	if len(recs) < 2 {
		return 0
	}
	var s, n float64
	for a := 0; a < len(recs); a++ {
		for b := a + 1; b < len(recs); b++ {
			s += 1 - model.Dot(m.Tracks[recs[a]].Emb, m.Tracks[recs[b]].Emb)
			n++
		}
	}
	return s / n
}

func artistDiv(m *model.Model, recs []int) float64 {
	if len(recs) == 0 {
		return 0
	}
	set := map[[16]byte]bool{}
	for _, i := range recs {
		set[m.Tracks[i].ArtistID] = true
	}
	return float64(len(set)) / float64(len(recs))
}

// Markdown renders the report tables.
func (r Report) Markdown() string {
	var b strings.Builder
	if r.Seeds > 1 {
		fmt.Fprintf(&b, "Mean over %d synthetic worlds (seeds %d…%d); per-world averages below.\n\n", r.Seeds, r.Cfg.Seed, r.Cfg.Seed+uint64(r.Seeds)-1) //nolint:gosec // small
	}
	fmt.Fprintf(&b, "Synthetic world: %d users, %d tracks (%d new, cold), %d artists, %d genres; %d events (%d train), %d test users, K=%d, seed=%d.\n\n",
		r.Cfg.Users, r.Cfg.Tracks, r.Stats["newTracks"], r.Cfg.Artists, r.Cfg.Genres, r.Stats["events"], r.Stats["train"], r.Stats["testUsers"], r.Cfg.K, r.Cfg.Seed)
	b.WriteString("### Offline (time-split holdout, unseen items)\n\n")
	b.WriteString("| Recommender | P@10 | R@10 | NDCG@10 | HitRate@10 | Coverage | ILD (audio) | Artist diversity | Novelty (bits) | Cold-start recall |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, x := range r.Offline {
		fmt.Fprintf(&b, "| %s | %.4f | %.4f | %.4f | %.3f | %.3f | %.3f | %.3f | %.2f | %.3f |\n",
			x.Name, x.Precision, x.Recall, x.NDCG, x.HitRate, x.Coverage, x.ILD, x.ArtistDiv, x.Novelty, x.ColdRecall)
	}
	b.WriteString("\n### Closed-loop wave sessions (3 batches × 10, simulated like/skip)\n\n")
	b.WriteString("| Recommender | Like rate | Skip rate | Mean true utility | Same artist back-to-back | Repeats in session |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, x := range r.Sessions {
		fmt.Fprintf(&b, "| %s | %.3f | %.3f | %.3f | %.3f | %.3f |\n", x.Name, x.LikeRate, x.SkipRate, x.MeanUtil, x.ArtistRepeats, x.RepeatsInSess)
	}
	return b.String()
}

// RunSeeds averages Run over n consecutive seeds (variance between synthetic
// worlds is large, so the report uses the mean).
func RunSeeds(cfg Config, n int, recs []Recommender) Report {
	var acc Report
	for s := 0; s < max(n, 1); s++ {
		c := cfg
		c.Seed = cfg.Seed + uint64(s) //nolint:gosec // s ≥ 0
		r := Run(c, recs)
		if s == 0 {
			acc = r
			continue
		}
		for i := range acc.Offline {
			a, b := &acc.Offline[i], r.Offline[i]
			a.Precision += b.Precision
			a.Recall += b.Recall
			a.NDCG += b.NDCG
			a.HitRate += b.HitRate
			a.Coverage += b.Coverage
			a.ILD += b.ILD
			a.ArtistDiv += b.ArtistDiv
			a.Novelty += b.Novelty
			a.ColdRecall += b.ColdRecall
		}
		for i := range acc.Sessions {
			a, b := &acc.Sessions[i], r.Sessions[i]
			a.LikeRate += b.LikeRate
			a.SkipRate += b.SkipRate
			a.MeanUtil += b.MeanUtil
			a.ArtistRepeats += b.ArtistRepeats
			a.RepeatsInSess += b.RepeatsInSess
		}
		for k, v := range r.Stats {
			acc.Stats[k] += v
		}
	}
	f := float64(max(n, 1))
	for i := range acc.Offline {
		a := &acc.Offline[i]
		a.Precision /= f
		a.Recall /= f
		a.NDCG /= f
		a.HitRate /= f
		a.Coverage /= f
		a.ILD /= f
		a.ArtistDiv /= f
		a.Novelty /= f
		a.ColdRecall /= f
	}
	for i := range acc.Sessions {
		a := &acc.Sessions[i]
		a.LikeRate /= f
		a.SkipRate /= f
		a.MeanUtil /= f
		a.ArtistRepeats /= f
		a.RepeatsInSess /= f
	}
	for k := range acc.Stats {
		acc.Stats[k] /= max(n, 1)
	}
	acc.Seeds = max(n, 1)
	return acc
}
