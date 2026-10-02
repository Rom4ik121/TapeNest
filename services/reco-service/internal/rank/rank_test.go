package rank

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tapenest/tapenest/services/reco-service/internal/audio"
	"github.com/tapenest/tapenest/services/reco-service/internal/model"
)

var now = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// world: 3 genres × 4 artists × 5 tracks; genre g has a distinct sound; energy grows with track number.
func world() *model.Model {
	var ts []model.Track
	for g := 0; g < 3; g++ {
		for a := 0; a < 4; a++ {
			artist := uuid.New()
			for k := 0; k < 5; k++ {
				raw := make([]float32, audio.RawDims)
				for d := range raw {
					if d%3 == g {
						raw[d] = 5
					}
				}
				raw[2] = float32(k) * 0.1 // energy
				raw[0] = 80 + float32(k)*15
				ts = append(ts, model.Track{
					ID: uuid.New(), Title: "t", Artist: "artist", ArtistID: artist, AlbumID: artist,
					Genre: string(rune('A' + g)), Popularity: float64(k) / 10, CreatedAt: now.AddDate(0, -3, 0), Raw: raw,
				})
			}
		}
	}
	return model.New(ts)
}

func likesGenre(m *model.Model, g string, n int) *User {
	u := &User{Taste: map[string]float64{}, Tracks: map[int]UserTrack{}}
	for i, t := range m.Tracks {
		if t.Genre == g && len(u.Tracks) < n {
			u.Tracks[i] = UserTrack{Affinity: 4, Plays: 2, Completes: 2, Liked: true, LastPlayed: now.AddDate(0, 0, -3)}
			u.Taste[TasteKey("genre", g)] += 2
			u.Taste[TasteKey("artist", t.ArtistID.String())] += 2
		}
	}
	return u
}

func rnd() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func TestPersonalizedAndConstraints(t *testing.T) {
	m := world()
	u := likesGenre(m, "B", 4)
	ex := map[int]bool{}
	for i := range u.Tracks {
		ex[i] = true
	}
	picks := Rank(m, u, &Session{Exclude: ex}, DefaultWeights(), 10, rnd(), now)
	if len(picks) != 10 {
		t.Fatalf("got %d picks", len(picks))
	}
	inB, perArtist := 0, map[uuid.UUID]int{}
	for k, p := range picks {
		tr := m.Tracks[p.Track]
		if ex[p.Track] {
			t.Fatal("excluded track served")
		}
		if tr.Genre == "B" {
			inB++
		}
		perArtist[tr.ArtistID]++
		if k > 0 && m.Tracks[picks[k-1].Track].ArtistID == tr.ArtistID {
			t.Fatal("same artist back-to-back")
		}
		if p.Reason.Kind == "" || p.Source == "" {
			t.Fatalf("pick without reason/source: %+v", p)
		}
	}
	if inB < 6 {
		t.Fatalf("only %d/10 picks match the liked genre", inB)
	}
	for _, n := range perArtist {
		if n > 2 {
			t.Fatal("more than 2 tracks of one artist")
		}
	}
}

func TestReasonsReferenceSeeds(t *testing.T) {
	m := world()
	u := likesGenre(m, "A", 2)
	w := DefaultWeights()
	w.Epsilon = 0
	picks := Rank(m, u, &Session{Exclude: map[int]bool{}}, w, 10, rnd(), now)
	found := false
	uses := map[int]int{}
	for _, p := range picks {
		if p.Reason.Kind == ReasonBecauseLiked {
			if uses[p.Reason.Ref]++; uses[p.Reason.Ref] > maxRefUses {
				t.Fatalf("reference %d used more than %d times", p.Reason.Ref, maxRefUses)
			}
			if _, ok := u.Tracks[p.Reason.Ref]; !ok {
				t.Fatalf("reason ref %d is not a liked track", p.Reason.Ref)
			}
			found = true
		}
		if u.Tracks[p.Track].Liked && p.Reason.Kind != ReasonFavorite {
			t.Fatal("liked track should be explained as favorite")
		}
	}
	if !found {
		t.Fatalf("no because_you_liked reason in %+v", picks)
	}
}

func TestColdStartUsesPopularity(t *testing.T) {
	m := world()
	w := DefaultWeights()
	w.Epsilon, w.Jitter = 0, 0
	picks := Rank(m, nil, nil, w, 5, nil, now)
	if len(picks) != 5 {
		t.Fatal("cold start must still fill a batch")
	}
	var pop float64
	for _, p := range picks {
		pop += m.Tracks[p.Track].Popularity
		if p.Reason.Kind != ReasonPopular && p.Reason.Kind != ReasonDiscovery {
			t.Fatalf("cold-start reason %s", p.Reason.Kind)
		}
	}
	if pop/5 < 0.25 {
		t.Fatalf("cold start should favour popular tracks, mean pop %.2f", pop/5)
	}
}

