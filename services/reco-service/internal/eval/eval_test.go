package eval

import (
	"strings"
	"testing"
)

func small() Config {
	c := DefaultConfig()
	c.Users, c.Tracks, c.Artists, c.EventsPerUser = 60, 200, 30, 40
	return c
}

func TestRunComparesRecommenders(t *testing.T) {
	rep := RunSeeds(small(), 2, Suite()[:3])
	if len(rep.Offline) != 3 || len(rep.Sessions) != 3 || rep.Seeds != 2 {
		t.Fatalf("report %+v", rep)
	}
	heur, pop, reco := rep.Offline[0], rep.Offline[1], rep.Offline[2]
	if reco.Recall <= pop.Recall || reco.Coverage <= heur.Coverage {
		t.Fatalf("reco should beat popularity recall and heuristic coverage: %+v %+v %+v", heur, pop, reco)
	}
	// the tiny test world is too small for the session gap seen at full size
	// (docs/reco/evaluation.md): require non-inferiority and the hard constraints
	if rep.Sessions[2].SkipRate > rep.Sessions[0].SkipRate+0.02 || rep.Sessions[2].ArtistRepeats != 0 || rep.Sessions[2].RepeatsInSess != 0 {
		t.Fatalf("session metrics %+v vs %+v", rep.Sessions[2], rep.Sessions[0])
	}
	md := rep.Markdown()
	for _, want := range []string{"NDCG@10", "Like rate", "heuristic", "Mean over 2"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown lacks %q", want)
		}
	}
}

func TestTune(t *testing.T) {
	ts := Tune(small(), 1, 3)
	if len(ts) != 2 || ts[0].Objective < ts[1].Objective {
		t.Fatalf("trials %+v", ts)
	}
	if !strings.Contains(TuneMarkdown(ts, 5), "objective") {
		t.Fatal("tune markdown")
	}
}
