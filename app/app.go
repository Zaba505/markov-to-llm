// Package app builds the service: its settings, its telemetry and its one HTTP
// Service.
package app

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/z5labs/bedrock"
	"github.com/z5labs/bedrock/config"
	"github.com/z5labs/humus"
	"github.com/z5labs/humus/httpserver"
	"github.com/z5labs/humus/telemetry"
	"go.opentelemetry.io/contrib/bridges/otelslog"

	"github.com/Zaba505/markov-to-llm/endpoint"
)

const (
	serviceName = "markov-to-llm"
	defaultAddr = "localhost:8443"

	// requestTimeout bounds reading and answering a request. It is well above
	// httpserver's defaults, so that the largest upload and the chain built
	// from it can't reach it. Nothing but this machine connects by default.
	requestTimeout = 30 * time.Second
)

// Options returns what main passes to httpserver.Main, so that a test runs with
// them too.
//
// There is no drain: it exists to let a load balancer stop sending traffic, and
// here it would only make Ctrl-C take 5s longer.
func Options() []humus.Option {
	return []humus.Option{
		humus.WithTelemetry(BuildTelemetry),
		humus.WithDrain(nil),
	}
}

// BuildTelemetry names the service, and otherwise keeps telemetry's defaults:
// JSON logs on standard output, and nothing exported.
func BuildTelemetry(ctx context.Context, p bedrock.Process, dial telemetry.Dialer) (telemetry.SDK, error) {
	s := telemetry.DefaultSettings()
	s.ServiceName = serviceName
	return telemetry.New(ctx, s, dial, p.Stdout)
}

// Init sources the service's settings and returns its HTTP Service.
//
// httpserver only serves HTTPS, so that a certificate is always required. To
// keep "go run ." working with no setup, a certificate is generated when
// TLS_PEM_FILE doesn't name one.
func Init(ctx context.Context, s *humus.Setup) ([]humus.Service, error) {
	generated, err := generateTLS(time.Now())
	if err != nil {
		return nil, err
	}

	settings, err := readSettings(ctx, s, generated)
	if err != nil {
		return nil, err // exit 78, naming every bad variable
	}

	handler, err := endpoint.New(s)
	if err != nil {
		return nil, err
	}

	svc, err := httpserver.NewService(s, settings, handler)
	if err != nil {
		return nil, err
	}

	logger := otelslog.NewLogger(serviceName, otelslog.WithLoggerProvider(s.Telemetry.LoggerProvider))
	logger.InfoContext(ctx, "open this in a browser", "url", browserURL(settings.Addr, svc.Addr()))
	if settings.TLS == generated {
		logger.InfoContext(ctx, "using a generated self-signed certificate, so the browser will warn about it; set TLS_PEM_FILE to use your own")
	}
	return []humus.Service{svc}, nil
}

func readSettings(ctx context.Context, s *humus.Setup, generated *tls.Config) (httpserver.Settings, error) {
	env := func(name string) config.Value[string] { return config.Env(s.Process.Environment, name) }

	return config.Struct(func(r *config.Resolver) httpserver.Settings {
		settings := httpserver.DefaultSettings()
		settings.ReadTimeout = requestTimeout
		settings.WriteTimeout = requestTimeout
		settings.Addr = r.Get(env("ADDR").Default(defaultAddr).Validate(httpserver.CheckAddr))
		settings.TLS = r.Get(env("TLS_PEM_FILE").Parse(readFile(s.Host.FS)).Parse(serverTLS).Default(generated).Validate(httpserver.CheckTLS))
		return settings
	}).Read(ctx)
}

// readFile returns a parser for config.Value.Parse that reads the whole file a
// variable names, through fsys.
func readFile(fsys humus.FS) func(name string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		f, err := fsys.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return io.ReadAll(f)
	}
}

// serverTLS returns a server's TLS config for a PEM file that holds its
// certificate chain and its private key.
func serverTLS(pem []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(pem, pem)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

// browserURL returns the URL to open for a service configured with addr, that
// is listening on listening. An address that means every interface is opened
// as localhost, and the port is the listener's, in case addr's was 0.
func browserURL(addr string, listening net.Addr) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "https://" + addr
	}
	if _, actual, err := net.SplitHostPort(listening.String()); err == nil {
		port = actual
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return "https://" + net.JoinHostPort(host, port)
}
