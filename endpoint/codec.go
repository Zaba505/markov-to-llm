package endpoint

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/z5labs/humus/openapi/v3"
)

const (
	htmlMediaType = "text/html; charset=utf-8"
	corpusField   = "corpus"
)

// htmlCodec writes T by rendering one named template with it.
type htmlCodec[T any] struct {
	templates *template.Template
	name      string
}

// htmlEncoder returns the function an operation takes for its encoder: T as the
// named template.
func htmlEncoder[T any](templates *template.Template, name string) func() openapi.Encoder[T] {
	return func() openapi.Encoder[T] { return htmlCodec[T]{templates: templates, name: name} }
}

func (htmlCodec[T]) MediaType() string { return htmlMediaType }

func (htmlCodec[T]) Schema() (openapi.Schema, error) { return nil, nil }

// Encode renders into a buffer, before the status is written, so that a
// template that fails is answered by the catch-all and never as half a page.
func (c htmlCodec[T]) Encode(v T) (io.WriterTo, error) {
	var b bytes.Buffer
	if err := c.templates.ExecuteTemplate(&b, c.name, v); err != nil {
		return nil, err
	}
	return bytes.NewReader(b.Bytes()), nil
}

// upload is an uploaded corpus, before it is checked.
type upload struct {
	Name string
	Data []byte
}

// uploadDecoder reads the corpus file of a multipart form.
type uploadDecoder struct{ limit int64 }

// newUploadDecoder returns the function an operation takes for its decoder.
func newUploadDecoder(limit int64) func() openapi.Decoder[upload] {
	return func() openapi.Decoder[upload] { return uploadDecoder{limit: limit} }
}

func (uploadDecoder) MediaType() string { return "multipart/form-data" }

func (uploadDecoder) Schema() (openapi.Schema, error) {
	return openapi.Schema(`{"type":"object","required":["` + corpusField + `"],"properties":{"` + corpusField + `":{"type":"string","format":"binary","description":"The UTF-8 text to build a chain from."}}}`), nil
}

// Decode reads the parts with a MultipartReader rather than ParseMultipartForm,
// so that nothing is written to a temporary file. A file over the limit is a
// *http.MaxBytesError, which the API answers with a 413.
func (d uploadDecoder) Decode(r *http.Request) (upload, error) {
	parts, err := r.MultipartReader()
	if err != nil {
		return upload{}, fmt.Errorf("the request isn't a valid multipart form: %w", err)
	}
	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			return upload{}, errNoFile
		}
		if err != nil {
			return upload{}, fmt.Errorf("the upload couldn't be read: %w", err)
		}
		if part.FormName() != corpusField {
			continue
		}
		return d.read(part)
	}
}

func (d uploadDecoder) read(part *multipart.Part) (upload, error) {
	data, err := io.ReadAll(io.LimitReader(part, d.limit+1))
	if err != nil {
		return upload{}, fmt.Errorf("the upload couldn't be read: %w", err)
	}
	if int64(len(data)) > d.limit {
		return upload{}, &http.MaxBytesError{Limit: d.limit}
	}
	// A browser sends an empty part with no name for a file input with nothing
	// chosen.
	if part.FileName() == "" && len(data) == 0 {
		return upload{}, errNoFile
	}
	return upload{Name: part.FileName(), Data: data}, nil
}

var errNoFile = errors.New("choose a file to upload")

// sentenceForm is the form that asks for a sentence.
type sentenceForm struct {
	Token string `form:"token"`
}

// sentenceDecoder reads a sentenceForm from a URL-encoded form.
type sentenceDecoder struct{}

func newSentenceDecoder() openapi.Decoder[sentenceForm] { return sentenceDecoder{} }

func (sentenceDecoder) MediaType() string { return "application/x-www-form-urlencoded" }

func (sentenceDecoder) Schema() (openapi.Schema, error) {
	return openapi.SchemaOf[sentenceForm]("form")
}

func (sentenceDecoder) Decode(r *http.Request) (sentenceForm, error) {
	if err := r.ParseForm(); err != nil {
		return sentenceForm{}, fmt.Errorf("the form couldn't be read: %w", err)
	}
	token := r.PostForm.Get("token")
	if token == "" {
		return sentenceForm{}, errors.New("choose a starting token first")
	}
	return sentenceForm{Token: token}, nil
}
