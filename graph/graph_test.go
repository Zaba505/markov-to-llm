package graph

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Zaba505/markov-to-llm/markov"
)

const text = `The quick brown fox jumps over the lazy dog. The dog barks, and the fox runs!
Does the dog chase the fox? The fox is quick; the dog is lazy. Very very very good.
A dog and a fox and a cat, and the cat is here. The cat chases the dog, and the dog runs.`

func TestBuild_is_deterministic(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	want := Build(c, 15, "the")
	for range 5 {
		if got := Build(c, 15, "the"); !reflect.DeepEqual(got, want) {
			t.Fatal("two builds with the same arguments differ")
		}
	}
}

func TestBuild_respects_the_limit(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	for _, limit := range []int{1, 5, 10} {
		g := Build(c, limit, "")
		if len(g.Nodes) != limit {
			t.Errorf("limit %d: %d nodes", limit, len(g.Nodes))
		}
		if got, want := tokensOf(g), c.Top(limit); !slices.Equal(got, want) {
			t.Errorf("limit %d: nodes %q, want the most frequent %q", limit, got, want)
		}
	}
	if g := Build(c, 1000, ""); len(g.Nodes) != c.Vocabulary() {
		t.Errorf("limit above the vocabulary: %d nodes, want %d", len(g.Nodes), c.Vocabulary())
	}
}

func TestBuild_includes_the_selected_token_and_what_follows_it(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))

	g := Build(c, 3, "quick")
	if g.Selected != "quick" {
		t.Errorf("Selected = %q, want quick", g.Selected)
	}
	tokens := tokensOf(g)
	for _, want := range []string{"quick", "brown", ";"} {
		if !slices.Contains(tokens, want) {
			t.Errorf("nodes %q don't include %q", tokens, want)
		}
	}
	if got := len(g.Nodes); got != 6 {
		t.Errorf("%d nodes, want the top 3, the selected token and its 2 next tokens", got)
	}
}

func TestBuild_adds_at_most_the_shown_successors_of_a_selection(t *testing.T) {
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "hub word%d.\n", i)
	}
	c := markov.Build(markov.Tokenize(b.String()))

	g := Build(c, 2, "hub")
	if got := len(g.Nodes); got > 2+MaxEdgesPerNode {
		t.Errorf("%d nodes, want at most %d", got, 2+MaxEdgesPerNode)
	}
}

func TestBuild_highlights_the_selected_token_and_its_edges(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))

	g := Build(c, 15, "the")
	nodes := nodesByToken(g)
	if !nodes["the"].Selected || !nodes["the"].Highlighted {
		t.Errorf("selected node = %+v, want it selected and highlighted", nodes["the"])
	}
	seenHighlighted := false
	highlightedTargets := map[string]bool{"the": true}
	for _, e := range g.Edges {
		if e.Highlighted != (e.From == "the") {
			t.Errorf("edge %s -> %s: Highlighted = %v", e.From, e.To, e.Highlighted)
		}
		if seenHighlighted && !e.Highlighted {
			t.Errorf("edge %s -> %s is drawn after a highlighted edge", e.From, e.To)
		}
		if e.Highlighted {
			seenHighlighted = true
			highlightedTargets[e.To] = true
		}
	}
	if !seenHighlighted {
		t.Error("no highlighted edges")
	}
	for token, node := range nodes {
		if node.Highlighted != highlightedTargets[token] {
			t.Errorf("node %q: Highlighted = %v, want %v", token, node.Highlighted, highlightedTargets[token])
		}
	}
}

func TestBuild_ignores_a_selected_token_not_in_the_chain(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	g := Build(c, 5, "missing")
	if g.Selected != "" || slices.Contains(tokensOf(g), "missing") {
		t.Errorf("Selected = %q, nodes %q, want no selection", g.Selected, tokensOf(g))
	}
}

func TestBuild_selection_moves_nothing_when_it_adds_no_nodes(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	all := c.Vocabulary()
	plain := nodesByToken(Build(c, all, ""))
	for token, node := range nodesByToken(Build(c, all, "the")) {
		if node.X != plain[token].X || node.Y != plain[token].Y {
			t.Errorf("%q moved from (%v, %v) to (%v, %v)", token, plain[token].X, plain[token].Y, node.X, node.Y)
		}
	}
}

