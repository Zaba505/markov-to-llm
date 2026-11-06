package app_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/z5labs/bedrock"
	"github.com/z5labs/bedrock/bedrocktest"
	"github.com/z5labs/humus"
	"github.com/z5labs/humus/httpserver/httpservertest"
	"github.com/z5labs/humus/humustest"
	"github.com/z5labs/humus/telemetry"

	"github.com/Zaba505/markov-to-llm/app"
	"github.com/Zaba505/markov-to-llm/markov"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	addr = "localhost:8443"
	base = "https://" + addr

	// onePath has one possible walk from "one": each token has one successor.
	onePath = "One two, three."
)

// service is the whole App, running on a test Host.
type service struct {
	t      *testing.T
	client *http.Client
	p      *bedrocktest.Process
	status <-chan bedrock.ExitStatus
}

type response struct {
	status      int
	contentType string
	body        string
}

// start runs the App with a certificate from a test CA, on the options main
// passes, with the test's Host and no telemetry output.
func start(t *testing.T) *service {
	t.Helper()
	ca := humustest.NewCA(t)
	cert := ca.Issue(t, "localhost")
	h := humustest.NewHost(t, humustest.WithFiles(map[string][]byte{"tls.pem": pemOf(t, cert.TLS())}))

	p, status := httpservertest.Start(t, app.Init,
		humustest.WithApp(app.Options()...),
		humustest.WithApp(humus.WithHost(h.Host()), humus.WithTelemetry(telemetry.NoopBuilder)),
		humustest.WithEnv(map[string]string{"ADDR": addr, "TLS_PEM_FILE": "tls.pem"}),
	)
	<-h.Listening(addr)
	return &service{t: t, client: httpservertest.NewClient(h, ca.Pool()), p: p, status: status}
}

