// Package endpoint is the service's HTTP API: typed operations that answer with
// the HTML fragments htmx swaps into the page, and the page's static assets.
package endpoint

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/z5labs/humus"
	"github.com/z5labs/humus/openapi/v3"
	"github.com/z5labs/humus/openapi/v3/problem"

	"github.com/Zaba505/markov-to-llm/graph"
	"github.com/Zaba505/markov-to-llm/markov"
)

const (
	// maxUploadMB is the largest corpus accepted.
	maxUploadMB    = 10
	maxUploadBytes = maxUploadMB << 20

	// maxRequestBytes leaves room for a multipart form's overhead around a
	// corpus of the largest size, so that the decoder is what rejects a file
	// that is too large, by its own size.
	maxRequestBytes = maxUploadBytes + 1<<16

	// maxSentenceTokens is where a walk that hasn't ended is cut off.
	maxSentenceTokens = 60

	previewRunes = 600

	// maxQueryRunes is the longest search, in characters.
	maxQueryRunes = 64

	runIDName = "runId"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static
var staticFiles embed.FS

// The API's paths, built from the root.
var (
	runsPath      = openapi.Root().Lit("runs")
	runPath       = runsPath.Param(runIDParam)
	chainPath     = runPath.Lit("chain")
	tokensPath    = runPath.Lit("tokens")
	sentencesPath = runPath.Lit("sentences")
)

// The API's parameters.
var (
	runIDParam = openapi.PathParam[string](runIDName).
			Description("The run, as the page was given it when its corpus was uploaded.").
			Pattern(regexp.MustCompile(`^[0-9a-f]{16}$`))

	limitParam = openapi.QueryParam[int]("limit").
			Description("How many of the most frequent tokens to draw.").
			Minimum(minLimit).
			Maximum(maxLimit).
			Default(defaultLimit)

	tokenParam = openapi.QueryParam[string]("token").
			Description("The token to select. A corpus's token, or empty for none.").
			MaxLength(markov.MaxTokenRunes)

	queryParam = openapi.QueryParam[string]("q").
			Description("The start of the tokens to find.").
			MaxLength(maxQueryRunes)
)

// InvalidCorpus answers an upload that has nothing to build a chain from.
type InvalidCorpus struct{ problem.Details }

// InvalidToken answers a token that can't be selected, or can't start a sentence.
type InvalidToken struct{ problem.Details }

// RunNotFound answers a run the server doesn't have, or no longer has.
type RunNotFound struct{ problem.Details }

// api is the state and the handlers of the operations.
type api struct {
	runs *runStore
}

// New returns the service's handler. Its error names every mistake in the
// templates or in the operations' declarations.
func New(s *humus.Setup) (http.Handler, error) {
	templates, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return newHandler(s, templates)
}

func parseTemplates() (*template.Template, error) {
	templates, err := template.New("").Funcs(template.FuncMap{
		"percent": graph.Percent,
		"count":   countOf,
		"coord":   func(v float64) string { return fmt.Sprintf("%.1f", v) },
	}).ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return templates, nil
}

func newHandler(s *humus.Setup, templates *template.Template) (http.Handler, error) {
	a := &api{runs: newRunStore()}
	notice := htmlEncoder[problem.Details](templates, "notice")
	runNotFound := openapi.Error[RunNotFound](http.StatusNotFound, htmlEncoder[RunNotFound](templates, "notice"))
	invalidToken := openapi.Error[InvalidToken](http.StatusUnprocessableEntity, htmlEncoder[InvalidToken](templates, "notice"))
	invalidCorpus := openapi.Error[InvalidCorpus](http.StatusUnprocessableEntity, htmlEncoder[InvalidCorpus](templates, "notice"))

	operations := []openapi.Operation{
		openapi.Get(openapi.Root(), a.getPage, htmlEncoder[pageView](templates, "page"),
			openapi.OperationID("getPage"),
			openapi.Summary("The page, with nothing uploaded yet"),
		),
		openapi.Post(runsPath, a.createRun, newUploadDecoder(maxUploadBytes), htmlEncoder[runView](templates, "run"),
			openapi.OperationID("createRun"),
			openapi.Summary("Build a chain from a corpus"),
			openapi.Status(http.StatusCreated),
			invalidCorpus,
		),
		openapi.Get(chainPath, a.getChain, htmlEncoder[chainView](templates, "chainResponse"),
			openapi.OperationID("getChain"),
			openapi.Summary("Draw a run's chain, with a token selected"),
			openapi.Query(limitParam),
			openapi.Query(tokenParam),
			runNotFound,
			invalidToken,
		),
		openapi.Get(tokensPath, a.searchTokens, htmlEncoder[chipsView](templates, "chips"),
			openapi.OperationID("searchTokens"),
			openapi.Summary("Find the tokens that can start a sentence"),
			openapi.Query(queryParam),
			runNotFound,
		),
		openapi.Post(sentencesPath, a.createSentence, newSentenceDecoder, htmlEncoder[sentenceView](templates, "sentence"),
			openapi.OperationID("createSentence"),
			openapi.Summary("Generate a sentence from a starting token"),
			runNotFound,
			invalidToken,
		),
		openapi.Document(openapi.Root().Lit("openapi.json")),
	}

	service, err := openapi.NewAPI(s,
		openapi.Info{Title: "Markov chain explorer", Version: "0.1.0"},
		openapi.Unmatched{
			NotFound:         problem.Handler(http.StatusNotFound, notice),
			MethodNotAllowed: problem.Handler(http.StatusMethodNotAllowed, notice),
		},
		openapi.RequestErrors(requestProblem, notice),
		openapi.CatchAll(http.StatusInternalServerError, serverProblem, notice),
		operations,
	)
	if err != nil {
		return nil, fmt.Errorf("build API: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.FileServerFS(staticFiles))
	mux.Handle("/", http.MaxBytesHandler(service, maxRequestBytes))

	// Every other site the browser has open can send forms here too.
	crossOrigin := http.NewCrossOriginProtection()
	crossOrigin.SetDenyHandler(problem.Handler(http.StatusForbidden, notice))
	return crossOrigin.Handler(mux), nil
}

func (a *api) getPage(context.Context, openapi.Request[openapi.EmptyBody]) (pageView, error) {
	return pageView{MaxUploadMB: maxUploadMB, RunsURL: runsPath.String()}, nil
}

func (a *api) createRun(_ context.Context, req openapi.Request[upload]) (runView, error) {
	u := req.Body()
	if !utf8.Valid(u.Data) {
		return runView{}, invalidCorpus("This file isn't UTF-8 text.")
	}
	text := string(u.Data)
	chain := markov.Build(markov.Tokenize(text))
	if chain.Tokens() == 0 {
		return runView{}, invalidCorpus("This file has no words in it.")
	}

	preview, isPart := previewOf(text)
	r := &run{name: u.Name, preview: preview, previewIsPart: isPart, chain: chain, graph: graph.NewBuilder(chain)}
	a.runs.add(r)
	return newRunView(r), nil
}

func (a *api) getChain(_ context.Context, req openapi.Request[openapi.EmptyBody]) (chainView, error) {
	r, err := a.run(openapi.ParamFrom(req, runIDParam))
	if err != nil {
		return chainView{}, err
	}
	token := openapi.ParamFrom(req, tokenParam)
	if token != "" && !r.chain.CanContinue(token) {
		return chainView{}, unselectableToken(token)
	}
	return newChainView(r, openapi.ParamFrom(req, limitParam), token), nil
}

func (a *api) searchTokens(_ context.Context, req openapi.Request[openapi.EmptyBody]) (chipsView, error) {
	r, err := a.run(openapi.ParamFrom(req, runIDParam))
	if err != nil {
		return chipsView{}, err
	}
	return newChipsView(r, openapi.ParamFrom(req, queryParam)), nil
}

func (a *api) createSentence(_ context.Context, req openapi.Request[sentenceForm]) (sentenceView, error) {
	r, err := a.run(openapi.ParamFrom(req, runIDParam))
	if err != nil {
		return sentenceView{}, err
	}
	token := req.Body().Token
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	s, err := r.chain.Generate(token, rng, maxSentenceTokens)
	if err != nil {
		return sentenceView{}, invalidStart(token)
	}
	return newSentenceView(s, maxSentenceTokens), nil
}

func (a *api) run(id string) (*run, error) {
	r, ok := a.runs.get(id)
	if !ok {
		return nil, RunNotFound{problem.Details{
			Title:  "This run has expired",
			Status: http.StatusNotFound,
			Detail: fmt.Sprintf("The server keeps its %d most recently used runs until it restarts. Upload the corpus again to keep exploring it.", maxRuns),
		}}
	}
	return r, nil
}

func invalidCorpus(detail string) InvalidCorpus {
	return InvalidCorpus{problem.Details{
		Title:  "That file can't be used",
		Status: http.StatusUnprocessableEntity,
		Detail: detail,
	}}
}

func unselectableToken(token string) InvalidToken {
	return InvalidToken{problem.Details{
		Title:  "That token can't be selected",
		Status: http.StatusUnprocessableEntity,
		Detail: fmt.Sprintf("%q isn't in the corpus, or it only ever ends a sentence.", token),
	}}
}

func invalidStart(token string) InvalidToken {
	return InvalidToken{problem.Details{
		Title:  "That token can't start a sentence",
		Status: http.StatusUnprocessableEntity,
		Detail: fmt.Sprintf("%q isn't in the corpus, or it's punctuation.", token),
	}}
}

// requestProblem answers a rejected request, with a message for people when the
// reason is the size of the file.
func requestProblem(e *openapi.RequestError) problem.Details {
	d := problem.FromRequestError(e)
	if e.Status == http.StatusRequestEntityTooLarge {
		d.Title = "That file is too large"
		d.Detail = fmt.Sprintf("The largest corpus this can use is %d MB.", maxUploadMB)
	}
	return d
}

// serverProblem answers what nothing else does. It never carries the error's
// text, which the API records instead.
func serverProblem(error) problem.Details {
	return problem.Details{
		Title:  "Something went wrong",
		Status: http.StatusInternalServerError,
		Detail: "The server couldn't answer that request. Its logs have the details.",
	}
}

// previewOf returns the start of text, and whether that is only part of it.
func previewOf(text string) (string, bool) {
	runes := 0
	for i := range text {
		if runes == previewRunes {
			return strings.TrimSpace(text[:i]), true
		}
		runes++
	}
	return strings.TrimSpace(text), false
}

// countOf is n and a noun, which is plural unless n is 1.
func countOf(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// runURL is the URL of path for a run. A run's ID needs no escaping.
func runURL(path openapi.Path, runID string) string {
	return strings.Replace(path.String(), "{"+runIDName+"}", runID, 1)
}

// formatWidth is a probability as a bar's width, in percent.
func formatWidth(p float64) string { return fmt.Sprintf("%.1f", p*100) }
