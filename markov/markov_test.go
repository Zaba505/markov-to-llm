package markov

import (
	"errors"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name string
		text string
		want [][]string
	}{
		{name: "empty", text: "", want: nil},
		{name: "only symbols", text: " \n\n \"()*[] ", want: nil},
		{name: "only punctuation", text: "... , ; ?!", want: nil},
		{
			name: "lowercases and splits punctuation",
			text: "The cat, sat.",
			want: [][]string{{"the", "cat", ",", "sat", "."}},
		},
		{
			name: "keeps contractions and hyphens",
			text: "Don't stop well-known",
			want: [][]string{{"don't", "stop", "well-known"}},
		},
		{
			name: "normalizes curly apostrophes",
			text: "don’t",
			want: [][]string{{"don't"}},
		},
		{
			name: "drops edge apostrophes and hyphens",
			text: "'tis - dogs' -x",
			want: [][]string{{"tis", "dogs", "x"}},
		},
		{
			name: "keeps numbers whole",
			text: "pi is 3.14 and 1,000.",
			want: [][]string{{"pi", "is", "3.14", "and", "1,000", "."}},
		},
		{
			name: "punctuation run is one terminator",
			text: "what?! really... yes",
			want: [][]string{{"what", "?!"}, {"really", "..."}, {"yes"}},
		},
		{
			name: "single newline doesn't end a sentence",
			text: "one\ntwo",
			want: [][]string{{"one", "two"}},
		},
		{
			name: "blank line ends a sentence",
			text: "title\n\nbody text\n  \t\nend",
			want: [][]string{{"title"}, {"body", "text"}, {"end"}},
		},
		{
			name: "crlf line endings and BOM",
			text: "\uFEFFone\r\n\r\ntwo",
			want: [][]string{{"one"}, {"two"}},
		},
		{
			name: "drops quotes and brackets",
			text: `"Hello" (world) [x]`,
			want: [][]string{{"hello", "world", "x"}},
		},
		{
			name: "drops leading punctuation",
			text: ", then. ; next",
			want: [][]string{{"then", "."}, {"next"}},
		},
		{
			name: "unicode words",
			text: "Über naïve 日本語. Ça va",
			want: [][]string{{"über", "naïve", "日本語", "."}, {"ça", "va"}},
		},
		{
			name: "drops a word that is too long",
			text: "before " + strings.Repeat("x", MaxTokenRunes+1) + " after",
			want: [][]string{{"before", "after"}},
		},
		{
			name: "keeps a word of exactly the longest length",
			text: strings.Repeat("é", MaxTokenRunes),
			want: [][]string{{strings.Repeat("é", MaxTokenRunes)}},
		},
		{
			name: "drops punctuation after a dropped word",
			text: strings.Repeat("x", MaxTokenRunes+1) + ", after",
			want: [][]string{{"after"}},
		},
		{
			name: "cuts a run of terminators that is too long",
			text: "wait" + strings.Repeat(".", MaxTokenRunes+1),
			want: [][]string{{"wait", strings.Repeat(".", MaxTokenRunes)}},
		},
		{
			name: "abbreviations end a sentence",
			text: "Mr. Smith",
			want: [][]string{{"mr", "."}, {"smith"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Tokenize(tt.text)
			if !slices.EqualFunc(got, tt.want, slices.Equal[[]string]) {
				t.Errorf("Tokenize(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestIsTerminator(t *testing.T) {
	for token, want := range map[string]bool{".": true, "?!": true, "...": true, "…": true, ",": false, "the": false, "": false} {
		if got := IsTerminator(token); got != want {
			t.Errorf("IsTerminator(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestDetokenize(t *testing.T) {
	tests := []struct {
		tokens []string
		want   string
	}{
		{nil, ""},
		{[]string{"the", "cat", ",", "sat", ";", "ran", ":", "far", "."}, "The cat, sat; ran: far."},
		{[]string{"über", "alles", "?!"}, "Über alles?!"},
	}
	for _, tt := range tests {
		if got := Detokenize(tt.tokens); got != tt.want {
			t.Errorf("Detokenize(%q) = %q, want %q", tt.tokens, got, tt.want)
		}
	}
}

func TestBuild(t *testing.T) {
	c := Build(Tokenize("The cat sat. The cat ran. The dog sat."))

	if got, want := c.Tokens(), 12; got != want {
		t.Errorf("Tokens() = %d, want %d", got, want)
	}
	if got, want := c.Sentences(), 3; got != want {
		t.Errorf("Sentences() = %d, want %d", got, want)
	}
	if got, want := c.Vocabulary(), 6; got != want {
		t.Errorf("Vocabulary() = %d, want %d", got, want)
	}
	if got, want := c.Count("the"), 3; got != want {
		t.Errorf(`Count("the") = %d, want %d`, got, want)
	}
	if got, want := c.Top(3), []string{".", "the", "cat"}; !slices.Equal(got, want) {
		t.Errorf("Top(3) = %q, want %q", got, want)
	}
	if got := c.Top(100); len(got) != 6 {
		t.Errorf("Top(100) has %d tokens, want all 6", len(got))
	}

	want := []Transition{
		{Token: "cat", Count: 2, Probability: 2.0 / 3},
		{Token: "dog", Count: 1, Probability: 1.0 / 3},
	}
	if got := c.Next("the"); !slices.Equal(got, want) {
		t.Errorf(`Next("the") = %+v, want %+v`, got, want)
	}
	if got, want := c.Next("."), []Transition{{Token: EOS, Count: 3, Probability: 1}}; !slices.Equal(got, want) {
		t.Errorf(`Next(".") = %+v, want %+v`, got, want)
	}
	if got := c.Next("missing"); got != nil {
		t.Errorf(`Next("missing") = %+v, want nil`, got)
	}
}

func TestBuild_probabilities_sum_to_one(t *testing.T) {
	c := Build(Tokenize(corpus))
	for _, token := range c.Top(c.Vocabulary()) {
		sum := 0.0
		for _, next := range c.Next(token) {
			sum += next.Probability
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Errorf("probabilities after %q sum to %v", token, sum)
		}
	}
}

func TestBuild_skips_empty_sentences(t *testing.T) {
	c := Build([][]string{{}, {"one"}})
	if c.Sentences() != 1 || c.Tokens() != 1 {
		t.Errorf("Sentences() = %d, Tokens() = %d, want 1 and 1", c.Sentences(), c.Tokens())
	}
}

func TestCanStart(t *testing.T) {
	c := Build(Tokenize("Stop, now."))
	for token, want := range map[string]bool{"stop": true, ",": false, "now": true, ".": false, "missing": false, "": false, EOS: false} {
		if got := c.CanStart(token); got != want {
			t.Errorf("CanStart(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestCanContinue(t *testing.T) {
	c := Build(Tokenize("Stop, now."))
	for token, want := range map[string]bool{"stop": true, ",": true, "now": true, ".": false, "missing": false, "": false, EOS: false} {
		if got := c.CanContinue(token); got != want {
			t.Errorf("CanContinue(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	c := Build(Tokenize("the then the there. the then."))

	tests := []struct {
		prefix string
		n      int
		want   []string
	}{
		{"th", 10, []string{"the", "then", "there"}},
		{"th", 1, []string{"the"}},
		{" THE ", 2, []string{"the", "then"}},
		{"", 2, []string{"the", "then"}},
		{".", 10, []string{}},
		{"x", 10, []string{}},
	}
	for _, tt := range tests {
		if got := c.Search(tt.prefix, tt.n); !slices.Equal(got, tt.want) {
			t.Errorf("Search(%q, %d) = %q, want %q", tt.prefix, tt.n, got, tt.want)
		}
	}
}

func TestSearch_finds_only_words(t *testing.T) {
	c := Build(Tokenize("One, two, three, four."))
	if got, want := c.Search("", 10), []string{"four", "one", "three", "two"}; !slices.Equal(got, want) {
		t.Errorf(`Search("", 10) = %q, want %q`, got, want)
	}
	if got := c.Search(",", 10); len(got) != 0 {
		t.Errorf(`Search(",", 10) = %q, want nothing`, got)
	}
}

func TestSearch_spells_a_prefix_as_tokens_are_spelled(t *testing.T) {
	c := Build(Tokenize("Don’t stop."))
	for _, prefix := range []string{"don'", "don’", "donʼ", "DON’T"} {
		if got, want := c.Search(prefix, 10), []string{"don't"}; !slices.Equal(got, want) {
			t.Errorf("Search(%q, 10) = %q, want %q", prefix, got, want)
		}
	}
}

func TestGenerate_rejects_a_token_that_cannot_start(t *testing.T) {
	c := Build(Tokenize("Stop, now."))
	for _, start := range []string{"missing", ".", ",", "", EOS} {
		if _, err := c.Generate(start, testRNG(), 10); !errors.Is(err, ErrCannotStart) {
			t.Errorf("Generate(%q) error = %v, want ErrCannotStart", start, err)
		}
	}
}

func TestGenerate_follows_the_only_path(t *testing.T) {
	c := Build(Tokenize("One two, three."))

	s, err := c.Generate("one", testRNG(), 60)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.Text(), "One two, three."; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if s.Truncated {
		t.Error("Truncated = true, want false")
	}
	if got, want := len(s.Steps), len(s.Tokens); got != want {
		t.Errorf("%d steps for %d tokens, want one each, the last choosing EOS", got, want)
	}
	last := s.Steps[len(s.Steps)-1]
	if last.From != "." || len(last.Candidates) != 1 || last.Candidates[0].Token != EOS {
		t.Errorf("last step = %+v, want the terminator choosing EOS", last)
	}
}

func TestGenerate_every_step_is_a_real_transition(t *testing.T) {
	c := Build(Tokenize(corpus))
	rng := testRNG()

	for range 200 {
		start := c.Search("", 20)[rng.IntN(20)]
		s, err := c.Generate(start, rng, 60)
		if err != nil {
			t.Fatal(err)
		}
		if s.Tokens[0] != start {
			t.Fatalf("first token = %q, want %q", s.Tokens[0], start)
		}
		for i, step := range s.Steps {
			if step.From != s.Tokens[i] {
				t.Fatalf("step %d is from %q, want %q", i, step.From, s.Tokens[i])
			}
			chosen := chosenOf(t, step)
			if !slices.ContainsFunc(c.Next(step.From), func(n Transition) bool { return n.Token == chosen }) {
				t.Fatalf("%q -> %q is not a transition of the chain", step.From, chosen)
			}
			if i+1 < len(s.Tokens) && chosen != s.Tokens[i+1] {
				t.Fatalf("step %d chose %q, but the next token is %q", i, chosen, s.Tokens[i+1])
			}
			if got, want := len(step.Candidates)+step.Others, len(c.Next(step.From)); got != want {
				t.Fatalf("step %d accounts for %d next tokens, want %d", i, got, want)
			}
			if len(step.Candidates) > topCandidates+1 {
				t.Fatalf("step %d keeps %d candidates, want at most %d", i, len(step.Candidates), topCandidates+1)
			}
		}
	}
}

func TestGenerate_keeps_an_unlikely_choice_in_the_trace(t *testing.T) {
	var b strings.Builder
	for _, next := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		b.WriteString("x " + next + ".\n")
	}
	c := Build(Tokenize(b.String()))

	rng := testRNG()
	for range 200 {
		s, err := c.Generate("x", rng, 60)
		if err != nil {
			t.Fatal(err)
		}
		first := s.Steps[0]
		chosenOf(t, first)
		if first.Others+len(first.Candidates) != 8 {
			t.Fatalf("first step = %+v, want it to account for all 8 tokens", first)
		}
	}
}

func TestGenerate_samples_by_probability(t *testing.T) {
	c := Build(Tokenize(strings.Repeat("x a.\n", 3) + "x b."))

	rng := testRNG()
	const walks = 10000
	a := 0
	for range walks {
		s, err := c.Generate("x", rng, 60)
		if err != nil {
			t.Fatal(err)
		}
		if s.Tokens[1] == "a" {
			a++
		}
	}
	if got := float64(a) / walks; math.Abs(got-0.75) > 0.02 {
		t.Errorf(`"a" came next %v of the time, want about 0.75`, got)
	}
}

func TestGenerate_stops_at_the_length_limit(t *testing.T) {
	// a is followed only by a, except for the one sentence end after the last.
	c := Build(Tokenize(strings.Repeat("a ", 1000)))

	s, err := c.Generate("a", rand.New(rand.NewPCG(3, 4)), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tokens) > 5 {
		t.Errorf("%d tokens, want at most 5", len(s.Tokens))
	}
	if !s.Truncated {
		t.Error("Truncated = false, want true for a walk that hadn't ended")
	}
}

func TestGenerate_does_not_truncate_a_sentence_that_ends_at_the_limit(t *testing.T) {
	c := Build(Tokenize("a b c d e."))

	s, err := c.Generate("a", testRNG(), 6)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c", "d", "e", "."}; !slices.Equal(s.Tokens, want) {
		t.Errorf("Tokens = %q, want %q", s.Tokens, want)
	}
	if s.Truncated {
		t.Error("Truncated = true, want false for a sentence that ended")
	}
	if len(s.Steps) != 6 || chosenOf(t, s.Steps[5]) != EOS {
		t.Errorf("Steps = %+v, want 6, the last choosing EOS", s.Steps)
	}
}

func TestGenerate_truncates_with_no_step_for_a_token_it_did_not_add(t *testing.T) {
	c := Build(Tokenize("a b c d e."))

	s, err := c.Generate("a", testRNG(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b", "c", "d", "e"}; !slices.Equal(s.Tokens, want) {
		t.Errorf("Tokens = %q, want %q", s.Tokens, want)
	}
	if !s.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(s.Steps) != 4 {
		t.Errorf("%d steps, want one for each token after the first", len(s.Steps))
	}
}

func chosenOf(t *testing.T, step Step) string {
	t.Helper()
	chosen := ""
	for _, candidate := range step.Candidates {
		if candidate.Chosen {
			if chosen != "" {
				t.Fatalf("step %+v has two chosen tokens", step)
			}
			chosen = candidate.Token
		}
	}
	if chosen == "" {
		t.Fatalf("step %+v has no chosen token", step)
	}
	return chosen
}

func testRNG() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

const corpus = `The quick brown fox jumps over the lazy dog. The dog barks, and the fox runs!
Does the dog chase the fox? The fox is quick; the dog is lazy.

Some sentences have no ending
and others run on, and on, and on, until the end of the text.`