// pemOf is cert as one PEM file, its chain and its private key.
func pemOf(t *testing.T, cert tls.Certificate) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, der := range cert.Certificate {
		require.NoError(t, pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	require.NoError(t, err)
	require.NoError(t, pem.Encode(&b, &pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	return b.Bytes()
}

// stop sends SIGTERM and checks that the App exits 0.
func (s *service) stop() {
	s.t.Helper()
	<-s.p.Notified(syscall.SIGTERM)
	s.p.Signal(syscall.SIGTERM)
	st := <-s.status
	require.Zero(s.t, st.Code(), "cause: %v", st.Cause())
}

func (s *service) do(req *http.Request) response {
	s.t.Helper()
	resp, err := s.client.Do(req)
	require.NoError(s.t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)
	return response{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: string(body)}
}

func (s *service) get(path string) response {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	require.NoError(s.t, err)
	return s.do(req)
}

func (s *service) postForm(path string, form url.Values) response {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
	require.NoError(s.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

func (s *service) upload(field, name string, content []byte) response {
	s.t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, err := w.CreateFormFile(field, name)
	require.NoError(s.t, err)
	_, err = part.Write(content)
	require.NoError(s.t, err)
	require.NoError(s.t, w.Close())

	req, err := http.NewRequest(http.MethodPost, base+"/runs", &b)
	require.NoError(s.t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return s.do(req)
}

// newRun uploads text and returns its run's ID and fragment.
func (s *service) newRun(text string) (id, fragment string) {
	s.t.Helper()
	r := s.upload("corpus", "corpus.txt", []byte(text))
	require.Equal(s.t, http.StatusCreated, r.status, r.body)
	return runIDOf(s.t, r.body), r.body
}

func runIDOf(t *testing.T, fragment string) string {
	t.Helper()
	m := regexp.MustCompile(`id="run-([0-9a-f]{16})"`).FindStringSubmatch(fragment)
	require.NotNil(t, m, "no run ID in %q", fragment)
	return m[1]
}

func TestInit_serves_the_page_and_its_assets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)

		page := s.get("/")
		require.Equal(t, http.StatusOK, page.status)
		require.Equal(t, "text/html; charset=utf-8", page.contentType)
		require.Contains(t, page.body, `id="upload"`)
		require.Contains(t, page.body, `id="notice"`)
		require.NotContains(t, page.body, `class="run"`)
		require.Contains(t, page.body, `"target":"#notice"`, "errors must not overwrite what is on the page")
		require.Contains(t, page.body, `hx-post="/runs"`)

		for path, want := range map[string]string{
			"/static/htmx.min.js": "javascript",
			"/static/app.css":     "text/css",
		} {
			r := s.get(path)
			require.Equal(t, http.StatusOK, r.status, path)
			require.Contains(t, r.contentType, want, path)
			require.NotEmpty(t, r.body, path)
		}

		s.stop()
	})
}

func TestInit_takes_a_corpus_through_every_step(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)

		id, fragment := s.newRun(onePath)
		require.Contains(t, fragment, "corpus.txt")
		require.Contains(t, fragment, "<dd>5</dd>", "the token count")
		require.Contains(t, fragment, `id="chain-`+id+`"`)
		require.Contains(t, fragment, `id="selection-`+id+`"`)
		require.Contains(t, fragment, `id="sentences-`+id+`"`)
		require.Contains(t, fragment, "<svg")
		require.Contains(t, fragment, `<button type="submit" disabled>`, "nothing can be generated before a token is selected")
		require.NotContains(t, fragment, "hx-swap-oob", "the first selection is in place, not out of band")
		require.Contains(t, fragment, `hx-post="/runs/`+id+`/sentences"`)
		require.Contains(t, fragment, `hx-get="/runs/`+id+`/tokens"`)
		require.Contains(t, fragment, `maxlength="64"`)
		require.Contains(t, fragment, "with up to 5 arrows")
		require.Contains(t, fragment, `/runs/`+id+`/chain?token=one`)

		chain := s.get("/runs/" + id + "/chain?token=one")
		require.Equal(t, http.StatusOK, chain.status, chain.body)
		require.Contains(t, chain.body, "has-selection")
		require.Contains(t, chain.body, `name="token" value="one"`)
		require.Contains(t, chain.body, `hx-swap-oob="true"`, "selecting updates step 3")
		require.Contains(t, chain.body, "100%")

		tokens := s.get("/runs/" + id + "/tokens?q=tw")
		require.Equal(t, http.StatusOK, tokens.status, tokens.body)
		require.Contains(t, tokens.body, ">two</button>")
		require.NotContains(t, tokens.body, ">one</button>")
		require.Contains(t, s.get("/runs/"+id+"/tokens?q=zz").body, "No tokens start like that")

		sentence := s.postForm("/runs/"+id+"/sentences", url.Values{"token": {"one"}})
		require.Equal(t, http.StatusOK, sentence.status, sentence.body)
		require.Contains(t, sentence.body, "One two, three.")
		require.Contains(t, sentence.body, "(end of sentence)", "the trace shows the walk ending")
		require.Equal(t, 5, strings.Count(sentence.body, "candidate chosen"), "one choice for each step")

		s.stop()
	})
}

func TestInit_draws_the_limit_it_is_asked_for(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		var b strings.Builder
		for i := range 60 {
			b.WriteString("word" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + " ")
		}
		id, fragment := s.newRun(b.String())
		require.Equal(t, 30, strings.Count(fragment, `transform="translate(`), "the default limit")

		r := s.get("/runs/" + id + "/chain?limit=10")
		require.Equal(t, http.StatusOK, r.status, r.body)
		require.Equal(t, 10, strings.Count(r.body, `transform="translate(`))
		require.Contains(t, r.body, `value="10"`, "the slider shows the limit")

		s.stop()
	})
}

// A repeated query parameter is a 400, so no URL in a fragment may carry limit:
// htmx adds the slider's value to every request that selects a token.
func TestInit_never_writes_limit_into_a_url(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		var b strings.Builder
		for i := range 40 {
			b.WriteString("word" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + " again ")
		}
		id, fragment := s.newRun(b.String())
		chain := s.get("/runs/" + id + "/chain?limit=20&token=again")
		tokens := s.get("/runs/" + id + "/tokens?q=wor")
		require.Contains(t, fragment, `name="limit"`, "the slider must be drawn for this to mean anything")
		require.Equal(t, http.StatusOK, chain.status, chain.body)

		urls := regexp.MustCompile(`hx-get="([^"]*)"`)
		found := 0
		for _, body := range []string{fragment, chain.body, tokens.body} {
			for _, m := range urls.FindAllStringSubmatch(body, -1) {
				found++
				require.NotContains(t, m[1], "limit=", "in %q", m[1])
			}
		}
		require.Greater(t, found, 20)

		repeated := s.get("/runs/" + id + "/chain?limit=20&limit=30")
		require.Equal(t, http.StatusBadRequest, repeated.status)

		s.stop()
	})
}

