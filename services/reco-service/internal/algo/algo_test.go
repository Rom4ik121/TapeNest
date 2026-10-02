package algo

import (
	"testing"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/model"
)

// two communities: users 0-9 like items 0-4, users 10-19 like items 5-9.
func blocks() []Interaction {
	var out []Interaction
	for u := 0; u < 20; u++ {
		base := 0
		if u >= 10 {
			base = 5
		}
		for k := 0; k < 4; k++ {
			out = append(out, Interaction{User: u, Item: base + (u+k)%5, Value: 1 + float64(k%2)})
		}
	}
	return out
}

func TestCoOccurrence(t *testing.T) {
	nb := CoOccurrence(10, append(blocks(), Interaction{User: 0, Item: 99, Value: 1}, Interaction{User: 0, Item: 1, Value: -1}), 3, 2)
	for i := 0; i < 10; i++ {
		if len(nb[i]) == 0 || len(nb[i]) > 3 {
			t.Fatalf("item %d neighbours %v", i, nb[i])
		}
		for _, n := range nb[i] {
			if (i < 5) != (n.Idx < 5) {
				t.Fatalf("item %d has cross-community neighbour %d", i, n.Idx)
			}
			if n.Sim <= 0 || n.Sim > 1 {
				t.Fatalf("sim %v", n.Sim)
			}
		}
	}
}

func TestALS(t *testing.T) {
	p := DefaultALS
	p.Iterations = 8
	users, items := ALS(21, 11, blocks(), p)
	if users[20] != nil || items[10] != nil {
		t.Fatal("rows without data must be nil")
	}
	dot := func(a, b []float32) float64 {
		var s float64
		for i := range a {
			s += float64(a[i] * b[i])
		}
		return s
	}
	// user 0 (community A) must score unseen A items above B items
	var a, b float64
	for i := 0; i < 5; i++ {
		a += dot(users[0], items[i])
		b += dot(users[0], items[5+i])
	}
	if a <= b {
		t.Fatalf("ALS failed to separate communities: A %.3f B %.3f", a, b)
	}
}

func TestContentNeighbors(t *testing.T) {
	e := func(x, y float32) []float32 { return model.Normalize([]float32{x, y}) }
	ts := []model.Track{{ID: uuid.New(), Emb: e(1, 0)}, {ID: uuid.New(), Emb: e(0.9, 0.1)}, {ID: uuid.New(), Emb: e(0, 1)}, {ID: uuid.New()}}
	nb := ContentNeighbors(ts, 1)
	if len(nb[0]) != 1 || nb[0][0].Idx != 1 || nb[2][0].Idx != 1 || nb[3] != nil {
		t.Fatalf("neighbours %v", nb)
	}
}
