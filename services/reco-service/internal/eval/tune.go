package eval

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"

	"github.com/tapenest/tapenest/services/reco-service/internal/rank"
)

// Trial is one random-search sample.
type Trial struct {
	W         rank.Weights
	Objective float64
	Off       Row
	Sess      SessRow
}

// Objective balances accuracy, cold start and the closed-loop session outcome.
func Objective(off Row, s SessRow) float64 {
	return off.NDCG + 0.1*off.ColdRecall + 0.05*off.Coverage + 0.2*s.LikeRate - 0.2*s.SkipRate
}

// Tune runs a random search over the blend weights on a validation world
// (a different seed than the reported one, to avoid tuning on the test).
func Tune(cfg Config, trials int, seed uint64) []Trial {
	w := NewWorld(cfg)
	sp := w.Holdout()
	tr := Train(w, sp.Train, sp.Cutoff)
	rnd := rand.New(rand.NewPCG(seed, 17)) //nolint:gosec // search
	pick := func(v ...float64) float64 { return v[rnd.IntN(len(v))] }
	out := make([]Trial, 0, trials+1)
	eval := func(wt rank.Weights) {
		r := Reco("trial", wt)
		off := offline(w, tr, sp, r, cfg.K)
		ss := sessions(w, tr, r, cfg)
		out = append(out, Trial{W: wt, Objective: Objective(off, ss), Off: off, Sess: ss})
	}
	eval(rank.DefaultWeights())
	for t := 0; t < trials; t++ {
		wt := rank.DefaultWeights()
		wt.Profile = pick(0.5, 1, 1.5)
		wt.CF = pick(0, 0.3, 0.6, 1)
		wt.ALS = pick(0.4, 0.8, 1.2)
		wt.Content = pick(0.3, 0.6, 0.9)
		wt.Popular = pick(0.2, 0.35, 0.6)
		wt.Fresh = pick(0.1, 0.3, 0.5)
		wt.Session = pick(0.8, 1.2, 1.6)
		wt.Novelty = pick(0, 0.1, 0.2)
		wt.MMRLambda = pick(0.65, 0.75, 0.85)
		wt.ColdContent = pick(0.2, 0.35, 0.5)
		wt.Epsilon = pick(0.05, 0.1)
		eval(wt)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Objective > out[b].Objective })
	return out
}

// TuneMarkdown renders the best trials.
func TuneMarkdown(ts []Trial, n int) string {
	var b strings.Builder
	b.WriteString("| # | objective | NDCG@10 | cold R | like | skip | profile | cf | als | content | popular | fresh | session | novelty | mmrλ | coldContent | ε |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for i, t := range ts {
		if i >= n {
			break
		}
		w := t.W
		fmt.Fprintf(&b, "| %d | %.4f | %.4f | %.3f | %.3f | %.3f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f |\n",
			i+1, t.Objective, t.Off.NDCG, t.Off.ColdRecall, t.Sess.LikeRate, t.Sess.SkipRate, w.Profile, w.CF, w.ALS, w.Content,
			w.Popular, w.Fresh, w.Session, w.Novelty, w.MMRLambda, w.ColdContent, w.Epsilon)
	}
	return b.String()
}