func TestInit_makes_tokens_that_need_escaping_selectable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)

		id, fragment := s.newRun("Don't stop, ok.")
		require.Contains(t, fragment, "/chain?token=don%27t")
		require.Contains(t, fragment, "/chain?token=%2C", "a comma is a token too")

		r := s.get("/runs/" + id + "/chain?token=%2C")
		require.Equal(t, http.StatusOK, r.status, r.body)
		require.Contains(t, r.body, "After <code>,</code>", "a comma's odds are worth seeing")
		require.Contains(t, r.body, `name="token" value=""`, "but a sentence can't start from it")
		require.Contains(t, r.body, `<button type="submit" disabled>`)
		require.Contains(t, r.body, "<code>,</code> can't start a sentence")

		s.stop()
	})
}

func TestInit_offers_only_words_as_starting_tokens(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		id, fragment := s.newRun("One, two, three, four, five.")
		require.NotContains(t, fragment, `class="chip" hx-get="/runs/`+id+`/chain?token=%2C"`)
		require.Contains(t, fragment, ">one</button>")

		tokens := s.get("/runs/" + id + "/tokens?q=%2C")
		require.Equal(t, http.StatusOK, tokens.status, tokens.body)
		require.Contains(t, tokens.body, "No tokens start like that")

		s.stop()
	})
}

func TestInit_makes_long_tokens_selectable_or_drops_them(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		// The longest token there can be, with every rune as long as a rune gets.
		longest := strings.Repeat("𝐱", markov.MaxTokenRunes)
		tooLong := strings.Repeat("y", markov.MaxTokenRunes+1)

		id, fragment := s.newRun("Short " + longest + " " + tooLong + " words.")
		require.Contains(t, fragment, "<dd>4</dd>", "the too-long word isn't a token, so only short, the longest, words and .")
		require.NotContains(t, fragment, "token="+tooLong)
		require.Contains(t, fragment, "token="+url.QueryEscape(longest))

		r := s.get("/runs/" + id + "/chain?" + url.Values{"token": {longest}}.Encode())
		require.Equal(t, http.StatusOK, r.status, r.body)
		require.Contains(t, r.body, "has-selection")

		rejected := s.get("/runs/" + id + "/chain?token=" + tooLong)
		require.Equal(t, http.StatusBadRequest, rejected.status, rejected.body)

		s.stop()
	})
}

func TestInit_finds_a_token_typed_with_a_curly_apostrophe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		id, _ := s.newRun("Don’t stop.")

		r := s.get("/runs/" + id + "/tokens?" + url.Values{"q": {"don’"}}.Encode())
		require.Equal(t, http.StatusOK, r.status, r.body)
		require.Contains(t, r.body, ">don&#39;t</button>")

		s.stop()
	})
}

func TestInit_shows_near_certainty_as_less_than_certain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		id, _ := s.newRun(strings.Repeat("Hub likely.\n", 996) + strings.Repeat("Hub rare.\n", 4))

		r := s.get("/runs/" + id + "/chain?token=hub")
		require.Equal(t, http.StatusOK, r.status, r.body)
		require.Contains(t, r.body, `<td class="pct">&gt;99%</td>`)
		require.Contains(t, r.body, `<td class="pct">&lt;1%</td>`)
		require.NotContains(t, r.body, ">100%<")

		s.stop()
	})
}

func TestInit_rejects_forms_from_other_sites(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		id, _ := s.newRun(onePath)

		send := func(header, value string) response {
			req, err := http.NewRequest(http.MethodPost, base+"/runs/"+id+"/sentences", strings.NewReader("token=one"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set(header, value)
			return s.do(req)
		}

		for header, value := range map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"} {
			r := send(header, value)
			require.Equal(t, http.StatusForbidden, r.status, "%s: %s", header, r.body)
			require.Equal(t, "text/html; charset=utf-8", r.contentType)
			require.True(t, strings.HasPrefix(r.body, "<p><strong>"), "%q isn't a notice", r.body)
		}

		for header, value := range map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": base} {
			r := send(header, value)
			require.Equal(t, http.StatusOK, r.status, "%s: %s", header, r.body)
		}

		s.stop()
	})
}

