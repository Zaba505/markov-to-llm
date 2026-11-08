package endpoint

import (
	"net/url"

	"github.com/Zaba505/markov-to-llm/graph"
	"github.com/Zaba505/markov-to-llm/markov"
)

const (
	minLimit     = 10
	maxLimit     = 100
	defaultLimit = 30
	limitStep    = 5

	// panelRows is how many next tokens the probability panel lists.
	panelRows = 12

	// chipCount is how many tokens a search returns, and the first ones offered.
	chipCount = 12

	endOfSentence = "(end of sentence)"
)

type pageView struct {
	MaxUploadMB int64
	RunsURL     string
}

type runView struct {
	ID            string
	Name          string
	Preview       string
	PreviewIsPart bool
	Tokens        int
	Vocabulary    int
	Sentences     int
	SentencesURL  string
	TokensURL     string
	MaxQuery      int
	Chain         chainView
	Selection     selectionView
	Chips         chipsView
}

func newRunView(r *run) runView {
	return runView{
		ID:            r.id,
		Name:          r.name,
		Preview:       r.preview,
		PreviewIsPart: r.previewIsPart,
		Tokens:        r.chain.Tokens(),
		Vocabulary:    r.chain.Vocabulary(),
		Sentences:     r.chain.Sentences(),
		SentencesURL:  runURL(sentencesPath, r.id),
		TokensURL:     runURL(tokensPath, r.id),
		MaxQuery:      maxQueryRunes,
		Chain:         newChainView(r, defaultLimit, ""),
		Selection:     selectionView{RunID: r.id},
		Chips:         newChipsView(r, ""),
	}
}

// chainView is the graph, its controls and the probability panel. It depends
// only on its run, its limit and its selected token.
type chainView struct {
	RunID      string
	Limit      int
	MinLimit   int
	MaxLimit   int
	LimitStep  int
	ShowLimit  bool
	SliderURL  string
	Vocabulary int
	MaxEdges   int

	Width, Height float64
	Nodes         []nodeView
	Edges         []graph.Edge
	Shown         int

	Selected      string
	SelectedCount int
	Next          []nextRow
	MoreNext      int

	// Selection updates step 3 out of band, in the response to selecting.
	Selection selectionView
}

type nodeView struct {
	graph.Node
	ID  int
	URL string
}

type nextRow struct {
	Token   string
	Percent string
	Width   string
	Count   int

	// URL selects Token, and is empty when Token can't be selected.
	URL string
}

func newChainView(r *run, limit int, selected string) chainView {
	g := r.graph.Build(limit, selected)
	v := chainView{
		RunID:      r.id,
		Limit:      limit,
		MinLimit:   minLimit,
		MaxLimit:   maxLimit,
		LimitStep:  limitStep,
		ShowLimit:  r.chain.Vocabulary() > minLimit,
		SliderURL:  chainURL(r.id, selected),
		Vocabulary: r.chain.Vocabulary(),
		MaxEdges:   graph.MaxEdgesPerNode,
		Width:      g.Width,
		Height:     g.Height,
		Edges:      g.Edges,
		Shown:      len(g.Nodes),
		Selected:   g.Selected,
		Selection:  newSelectionView(r, g.Selected),
	}
	v.Selection.OOB = true
	for i, node := range g.Nodes {
		nv := nodeView{Node: node, ID: i}
		if !node.Terminal {
			nv.URL = chainURL(r.id, node.Token)
		}
		v.Nodes = append(v.Nodes, nv)
	}
	if g.Selected != "" {
		v.SelectedCount = r.chain.Count(g.Selected)
		next := r.chain.Next(g.Selected)
		for _, t := range next[:min(panelRows, len(next))] {
			v.Next = append(v.Next, newNextRow(r, t))
		}
		v.MoreNext = len(next) - len(v.Next)
	}
	return v
}

func newNextRow(r *run, t markov.Transition) nextRow {
	row := nextRow{
		Token:   displayToken(t.Token),
		Percent: graph.Percent(t.Probability),
		Width:   formatWidth(t.Probability),
		Count:   t.Count,
	}
	if r.chain.CanContinue(t.Token) {
		row.URL = chainURL(r.id, t.Token)
	}
	return row
}

// chainURL is the URL that selects token. It never carries limit, since a
// repeated query parameter is rejected and every control that uses this URL
// sends the slider's value along with it.
func chainURL(runID, token string) string {
	u := runURL(chainPath, runID)
	if token == "" {
		return u
	}
	return u + "?" + url.Values{"token": {token}}.Encode()
}

// selectionView is the starting token and the button that generates from it.
type selectionView struct {
	RunID string

	// Token is the starting token, if there is one.
	Token string

	// Unusable is a selected token that can't start a sentence. A clause mark
	// can be selected for its odds, but it leaves nothing to generate from.
	Unusable string

	// OOB swaps the selection into place out of band.
	OOB bool
}

func newSelectionView(r *run, selected string) selectionView {
	v := selectionView{RunID: r.id}
	if r.chain.CanStart(selected) {
		v.Token = selected
	} else {
		v.Unusable = selected
	}
	return v
}

type chipsView struct {
	RunID string
	Chips []chip
}

type chip struct {
	Token string
	Count int
	URL   string
}

func newChipsView(r *run, query string) chipsView {
	v := chipsView{RunID: r.id}
	for _, token := range r.chain.Search(query, chipCount) {
		v.Chips = append(v.Chips, chip{Token: token, Count: r.chain.Count(token), URL: chainURL(r.id, token)})
	}
	return v
}

type sentenceView struct {
	Text      string
	Start     string
	Truncated bool
	MaxTokens int
	Steps     []stepView
}

type stepView struct {
	From       string
	Candidates []candidateView
	Others     int
}

type candidateView struct {
	Token   string
	Percent string
	Chosen  bool
}

func newSentenceView(s markov.Sentence, maxTokens int) sentenceView {
	v := sentenceView{
		Text:      s.Text(),
		Start:     s.Tokens[0],
		Truncated: s.Truncated,
		MaxTokens: maxTokens,
	}
	for _, step := range s.Steps {
		sv := stepView{From: step.From, Others: step.Others}
		for _, c := range step.Candidates {
			sv.Candidates = append(sv.Candidates, candidateView{
				Token:   displayToken(c.Token),
				Percent: graph.Percent(c.Probability),
				Chosen:  c.Chosen,
			})
		}
		v.Steps = append(v.Steps, sv)
	}
	return v
}

func displayToken(token string) string {
	if token == markov.EOS {
		return endOfSentence
	}
	return token
}
