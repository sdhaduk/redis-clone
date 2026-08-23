package skiplist

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// ---------- helpers ----------

// pair is the test-side view of an element, independent of skiplist internals.
type pair struct {
	score  float64
	member string
}

// before is the sort rule the skiplist must obey: ascending score,
// ties broken by ascending member.
func before(a, b pair) bool {
	if a.score != b.score {
		return a.score < b.score
	}
	return a.member < b.member
}

// walkLane0 collects every element by following the bottom lane.
func walkLane0(sl *SkipList) []pair {
	var out []pair
	for n := sl.head.next[0]; n != nil; n = n.next[0] {
		out = append(out, pair{score: n.score, member: n.member})
	}
	return out
}

// checkSorted walks lane 0 and asserts strict sortedness under the tie rule.
func checkSorted(t *testing.T, sl *SkipList) {
	t.Helper()
	got := walkLane0(sl)
	for i := 1; i < len(got); i++ {
		if !before(got[i-1], got[i]) {
			t.Fatalf("lane 0 out of order at index %d: %+v then %+v", i-1, got[i-1], got[i])
		}
	}
}

// checkMatches asserts lane 0 holds exactly the expected pairs, in sorted order.
func checkMatches(t *testing.T, sl *SkipList, want []pair) {
	t.Helper()
	sorted := make([]pair, len(want))
	copy(sorted, want)
	sort.Slice(sorted, func(i, j int) bool { return before(sorted[i], sorted[j]) })

	got := walkLane0(sl)
	if len(got) != len(sorted) {
		t.Fatalf("lane 0 has %d elements, want %d", len(got), len(sorted))
	}
	for i := range sorted {
		if got[i] != sorted[i] {
			t.Fatalf("lane 0 index %d: got %+v, want %+v", i, got[i], sorted[i])
		}
	}
	if sl.numNodes != len(sorted) {
		t.Fatalf("numNodes is %d, want %d", sl.numNodes, len(sorted))
	}
}

// checkSpans verifies the span invariants directly. Span bugs never crash —
// they only produce wrong ranks — so we check the structure itself:
//   - span[0] == 1 on every node (each bottom-lane hop covers exactly one element)
//   - at every level, span[i] equals the true lane-0 distance to next[i]
func checkSpans(t *testing.T, sl *SkipList) {
	t.Helper()

	// position of each node in lane 0; head sits at position 0
	pos := map[*node]int{sl.head: 0}
	p := 1
	for n := sl.head.next[0]; n != nil; n = n.next[0] {
		pos[n] = p
		p++
	}

	nodes := []*node{sl.head}
	for n := sl.head.next[0]; n != nil; n = n.next[0] {
		nodes = append(nodes, n)
	}

	for _, n := range nodes {
		if n.next[0] != nil && n.span[0] != 1 {
			t.Fatalf("node %q: span[0] = %d, want 1", n.member, n.span[0])
		}
		for i := 0; i < len(n.next); i++ {
			if n.next[i] == nil {
				continue // spans past the tail are never read by Search
			}
			want := int64(pos[n.next[i]] - pos[n])
			if n.span[i] != want {
				t.Fatalf("node %q level %d: span = %d, want %d (true lane-0 distance)",
					n.member, i, n.span[i], want)
			}
		}
	}
}

// checkRanks asserts Rank agrees with an oracle rank computed from the
// expected data alone (sort the pairs, take the index) — the skiplist's own
// lanes play no part in producing the expected value.
func checkRanks(t *testing.T, sl *SkipList, want []pair) {
	t.Helper()
	sorted := make([]pair, len(want))
	copy(sorted, want)
	sort.Slice(sorted, func(i, j int) bool { return before(sorted[i], sorted[j]) })

	for oracle, p := range sorted {
		if got := sl.Rank(p.score, p.member); got != int64(oracle) {
			t.Fatalf("Rank(%v, %q) = %d, want %d", p.score, p.member, got, oracle)
		}
	}
}

// buildPairs makes n pairs with deliberate score ties (groups of 4 share a
// score) so the member tie-break rule actually gets exercised.
func buildPairs(n int) []pair {
	pairs := make([]pair, 0, n)
	for i := 0; i < n; i++ {
		pairs = append(pairs, pair{score: float64(i / 4), member: fmt.Sprintf("m%02d", i)})
	}
	return pairs
}