func TestInit_lets_the_keyboard_activate_graph_nodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		_, fragment := s.newRun(onePath)

		require.Contains(t, fragment, `role="button"`)
		require.Contains(t, fragment, `key=='Enter'`)
		require.Contains(t, fragment, `key==' '`, "a button responds to Space too")
		require.Contains(t, fragment, `hx-on:keydown="if (event.key === ' ') event.preventDefault()"`, "Space must not scroll the page")

		s.stop()
	})
}

func TestInit_counts_in_the_singular(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		var b strings.Builder
		for i := range 13 {
			b.WriteString("Hub word" + string(rune('a'+i)) + ".\n")
		}
		id, fragment := s.newRun(b.String())
		require.Contains(t, fragment, "appears 1 time")
		require.Contains(t, fragment, `title="Appears 1 time"`)
		require.NotContains(t, fragment, "1 times")

		r := s.get("/runs/" + id + "/chain?token=hub")
		require.Equal(t, http.StatusOK, r.status, r.body)
		require.Contains(t, r.body, "Appears 13 times.")
		require.Contains(t, r.body, "and 1 less likely token.")

		one := s.get("/runs/" + id + "/chain?token=worda")
		require.Contains(t, one.body, "Appears 1 time.")

		s.stop()
	})
}

func TestInit_answers_every_failure_with_a_notice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		id, _ := s.newRun(onePath)
		unknown := "0123456789abcdef"

		tests := []struct {
			name   string
			do     func() response
			status int
			want   string
		}{
			{
				name:   "empty file",
				do:     func() response { return s.upload("corpus", "empty.txt", nil) },
				status: http.StatusUnprocessableEntity,
				want:   "no words",
			},
			{
				name:   "file with no words",
				do:     func() response { return s.upload("corpus", "symbols.txt", []byte("*** ---\n")) },
				status: http.StatusUnprocessableEntity,
				want:   "no words",
			},
			{
				name:   "file that isn't UTF-8",
				do:     func() response { return s.upload("corpus", "binary.dat", []byte{0xff, 0xfe, 0x00, 'x'}) },
				status: http.StatusUnprocessableEntity,
				want:   "UTF-8",
			},
			{
				name:   "file over the limit",
				do:     func() response { return s.upload("corpus", "big.txt", bytes.Repeat([]byte("word "), 2<<20+1)) },
				status: http.StatusRequestEntityTooLarge,
				want:   "10 MB",
			},
			{
				name:   "request far over the limit",
				do:     func() response { return s.upload("corpus", "huge.txt", bytes.Repeat([]byte("word "), 5<<20)) },
				status: http.StatusRequestEntityTooLarge,
				want:   "10 MB",
			},
			{
				name:   "no file",
				do:     func() response { return s.upload("other", "x.txt", []byte("words")) },
				status: http.StatusBadRequest,
				want:   "choose a file",
			},
			{
				name:   "upload that isn't a multipart form",
				do:     func() response { return s.postForm("/runs", url.Values{"corpus": {"words"}}) },
				status: http.StatusUnsupportedMediaType,
				want:   "multipart/form-data",
			},
			{
				name:   "selecting a token not in the corpus",
				do:     func() response { return s.get("/runs/" + id + "/chain?token=missing") },
				status: http.StatusUnprocessableEntity,
				want:   "missing",
			},
			{
				name:   "selecting a terminator",
				do:     func() response { return s.get("/runs/" + id + "/chain?token=.") },
				status: http.StatusUnprocessableEntity,
				want:   "can&#39;t be selected",
			},
			{
				name: "generating from a token not in the corpus",
				do: func() response {
					return s.postForm("/runs/"+id+"/sentences", url.Values{"token": {"missing"}})
				},
				status: http.StatusUnprocessableEntity,
				want:   "missing",
			},
			{
				name: "generating from a terminator",
				do: func() response {
					return s.postForm("/runs/"+id+"/sentences", url.Values{"token": {"."}})
				},
				status: http.StatusUnprocessableEntity,
				want:   "can&#39;t start a sentence",
			},
			{
				name: "generating from a clause mark",
				do: func() response {
					return s.postForm("/runs/"+id+"/sentences", url.Values{"token": {","}})
				},
				status: http.StatusUnprocessableEntity,
				want:   "can&#39;t start a sentence",
			},
			{
				name:   "generating with no token",
				do:     func() response { return s.postForm("/runs/"+id+"/sentences", nil) },
				status: http.StatusBadRequest,
				want:   "choose a starting token",
			},
			{
				name:   "drawing a run the server doesn't have",
				do:     func() response { return s.get("/runs/" + unknown + "/chain") },
				status: http.StatusNotFound,
				want:   "expired",
			},
			{
				name:   "searching a run the server doesn't have",
				do:     func() response { return s.get("/runs/" + unknown + "/tokens?q=a") },
				status: http.StatusNotFound,
				want:   "expired",
			},
			{
				name: "generating from a run the server doesn't have",
				do: func() response {
					return s.postForm("/runs/"+unknown+"/sentences", url.Values{"token": {"one"}})
				},
				status: http.StatusNotFound,
				want:   "expired",
			},
			{
				name:   "a malformed run ID",
				do:     func() response { return s.get("/runs/nope/chain") },
				status: http.StatusBadRequest,
				want:   "runId",
			},
			{
				name:   "a limit below the minimum",
				do:     func() response { return s.get("/runs/" + id + "/chain?limit=9") },
				status: http.StatusBadRequest,
				want:   "limit",
			},
			{
				name:   "a limit above the maximum",
				do:     func() response { return s.get("/runs/" + id + "/chain?limit=101") },
				status: http.StatusBadRequest,
				want:   "limit",
			},
			{
				name:   "a limit that isn't a number",
				do:     func() response { return s.get("/runs/" + id + "/chain?limit=lots") },
				status: http.StatusBadRequest,
				want:   "limit",
			},
			{
				name:   "an unknown path",
				do:     func() response { return s.get("/nope") },
				status: http.StatusNotFound,
				want:   "Not Found",
			},
			{
				name: "an unknown method",
				do: func() response {
					req, err := http.NewRequest(http.MethodDelete, base+"/runs", nil)
					require.NoError(t, err)
					return s.do(req)
				},
				status: http.StatusMethodNotAllowed,
				want:   "Method Not Allowed",
			},
		}
		for _, tt := range tests {
			r := tt.do()
			require.Equal(t, tt.status, r.status, "%s: %s", tt.name, r.body)
			require.Equal(t, "text/html; charset=utf-8", r.contentType, tt.name)
			require.Contains(t, r.body, tt.want, tt.name)
			require.True(t, strings.HasPrefix(r.body, "<p><strong>"), "%s: %q isn't a notice", tt.name, r.body)
		}

		s.stop()
	})
}

