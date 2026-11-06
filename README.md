# From Markov chains to LLMs

This repo provides a locally runnable service for constructing and visualizing
a Markov chain from a given corpus. Its purpose is to aid in explaining token
prediction and to form an intuitive explanation of how LLMs function.

You upload some text, see which tokens tend to follow which, and pick a starting
token to watch a new sentence get built one prediction at a time. Every step
stays on one scrolling page, so you can look back at the corpus, the chain and
earlier sentences as you go.

## Running it

You need Go 1.27 or later.

```sh
go run .
```

Open <https://localhost:8443> and upload a UTF-8 text file of up to 10 MB.
[`testdata/sample.txt`](testdata/sample.txt) is a small one to start with.

The server only speaks HTTPS, because [humus](https://github.com/z5labs/humus)
requires it. Unless you give it a certificate, it generates a self-signed one
each time it starts, so your browser shows a warning that you'll need to accept,
on your first visit and again after each restart. To avoid the warning, use a
certificate your browser trusts, such as one from
[mkcert](https://github.com/FiloSottile/mkcert):

```sh
mkcert -install
mkcert -cert-file cert.pem -key-file key.pem localhost
cat cert.pem key.pem > tls.pem
TLS_PEM_FILE=tls.pem go run .
```

### Settings

| Variable | Default | Meaning |
|---|---|---|
| `ADDR` | `localhost:8443` | The address to listen on, as `host:port`. |
| `TLS_PEM_FILE` | A generated self-signed certificate | A PEM file with the certificate chain and the private key. |

## What it does

- **Tokens** are lowercase words, the punctuation marks `, ; :`, and sentence
  terminators (runs of `. ! ?`). Other symbols are dropped. A sentence ends at a
  terminator or a blank line, so an abbreviation like `Mr.` ends one too.
- **The chain** is first order: the next token depends only on the current
  token. Each sentence's last token moves to an explicit end-of-sentence state,
  much like the end-of-sequence token of an LLM.
- **The graph** shows the most frequent tokens (10 to 100, adjustable), with up
  to 5 arrows out of each. Select any token, by clicking it or searching for it,
  to see the full odds of what comes next.
- **Generating** samples each next token by its probability until the walk
  reaches the end-of-sentence state or 60 tokens. Each sentence can expand to
  show the distribution behind each step.

Refreshing the page clears it. The page is the history: the server only keeps
the chains of the 8 most recently used runs, in memory, and no sentences.

## Developing

```sh
go test ./...
```

| Path | What's there |
|---|---|
| `main.go`, `app/` | The humus service: settings, certificate and telemetry. |
| `endpoint/` | The `openapi/v3` operations, HTML codecs, templates and static assets. |
| `markov/` | Tokenizing, building the chain and generating sentences. |
| `graph/` | Choosing what to draw, and the force-directed layout. |

The API's OpenAPI document is served at `/openapi.json`. A change to an operation
shows up as a change to `app/testdata/openapi.json`, which
`go test ./app -update` rewrites.

`endpoint/static/htmx.min.js` is [htmx](https://htmx.org) 2.0.11, unmodified.