// shuffled returns a copy of pairs in random order using the given rng.
func shuffled(rng *rand.Rand, pairs []pair) []pair {
	out := make([]pair, len(pairs))
	copy(out, pairs)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// ---------- ordering ----------

func TestInsertShuffledIsSorted(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	pairs := buildPairs(40)

	sl := New()
	for _, p := range shuffled(rng, pairs) {
		sl.Insert(p.score, p.member)
	}

	checkSorted(t, sl)
	checkMatches(t, sl, pairs)
}

// ---------- delete ----------

func TestDelete(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	pairs := buildPairs(40)

	sl := New()
	for _, p := range shuffled(rng, pairs) {
		sl.Insert(p.score, p.member)
	}

	// delete every third element
	remaining := []pair{}
	for i, p := range pairs {
		if i%3 == 0 {
			if !sl.Delete(p.score, p.member) {
				t.Fatalf("Delete(%v, %q) = false for an existing element", p.score, p.member)
			}
		} else {
			remaining = append(remaining, p)
		}
	}

	checkSorted(t, sl)
	checkMatches(t, sl, remaining)

	// deleting something absent must report false and change nothing:
	// unknown member, wrong score for a real member, and a repeat delete
	if sl.Delete(0, "nope") {
		t.Error("Delete of an unknown member returned true")
	}
	if sl.Delete(999, remaining[0].member) {
		t.Error("Delete with the wrong score returned true")
	}
	if sl.Delete(pairs[0].score, pairs[0].member) {
		t.Error("second Delete of the same element returned true")
	}
	checkMatches(t, sl, remaining)
}

// ---------- re-score ----------

func TestRescoreMoves(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	pairs := buildPairs(40)

	sl := New()
	for _, p := range shuffled(rng, pairs) {
		sl.Insert(p.score, p.member)
	}

	// move m00 from score 0 to a score past everything else
	if !sl.Delete(0, "m00") {
		t.Fatal("Delete of m00 at its old score failed")
	}
	sl.Insert(100, "m00")

	if got := sl.Rank(0, "m00"); got != -1 {
		t.Errorf("Rank at the old score = %d, want -1 (element should have moved)", got)
	}
	if got := sl.Rank(100, "m00"); got != int64(len(pairs)-1) {
		t.Errorf("Rank at the new score = %d, want %d (last place)", got, len(pairs)-1)
	}

	// the member must appear exactly once, at the new score
	count := 0
	for _, p := range walkLane0(sl) {
		if p.member == "m00" {
			count++
			if p.score != 100 {
				t.Errorf("m00 found with score %v, want 100", p.score)
			}
		}
	}
	if count != 1 {
		t.Errorf("m00 appears %d times after re-score, want 1", count)
	}
	checkSorted(t, sl)
}

// ---------- spans and ranks ----------

func TestSpansAndRanks(t *testing.T) {
	// node heights are random, so one pass can miss a structural bug;
	// repeat with fresh shuffles and level rolls each trial
	for trial := 0; trial < 25; trial++ {
		rng := rand.New(rand.NewSource(int64(100 + trial)))
		pairs := buildPairs(48)

		sl := New()
		for _, p := range shuffled(rng, pairs) {
			sl.Insert(p.score, p.member)
		}

		checkSpans(t, sl)
		checkRanks(t, sl, pairs)

		// delete a random ~third and re-verify everything
		remaining := []pair{}
		for _, p := range pairs {
			if rng.Intn(3) == 0 {
				if !sl.Delete(p.score, p.member) {
					t.Fatalf("trial %d: Delete(%v, %q) failed", trial, p.score, p.member)
				}
			} else {
				remaining = append(remaining, p)
			}
		}

		checkSorted(t, sl)
		checkSpans(t, sl)
		checkRanks(t, sl, remaining)

		// Rank of anything deleted must be -1 now
		for _, p := range pairs {
			found := false
			for _, r := range remaining {
				if r == p {
					found = true
					break
				}
			}
			if !found {
				if got := sl.Rank(p.score, p.member); got != -1 {
					t.Fatalf("trial %d: Rank of deleted (%v, %q) = %d, want -1",
						trial, p.score, p.member, got)
				}
			}
		}
	}
}

// ---------- GetElem agrees with rank ----------

func TestGetElemMatchesRank(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pairs := buildPairs(40)

	sl := New()
	for _, p := range shuffled(rng, pairs) {
		sl.Insert(p.score, p.member)
	}

	sorted := make([]pair, len(pairs))
	copy(sorted, pairs)
	sort.Slice(sorted, func(i, j int) bool { return before(sorted[i], sorted[j]) })

	// GetElem is driven entirely by spans, so this catches span bugs too
	for i, p := range sorted {
		n := sl.GetElem(int64(i))
		if n == nil || n.member != p.member || n.score != p.score {
			t.Fatalf("GetElem(%d) = %+v, want (%v, %q)", i, n, p.score, p.member)
		}
	}
	if n := sl.GetElem(int64(len(sorted))); n != nil {
		t.Errorf("GetElem past the end = %+v, want nil", n)
	}
}
