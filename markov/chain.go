package markov

import (
	"cmp"
	"slices"
	"strings"
)

// EOS is the end-of-sentence state: the last token of every sentence moves to
// it, so that the chance of a sentence ending is a probability like any other.
// The tokenizer drops < and >, so EOS is never a token of a corpus.
const EOS = "</s>"

// Transition is one possible next token after a given token.
type Transition struct {
	// Token is the next token, or EOS.
	Token string

	// Count is how many times the corpus has Token next.
	Count int

	// Probability is Count as a share of everything that came next.
	Probability float64
}

// Chain is a first-order Markov chain. It is read-only once built, so it is
// safe to use from several goroutines.
type Chain struct {
	next      map[string][]Transition
	counts    map[string]int
	ranked    []string
	tokens    int
	sentences int
}

// Build counts the transitions of sentences. Tokens of a sentence must not be
// empty, as Tokenize's never are.
func Build(sentences [][]string) *Chain {
	counts := make(map[string]map[string]int)
	add := func(from, to string) {
		successors := counts[from]
		if successors == nil {
			successors = make(map[string]int)
			counts[from] = successors
		}
		successors[to]++
	}

	c := &Chain{
		next:   make(map[string][]Transition),
		counts: make(map[string]int),
	}
	for _, sentence := range sentences {
		if len(sentence) == 0 {
			continue
		}
		c.sentences++
		c.tokens += len(sentence)
		for i, token := range sentence {
			if i+1 < len(sentence) {
				add(token, sentence[i+1])
			} else {
				add(token, EOS)
			}
		}
	}

	c.ranked = make([]string, 0, len(counts))
	for token, successors := range counts {
		total := 0
		for _, count := range successors {
			total += count
		}
		transitions := make([]Transition, 0, len(successors))
		for to, count := range successors {
			transitions = append(transitions, Transition{
				Token:       to,
				Count:       count,
				Probability: float64(count) / float64(total),
			})
		}
		slices.SortFunc(transitions, func(a, b Transition) int {
			return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Token, b.Token))
		})
		c.next[token] = transitions
		c.counts[token] = total
		c.ranked = append(c.ranked, token)
	}
	slices.SortFunc(c.ranked, func(a, b string) int {
		return cmp.Or(cmp.Compare(c.counts[b], c.counts[a]), cmp.Compare(a, b))
	})
	return c
}

// Next returns what can follow token, most likely first. The result must not
// be changed.
func (c *Chain) Next(token string) []Transition { return c.next[token] }

// Count returns how many times token appears in the corpus.
func (c *Chain) Count(token string) int { return c.counts[token] }

// Tokens returns the number of tokens in the corpus.
func (c *Chain) Tokens() int { return c.tokens }

// Vocabulary returns the number of distinct tokens in the corpus.
func (c *Chain) Vocabulary() int { return len(c.ranked) }

// Sentences returns the number of sentences in the corpus.
func (c *Chain) Sentences() int { return c.sentences }

// Top returns the n most frequent tokens, most frequent first, with ties in
// alphabetical order. The result must not be changed.
func (c *Chain) Top(n int) []string {
	return c.ranked[:min(max(n, 0), len(c.ranked))]
}

// CanContinue reports whether a walk can continue from token: it is in the
// corpus and isn't a terminator, which has nothing after it but the end.
func (c *Chain) CanContinue(token string) bool {
	_, ok := c.next[token]
	return ok && !IsTerminator(token)
}

// CanStart reports whether a sentence can start from token: it is in the corpus
// and is a word. Tokenize drops punctuation with no words before it, so no
// sentence of a corpus starts with any, and neither does a generated one.
func (c *Chain) CanStart(token string) bool {
	_, ok := c.next[token]
	return ok && !isPunctuation(token)
}

// Search returns up to n tokens that begin with prefix and can start a
// sentence, most frequent first. An empty prefix matches every such token.
func (c *Chain) Search(prefix string, n int) []string {
	prefix = normalize(strings.TrimSpace(prefix))
	matches := make([]string, 0, min(max(n, 0), len(c.ranked)))
	for _, token := range c.ranked {
		if len(matches) == n {
			break
		}
		if strings.HasPrefix(token, prefix) && !isPunctuation(token) {
			matches = append(matches, token)
		}
	}
	return matches
}
