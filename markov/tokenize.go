// Package markov builds a first-order Markov chain over the words and
// punctuation of a text corpus, and walks it to generate new sentences.
package markov

import (
	"strings"
	"unicode"
)

// Tokenize splits text into sentences of lowercase tokens.
//
// A token is a word, a clause mark (one of , ; :), or a terminator (a run of
// . ! ? or …). A word keeps its internal apostrophes and hyphens, as in don't
// and well-known, and a number keeps its internal . and , as in 3.14. Other
// symbols are dropped, since quotes and brackets that are sampled one at a
// time don't come out balanced.
//
// A sentence ends at a terminator or a blank line. Punctuation with no words
// before it in its sentence is dropped. An abbreviation such as "Mr." ends its
// sentence, a limitation of telling sentences apart this simply.
func Tokenize(text string) [][]string {
	text = strings.TrimPrefix(text, "\uFEFF")
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "’", "'", "ʼ", "'").Replace(text)
	runes := []rune(strings.ToLower(text))

	var sentences [][]string
	var current []string
	endSentence := func() {
		if len(current) > 0 {
			sentences = append(sentences, current)
			current = nil
		}
	}

	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case isWordRune(r):
			end := wordEnd(runes, i)
			current = append(current, string(runes[i:end]))
			i = end
		case isTerminatorRune(r):
			end := i
			for end < len(runes) && isTerminatorRune(runes[end]) {
				end++
			}
			if len(current) > 0 {
				current = append(current, string(runes[i:end]))
				endSentence()
			}
			i = end
		case r == ',' || r == ';' || r == ':':
			if len(current) > 0 {
				current = append(current, string(r))
			}
			i++
		case r == '\n':
			end, blank := skipBlankLines(runes, i)
			if blank {
				endSentence()
			}
			i = end
		default:
			i++
		}
	}
	endSentence()
	return sentences
}

// wordEnd returns the index just past the word that starts at start.
func wordEnd(runes []rune, start int) int {
	i := start + 1
	for i < len(runes) {
		r := runes[i]
		switch {
		case isWordRune(r):
			i++
		case (r == '\'' || r == '-') && i+1 < len(runes) && isWordRune(runes[i+1]):
			i += 2
		case (r == '.' || r == ',') && unicode.IsDigit(runes[i-1]) && i+1 < len(runes) && unicode.IsDigit(runes[i+1]):
			i += 2
		default:
			return i
		}
	}
	return i
}

// skipBlankLines returns the index of the first non-space rune after the
// newline at start, and whether another newline came before it.
func skipBlankLines(runes []rune, start int) (end int, blank bool) {
	i := start + 1
	for i < len(runes) && unicode.IsSpace(runes[i]) {
		if runes[i] == '\n' {
			blank = true
		}
		i++
	}
	return i, blank
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
}

func isTerminatorRune(r rune) bool {
	return r == '.' || r == '!' || r == '?' || r == '…'
}

// IsTerminator reports whether token ends a sentence.
func IsTerminator(token string) bool {
	for _, r := range token {
		return isTerminatorRune(r)
	}
	return false
}

// Detokenize joins tokens into readable text: no space before punctuation, and
// a capital first letter.
func Detokenize(tokens []string) string {
	var b strings.Builder
	for i, token := range tokens {
		if i > 0 && !isPunctuation(token) {
			b.WriteByte(' ')
		}
		if i == 0 {
			runes := []rune(token)
			runes[0] = unicode.ToUpper(runes[0])
			token = string(runes)
		}
		b.WriteString(token)
	}
	return b.String()
}

func isPunctuation(token string) bool {
	return token == "," || token == ";" || token == ":" || IsTerminator(token)
}