func TestBuild_keeps_nodes_in_bounds(t *testing.T) {
	var b strings.Builder
	for i := range 400 {
		fmt.Fprintf(&b, "word%d word%d word%d, word%d.\n", i%90, (i*7)%90, (i*13)%90, (i*3)%90)
	}
	c := markov.Build(markov.Tokenize(b.String()))

	for _, limit := range []int{1, 2, 10, 30, 100} {
		g := Build(c, limit, "word3")
		for _, n := range g.Nodes {
			if math.IsNaN(n.X) || math.IsNaN(n.Y) {
				t.Fatalf("limit %d: node %q has no position", limit, n.Token)
			}
			if n.X < margin || n.X > g.Width-margin || n.Y < margin || n.Y > g.Height-margin {
				t.Errorf("limit %d: node %q at (%v, %v) is outside %v x %v, less its margin", limit, n.Token, n.X, n.Y, g.Width, g.Height)
			}
		}
	}
}

func TestBuild_separates_nodes(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	g := Build(c, 20, "")
	for i, a := range g.Nodes {
		for _, b := range g.Nodes[i+1:] {
			if d := math.Hypot(a.X-b.X, a.Y-b.Y); d < a.R+b.R {
				t.Errorf("%q and %q overlap: %v apart, with radii %v and %v", a.Token, b.Token, d, a.R, b.R)
			}
		}
	}
}

func TestBuild_limits_and_filters_edges(t *testing.T) {
	var b strings.Builder
	for i := range 12 {
		fmt.Fprintf(&b, "hub word%d.\nhub word%d.\n", i, i)
	}
	c := markov.Build(markov.Tokenize(b.String()))

	g := Build(c, c.Vocabulary(), "")
	outgoing := map[string]int{}
	shown := tokensOf(g)
	for _, e := range g.Edges {
		outgoing[e.From]++
		if e.To == markov.EOS {
			t.Errorf("edge %s -> EOS, want EOS left out of the picture", e.From)
		}
		if !slices.Contains(shown, e.From) || !slices.Contains(shown, e.To) {
			t.Errorf("edge %s -> %s has an end that isn't a node", e.From, e.To)
		}
		if e.Path == "" || strings.Contains(e.Path, "NaN") {
			t.Errorf("edge %s -> %s has path %q", e.From, e.To, e.Path)
		}
	}
	for token, count := range outgoing {
		if count > MaxEdgesPerNode {
			t.Errorf("%q has %d outgoing edges, want at most %d", token, count, MaxEdgesPerNode)
		}
	}
	if outgoing["hub"] != MaxEdgesPerNode {
		t.Errorf("hub has %d outgoing edges, want %d", outgoing["hub"], MaxEdgesPerNode)
	}
}

func TestBuild_draws_self_loops_and_two_way_edges(t *testing.T) {
	c := markov.Build(markov.Tokenize("Very very good. Good very."))
	g := Build(c, 10, "")

	loop, there, back := Edge{}, Edge{}, Edge{}
	for _, e := range g.Edges {
		switch {
		case e.From == "very" && e.To == "very":
			loop = e
		case e.From == "very" && e.To == "good":
			there = e
		case e.From == "good" && e.To == "very":
			back = e
		}
	}
	if !strings.Contains(loop.Path, "C") {
		t.Errorf("self-loop path = %q, want a cubic curve", loop.Path)
	}
	if there.Path == "" || back.Path == "" || there.Path == back.Path {
		t.Errorf("two-way paths = %q and %q, want two different curves", there.Path, back.Path)
	}
	if math.Hypot(there.LabelX-back.LabelX, there.LabelY-back.LabelY) < 10 {
		t.Errorf("two-way labels at (%v, %v) and (%v, %v) overlap", there.LabelX, there.LabelY, back.LabelX, back.LabelY)
	}
}