func TestInit_accepts_a_file_of_exactly_the_limit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		content := bytes.Repeat([]byte("word "), 2<<20)
		require.Len(t, content, 10<<20)

		r := s.upload("corpus", "limit.txt", content)
		require.Equal(t, http.StatusCreated, r.status, r.body)

		s.stop()
	})
}

func TestInit_escapes_what_it_is_given(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)

		r := s.upload("corpus", `<img src=x onerror=alert(1)>.txt`, []byte(`Fine <script>alert(1)</script> words.`))
		require.Equal(t, http.StatusCreated, r.status, r.body)
		require.NotContains(t, r.body, "<img")
		require.NotContains(t, r.body, "<script")
		require.Contains(t, r.body, "&lt;script&gt;", "the preview shows the text as it was")

		missing := s.get("/runs/" + runIDOf(t, r.body) + "/chain?token=%3Cscript%3E")
		require.Equal(t, http.StatusUnprocessableEntity, missing.status)
		require.NotContains(t, missing.body, "<script")

		s.stop()
	})
}

func TestInit_keeps_the_most_recently_used_runs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)

		oldest, _ := s.newRun(onePath)
		used, _ := s.newRun(onePath)
		for range 6 {
			s.newRun(onePath)
		}
		require.Equal(t, http.StatusOK, s.get("/runs/"+used+"/chain").status)

		// The store is full: the next run evicts the oldest one, not the one
		// that has just been used.
		s.newRun(onePath)
		require.Equal(t, http.StatusNotFound, s.get("/runs/"+oldest+"/chain").status)

		s.newRun(onePath)
		require.Equal(t, http.StatusOK, s.get("/runs/"+used+"/chain").status)

		s.stop()
	})
}

