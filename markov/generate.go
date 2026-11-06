package markov

import (
	"errors"
	"math/rand/v2"
)

// ErrCannotStart is returned for a starting token that isn't in the corpus, or
// that is a terminator.
var ErrCannotStart = errors.New("a sentence can't start from this token")

// topCandidates is how many of a step's most likely tokens its trace keeps.
const topCandidates = 5

// Candidate is a token a Step could have chosen.
type Candidate struct {
	Token       string
	Probability float64
	Chosen      bool
}

// Step is one prediction of a generated sentence: from the current token, the
// distribution that was sampled, and how it came out.
type Step struct {
	// From is the current token.
	From string

	// Candidates are the most likely next tokens, most likely first. The chosen
	// token is always among them, last when it isn't one of the most likely.
	Candidates []Candidate

	// Others is how many other tokens could have come next.
	Others int
}

// Sentence is a generated sentence and how it came to be.
type Sentence struct {
	Tokens []string
	Steps  []Step

	// Truncated reports that the sentence reached the length limit before the
	// chain chose to end it.
	Truncated bool
}

// Text returns the sentence as readable text.
func (s Sentence) Text() string { return Detokenize(s.Tokens) }

// Generate walks the chain from start, sampling each next token by its
// probability, until the walk reaches EOS or maxTokens tokens. It returns
// ErrCannotStart when CanStart(start) is false.
func (c *Chain) Generate(start string, rng *rand.Rand, maxTokens int) (Sentence, error) {
	if !c.CanStart(start) {
		return Sentence{}, ErrCannotStart
	}

	s := Sentence{Tokens: []string{start}}
	current := start
	for {
		if len(s.Tokens) >= maxTokens {
			s.Truncated = true
			return s, nil
		}
		transitions := c.next[current]
		chosen := sample(transitions, c.counts[current], rng)
		s.Steps = append(s.Steps, newStep(current, transitions, chosen))
		if transitions[chosen].Token == EOS {
			return s, nil
		}
		current = transitions[chosen].Token
		s.Tokens = append(s.Tokens, current)
	}
}

// sample returns the index of a transition, weighted by count. total is the sum
// of the transitions' counts.
func sample(transitions []Transition, total int, rng *rand.Rand) int {
	n := rng.IntN(total)
	for i, t := range transitions {
		n -= t.Count
		if n < 0 {
			return i
		}
	}
	panic("markov: total is less than the sum of the transitions' counts")
}

func newStep(from string, transitions []Transition, chosen int) Step {
	step := Step{From: from}
	for i, t := range transitions {
		if i < topCandidates || i == chosen {
			step.Candidates = append(step.Candidates, Candidate{
				Token:       t.Token,
				Probability: t.Probability,
				Chosen:      i == chosen,
			})
		}
	}
	step.Others = len(transitions) - len(step.Candidates)
	return step
}