func TestSessionFeedbackShiftsBatch(t *testing.T) {
	m := world()
	w := DefaultWeights()
	w.Epsilon = 0
	liked := 12 // genre A? index 12 is genre A (0..19)
	skipGenre := m.Tracks[45].Genre
	s := &Session{
		Exclude: map[int]bool{liked: true, 45: true}, Recent: []int{liked, 45},
		Feedback: []Feedback{{Track: liked, Action: "like"}, {Track: 45, Action: "skip"}},
	}
	picks := Rank(m, &User{}, s, w, 6, rnd(), now)
	same, skipped := 0, 0
	for _, p := range picks {
		if m.Tracks[p.Track].Genre == m.Tracks[liked].Genre {
			same++
		}
		if m.Tracks[p.Track].Genre == skipGenre {
			skipped++
		}
	}
	if same < 4 || skipped > 1 {
		t.Fatalf("session feedback ignored: liked-genre %d skipped-genre %d", same, skipped)
	}
	if m.Tracks[picks[0].Track].ArtistID == m.Tracks[45].ArtistID {
		t.Fatal("first pick repeats the artist of the last recent track")
	}
}

func TestModes(t *testing.T) {
	m := world()
	w := DefaultWeights()
	w.Epsilon, w.Jitter = 0, 0
	mean := func(mode string) float64 {
		picks := Rank(m, &User{}, &Session{Mode: mode}, w, 8, rnd(), now)
		var e float64
		for _, p := range picks {
			e += m.Tracks[p.Track].EnergyPct
		}
		return e / float64(len(picks))
	}
	if calm, energetic := mean(ModeCalm), mean(ModeEnergetic); calm >= energetic-0.2 {
		t.Fatalf("calm %.2f should be much lower than energetic %.2f", calm, energetic)
	}
	u := likesGenre(m, "C", 3)
	// the 3 liked tracks share one artist, so diversity allows L, other, L
	fav := Rank(m, u, &Session{Mode: ModeFavorites}, w, 3, rnd(), now)
	nLiked := 0
	for _, p := range fav {
		if u.Tracks[p.Track].Liked {
			nLiked++
		}
	}
	if nLiked < 2 || !u.Tracks[fav[0].Track].Liked {
		t.Fatalf("favorites mode should lead with liked tracks (%d/3)", nLiked)
	}
	disc := Rank(m, u, &Session{Mode: ModeDiscover}, w, 5, rnd(), now)
	for _, p := range disc {
		if _, heard := u.Tracks[p.Track]; heard {
			t.Fatal("discover mode served a heard track")
		}
	}
	for _, md := range []string{"", ModeDefault, ModeCalm, ModeEnergetic, ModeDiscover, ModeFavorites} {
		if !ValidMode(md) {
			t.Fatal(md)
		}
	}
	if ValidMode("party") {
		t.Fatal("unknown mode accepted")
	}
}

func TestExplorationAndThompson(t *testing.T) {
	m := world()
	u := likesGenre(m, "A", 3)
	u.Sources = map[string]Beta{SrcCF: {Alpha: 50, Beta: 1}, SrcContent: {Alpha: 1, Beta: 50}}
	w := DefaultWeights()
	w.Epsilon = 1 // every slot explores
	picks := Rank(m, u, &Session{}, w, 5, rnd(), now)
	for _, p := range picks {
		if p.Source != SrcExplore || p.Reason.Kind != ReasonDiscovery {
			t.Fatalf("expected exploration picks, got %+v", p)
		}
		if _, heard := u.Tracks[p.Track]; heard {
			t.Fatal("exploration must pick unheard tracks")
		}
	}
	mult := thompson(u, DefaultWeights(), rnd())
	if mult[SrcCF] <= mult[SrcContent] {
		t.Fatalf("thompson multipliers ignore arm stats: %v", mult)
	}
	r := rnd()
	var s float64
	for i := 0; i < 4000; i++ {
		s += sampleBeta(r, 2, 6)
	}
	if mean := s / 4000; math.Abs(mean-0.25) > 0.02 {
		t.Fatalf("beta mean %.3f", mean)
	}
	if g := sampleGamma(r, 0.5); g < 0 {
		t.Fatal("gamma")
	}
}

func TestPenaltiesAndColdItems(t *testing.T) {
	ut := UserTrack{LastPlayed: now.Add(-time.Hour), EarlySkips: 5, Affinity: -2}
	if p := penalties(ut, true, now); p > -2.5 {
		t.Fatalf("penalty %.2f", p)
	}
	if penalties(UserTrack{LastPlayed: now.Add(-5 * time.Hour)}, true, now) != -0.35 || penalties(ut, false, now) != 0 {
		t.Fatal("penalty cases")
	}
	// a fresh track without collaborative data can still rank via content + freshness
	m := world()
	m.Tracks[59].CreatedAt = now.AddDate(0, 0, -1)
	u := likesGenre(m, m.Tracks[59].Genre, 3)
	for i := range m.Tracks {
		if i != 59 {
			m.CF[i] = []model.Neighbor{{Idx: 0, Sim: 0.1}}
		}
	}
	w := DefaultWeights()
	w.Epsilon = 0
	picks := Rank(m, u, &Session{}, w, 10, rnd(), now)
	found := false
	for _, p := range picks {
		found = found || p.Track == 59
	}
	if !found {
		t.Fatal("fresh cold track matching the taste should be recommended")
	}
	if Rank(nil, nil, nil, w, 5, nil, now) != nil || Rank(m, nil, nil, w, 0, nil, now) != nil {
		t.Fatal("degenerate inputs")
	}
}
