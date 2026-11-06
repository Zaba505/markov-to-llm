// Command markov-to-llm serves a page for building and exploring a Markov chain
// from a text corpus, as an intuitive first step towards how LLMs predict
// tokens.
package main

import (
	"github.com/Zaba505/markov-to-llm/app"
	"github.com/z5labs/humus/httpserver"
)

func main() {
	httpserver.Main(app.Init, app.Options()...)
}