func TestBuild_marks_terminal_nodes(t *testing.T) {
	c := markov.Build(markov.Tokenize("Stop now."))
	nodes := nodesByToken(Build(c, 10, ""))
	if !nodes["."].Terminal {
		t.Error(`"." isn't terminal`)
	}
	if nodes["now"].Terminal {
		t.Error(`"now" is terminal`)
	}
}

func TestBuild_handles_empty_and_tiny_chains(t *testing.T) {
	if g := Build(markov.Build(nil), 10, ""); len(g.Nodes) != 0 || len(g.Edges) != 0 {
		t.Errorf("empty chain: %+v", g)
	}
	g := Build(markov.Build(markov.Tokenize("alone")), 10, "alone")
	if len(g.Nodes) != 1 || math.IsNaN(g.Nodes[0].X) {
		t.Errorf("one-token chain: %+v", g)
	}
}

func TestBuild_shortens_long_labels(t *testing.T) {
	long := strings.Repeat("x", 40)
	nodes := nodesByToken(Build(markov.Build(markov.Tokenize(long)), 10, ""))
	node := nodes[long]
	if node.Token != long {
		t.Errorf("Token = %q, want it whole", node.Token)
	}
	if got := len([]rune(node.Label)); got != maxLabelRunes || !strings.HasSuffix(node.Label, "…") {
		t.Errorf("Label = %q, want %d runes ending in an ellipsis", node.Label, maxLabelRunes)
	}
}

func TestPercent(t *testing.T) {
	for p, want := range map[float64]string{0: "0%", 0.001: "<1%", 0.01: "1%", 0.426: "43%", 0.99: "99%", 0.996: ">99%", 1: "100%"} {
		if got := Percent(p); got != want {
			t.Errorf("Percent(%v) = %q, want %q", p, got, want)
		}
	}
}

func TestBuilder_builds_what_Build_builds(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	b := NewBuilder(c)

	// Twice each, so that the second comes from what the first left behind, and
	// in an order where a selection could disturb what the next one finds.
	for range 2 {
		for _, selected := range []string{"", "quick", "the", "missing", "barks"} {
			if got, want := b.Build(5, selected), Build(c, 5, selected); !reflect.DeepEqual(got, want) {
				t.Errorf("selected %q: the Builder's picture differs from Build's", selected)
			}
		}
	}
}

func TestBuilder_lays_out_the_same_nodes_once(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	b := NewBuilder(c)

	all := c.Vocabulary()
	b.Build(all, "")
	b.Build(all, "the")
	b.Build(all, "quick")
	b.Build(all+50, "the")
	if got := len(b.layouts); got != 1 {
		t.Errorf("%d layouts for one set of nodes", got)
	}

	b.Build(3, "")
	b.Build(3, "quick")
	if got := len(b.layouts); got != 3 {
		t.Errorf("%d layouts, want one more for each new set of nodes", got)
	}
}

func TestBuilder_forgets_layouts_when_it_is_full(t *testing.T) {
	var s strings.Builder
	for i := range maxLayouts + 10 {
		fmt.Fprintf(&s, "word%d.\n", i)
	}
	c := markov.Build(markov.Tokenize(s.String()))
	b := NewBuilder(c)

	for i := range maxLayouts + 10 {
		b.Build(1, fmt.Sprintf("word%d", i))
		if got := len(b.layouts); got > maxLayouts {
			t.Fatalf("%d layouts, want at most %d", got, maxLayouts)
		}
	}
}

func TestBuilder_can_be_shared(t *testing.T) {
	c := markov.Build(markov.Tokenize(text))
	b := NewBuilder(c)
	want := Build(c, 5, "the")

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if got := b.Build(5, "the"); !reflect.DeepEqual(got, want) {
				t.Error("a shared Builder's picture differs from Build's")
			}
		})
	}
	wg.Wait()
}

func tokensOf(g Graph) []string {
	tokens := make([]string, len(g.Nodes))
	for i, n := range g.Nodes {
		tokens[i] = n.Token
	}
	return tokens
}

func nodesByToken(g Graph) map[string]Node {
	nodes := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes[n.Token] = n
	}
	return nodes
}