func TestInit_documents_its_operations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)

		r := s.get("/openapi.json")
		require.Equal(t, http.StatusOK, r.status)
		var indented bytes.Buffer
		require.NoError(t, json.Indent(&indented, []byte(r.body), "", "  "))
		indented.WriteByte('\n')

		golden := filepath.Join("testdata", "openapi.json")
		if *update {
			require.NoError(t, os.WriteFile(golden, indented.Bytes(), 0o644))
		}
		want, err := os.ReadFile(golden)
		require.NoError(t, err)
		require.Equal(t, string(want), indented.String(), "run go test ./app -update if this change is intended")

		s.stop()
	})
}

func TestInit_generates_a_certificate_without_TLS_PEM_FILE(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := humustest.NewHost(t)
		p, status := httpservertest.Start(t, app.Init,
			humustest.WithApp(app.Options()...),
			humustest.WithApp(humus.WithHost(h.Host()), humus.WithTelemetry(telemetry.NoopBuilder)),
		)
		<-h.Listening("localhost:8443")

		client := h.HTTPClient()
		var peer *tls.ConnectionState
		tr := client.Transport.(*http.Transport)
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, // the point of the test is to look at what it is sent
			VerifyConnection: func(cs tls.ConnectionState) error {
				peer = &cs
				return nil
			},
		}
		resp, err := client.Get(base + "/")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		require.Equal(t, http.StatusOK, resp.StatusCode)

		require.NotNil(t, peer)
		cert := peer.PeerCertificates[0]
		require.NoError(t, cert.VerifyHostname("localhost"))
		require.NoError(t, cert.VerifyHostname("127.0.0.1"))
		require.NoError(t, cert.VerifyHostname("::1"))

		<-p.Notified(syscall.SIGTERM)
		p.Signal(syscall.SIGTERM)
		st := <-status
		require.Zero(t, st.Code(), "cause: %v", st.Cause())
	})
}

func TestInit_logs_the_url_to_open(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := humustest.NewHost(t)
		p, status := httpservertest.Start(t, app.Init,
			humustest.WithApp(app.Options()...),
			humustest.WithApp(humus.WithHost(h.Host())),
			humustest.WithEnv(map[string]string{"ADDR": ":9443"}),
		)
		<-h.Listening("localhost:9443")

		<-p.Notified(syscall.SIGTERM)
		p.Signal(syscall.SIGTERM)
		st := <-status
		require.Zero(t, st.Code(), "cause: %v", st.Cause())
		require.Contains(t, p.Stdout(), `"url":"https://localhost:9443"`)
		require.Contains(t, p.Stdout(), "self-signed")
	})
}

func TestInit_fails_on_bad_settings_naming_each_variable(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			name: "bad address",
			env:  map[string]string{"ADDR": "not an address"},
			want: []string{"config ADDR"},
		},
		{
			name: "missing certificate file",
			env:  map[string]string{"TLS_PEM_FILE": "missing.pem"},
			want: []string{"config TLS_PEM_FILE"},
		},
		{
			name: "certificate file that isn't one",
			env:  map[string]string{"TLS_PEM_FILE": "garbage.pem"},
			want: []string{"config TLS_PEM_FILE"},
		},
		{
			name: "both",
			env:  map[string]string{"ADDR": "not an address", "TLS_PEM_FILE": "missing.pem"},
			want: []string{"config ADDR", "config TLS_PEM_FILE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := humustest.NewHost(t, humustest.WithFiles(map[string][]byte{"garbage.pem": []byte("not a certificate")}))
				_, status := httpservertest.Start(t, app.Init,
					humustest.WithApp(app.Options()...),
					humustest.WithApp(humus.WithHost(h.Host()), humus.WithTelemetry(telemetry.NoopBuilder)),
					humustest.WithEnv(tt.env),
				)

				st := <-status
				require.Equal(t, humus.ExitConfig, st.Code(), "cause: %v", st.Cause())
				for _, want := range tt.want {
					require.ErrorContains(t, st.Cause(), want)
				}
			})
		})
	}
}

func TestOptions_remove_the_drain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := start(t)
		began := time.Now()

		s.stop()

		// Fake time only moves when something waits on it, so a drain would
		// show up here as its whole 5s.
		require.Less(t, time.Since(began), time.Second, "Ctrl-C should exit at once")
	})
}
