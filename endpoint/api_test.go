package endpoint

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
	"github.com/z5labs/humus"
	"github.com/z5labs/humus/httpserver"
	"github.com/z5labs/humus/httpserver/httpservertest"
	"github.com/z5labs/humus/humustest"
)

// A template that fails is the one unplanned failure a test can cause from the
// outside. It must reach the page as a notice with no detail of the failure,
// and the logs must have the detail.
func TestCatchAll_answers_a_failing_template_with_a_notice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		templates, err := parseTemplates()
		require.NoError(t, err)
		_, err = templates.New("page").Parse(`half a page {{.NoSuchField}}`)
		require.NoError(t, err)

		ca := humustest.NewCA(t)
		cert := ca.Issue(t, "localhost")
		init := func(_ context.Context, s *humus.Setup) ([]humus.Service, error) {
			handler, err := newHandler(s, templates)
			if err != nil {
				return nil, err
			}
			settings := httpserver.DefaultSettings()
			settings.Addr = "localhost:8443"
			settings.TLS = &tls.Config{Certificates: []tls.Certificate{cert.TLS()}}
			svc, err := httpserver.NewService(s, settings, handler)
			if err != nil {
				return nil, err
			}
			return []humus.Service{svc}, nil
		}

		h := humustest.NewHost(t)
		p, status := httpservertest.Start(t, init, humustest.WithApp(humus.WithHost(h.Host())))
		<-h.Listening("localhost:8443")

		resp, err := httpservertest.NewClient(h, ca.Pool()).Get("https://localhost:8443/")
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.True(t, strings.HasPrefix(string(body), "<p><strong>Something went wrong"), "body = %q", body)
		require.NotContains(t, string(body), "half a page")
		require.NotContains(t, string(body), "NoSuchField", "the failure's detail is for the logs")

		<-p.Notified(syscall.SIGTERM)
		p.Signal(syscall.SIGTERM)
		st := <-status
		require.Zero(t, st.Code(), "cause: %v", st.Cause())
		require.Contains(t, p.Stdout(), "request failed")
		require.Contains(t, p.Stdout(), "NoSuchField")
	})
}

func TestPreviewOf(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		want   string
		isPart bool
	}{
		{name: "short text is whole", text: "  a b \n", want: "a b"},
		{name: "exactly the limit is whole", text: strings.Repeat("é", previewRunes), want: strings.Repeat("é", previewRunes)},
		{name: "longer text is cut on a rune", text: strings.Repeat("é", previewRunes+1), want: strings.Repeat("é", previewRunes), isPart: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isPart := previewOf(tt.text)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.isPart, isPart)
		})
	}
}

func TestCountOf(t *testing.T) {
	require.Equal(t, "0 times", countOf(0, "time"))
	require.Equal(t, "1 time", countOf(1, "time"))
	require.Equal(t, "2 times", countOf(2, "time"))
	require.Equal(t, "1 less likely token", countOf(1, "less likely token"))
}

func TestRunURL(t *testing.T) {
	require.Equal(t, "/runs/ab/sentences", runURL(sentencesPath, "ab"))
	require.Equal(t, "/runs/ab/tokens", runURL(tokensPath, "ab"))
}

func TestChainURL(t *testing.T) {
	require.Equal(t, "/runs/ab/chain", chainURL("ab", ""))
	require.Equal(t, "/runs/ab/chain?token=don%27t", chainURL("ab", "don't"))
	require.Equal(t, "/runs/ab/chain?token=%C3%BCber", chainURL("ab", "über"))
}
