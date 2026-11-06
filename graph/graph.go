// Package graph lays out part of a Markov chain as a picture: which tokens and
// transitions to draw, and where. It knows nothing of SVG beyond the paths of
// its edges, and nothing of HTTP.
//
// A chain of natural text is too dense to draw whole, so a Graph shows the most
// frequent tokens and each token's most likely transitions among them.
package graph

import (
	"fmt"
	"math"
	"slices"

	"github.com/Zaba505/markov-to-llm/markov"
)

const (
	// Width is the width of every Graph's coordinate space.
	Width = 960.0

	// MaxEdgesPerNode is how many outgoing edges a node keeps at most.
	MaxEdgesPerNode = 5

	minHeight = 560.0
	maxHeight = 960.0

	minRadius = 12.0
	maxRadius = 32.0

	maxLabelRunes = 16
	labelRuneSize = 8.0

	// margin keeps node centers far enough from the edge of the picture for
	// their labels and self-loops.
	margin = 52.0

	arrowGap = 4.0
	loopSize = 34.0

	layoutIterations  = 300
	collisionPasses   = 80
	collisionPadding  = 6.0
	gravity           = 0.015
	spacingMultiplier = 1.2
)

// Node is a token in the picture.
type Node struct {
	Token string

	// Label is Token, shortened when it is too long to draw.
	Label string

	// Count is how many times Token appears in the corpus.
	Count int

	X, Y, R float64

	// Terminal reports that Token ends a sentence, so a walk can't continue
	// from it, or start from it.
	Terminal bool

	// Selected reports that Token is the selected token.
	Selected bool

	// Highlighted reports that Token is the selected token or can follow it.
	Highlighted bool
}

// Edge is a transition in the picture.
type Edge struct {
	From, To string

	// Probability is the chance of To following From in the whole chain, not
	// only among the edges in the picture.
	Probability float64

	// Path is the edge's SVG path data.
	Path string

	Width, Opacity float64

	// LabelX and LabelY are where the edge's probability goes.
	LabelX, LabelY float64

	// Highlighted reports that the edge leaves the selected token.
	Highlighted bool
}

// Graph is a picture of part of a chain. Edges are in drawing order, with the
// highlighted ones last, so that they are on top.
type Graph struct {
	Width, Height float64
	Nodes         []Node
	Edges         []Edge

	// Selected is the selected token, or "" when there is none in the picture.
	Selected string
}

// Percent formats a probability for people.
func Percent(p float64) string {
	if p > 0 && p < 0.01 {
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", p*100)
}

// Build lays out the limit most frequent tokens of c. When selected is a token
// of c, it and its most likely next tokens are in the picture too, whatever
// their frequency. The same arguments always give the same picture, and
// selected moves nothing unless it adds nodes.
func Build(c *markov.Chain, limit int, selected string) Graph {
	tokens := slices.Clone(c.Top(limit))
	if c.Count(selected) == 0 {
		selected = ""
	}
	if selected != "" {
		if !slices.Contains(tokens, selected) {
			tokens = append(tokens, selected)
		}
		for _, next := range c.Next(selected)[:min(MaxEdgesPerNode, len(c.Next(selected)))] {
			if next.Token != markov.EOS && !slices.Contains(tokens, next.Token) {
				tokens = append(tokens, next.Token)
			}
		}
	}

	index := make(map[string]int, len(tokens))
	for i, token := range tokens {
		index[token] = i
	}

	g := Graph{Width: Width, Height: heightFor(len(tokens)), Selected: selected}
	g.Nodes = newNodes(c, tokens, selected)

	links := newLinks(c, tokens, index)
	layOut(g.Nodes, links, g.Width, g.Height)

	for _, l := range links {
		g.Edges = append(g.Edges, newEdge(g.Nodes, l, g.Width, g.Height))
	}
	if selected != "" {
		highlight(&g, index)
	}
	return g
}

func heightFor(nodes int) float64 {
	return min(maxHeight, max(minHeight, 440+float64(nodes)*5))
}

func newNodes(c *markov.Chain, tokens []string, selected string) []Node {
	highest := 1
	for _, token := range tokens {
		highest = max(highest, c.Count(token))
	}
	nodes := make([]Node, len(tokens))
	for i, token := range tokens {
		nodes[i] = Node{
			Token:    token,
			Label:    shorten(token),
			Count:    c.Count(token),
			R:        minRadius + (maxRadius-minRadius)*math.Sqrt(float64(c.Count(token))/float64(highest)),
			Terminal: markov.IsTerminator(token),
			Selected: token == selected,
		}
	}
	return nodes
}

func shorten(token string) string {
	runes := []rune(token)
	if len(runes) <= maxLabelRunes {
		return token
	}
	return string(runes[:maxLabelRunes-1]) + "…"
}

// link is an edge by its nodes' indexes, before it has a shape.
type link struct {
	from, to    int
	probability float64
}

func newLinks(c *markov.Chain, tokens []string, index map[string]int) []link {
	var links []link
	for from, token := range tokens {
		kept := 0
		for _, next := range c.Next(token) {
			if kept == MaxEdgesPerNode {
				break
			}
			to, shown := index[next.Token]
			if !shown {
				continue
			}
			links = append(links, link{from: from, to: to, probability: next.Probability})
			kept++
		}
	}
	return links
}

// layOut positions nodes with Fruchterman-Reingold force-directed layout: nodes
// repel each other, and edges pull their ends together. It starts from a
// spiral with the most frequent tokens in the middle, and uses no randomness,
// so a layout depends only on its input.
func layOut(nodes []Node, links []link, width, height float64) {
	n := len(nodes)
	if n == 0 {
		return
	}
	k := spacingMultiplier * math.Sqrt(width*height/float64(n))
	x, y := spiral(n, width, height)
	dx, dy := make([]float64, n), make([]float64, n)

	temperature := width / 8
	cooling := temperature / layoutIterations
	for range layoutIterations {
		clear(dx)
		clear(dy)
		for i := range n {
			for j := i + 1; j < n; j++ {
				ux, uy, d := direction(x[j]-x[i], y[j]-y[i], i, j)
				force := k * k / d
				dx[i] -= ux * force
				dy[i] -= uy * force
				dx[j] += ux * force
				dy[j] += uy * force
			}
		}
		for _, l := range links {
			if l.from == l.to {
				continue
			}
			ux, uy, d := direction(x[l.to]-x[l.from], y[l.to]-y[l.from], l.from, l.to)
			force := d * d / k * (0.3 + l.probability)
			dx[l.from] += ux * force
			dy[l.from] += uy * force
			dx[l.to] -= ux * force
			dy[l.to] -= uy * force
		}
		for i := range n {
			dx[i] += (width/2 - x[i]) * gravity * k / 10
			dy[i] += (height/2 - y[i]) * gravity * k / 10
			d := math.Max(math.Hypot(dx[i], dy[i]), 1e-9)
			step := math.Min(d, temperature)
			x[i] += dx[i] / d * step
			y[i] += dy[i] / d * step
		}
		temperature = math.Max(temperature-cooling, 0.5)
	}

	fit(x, y, width, height)
	separate(nodes, x, y, width, height)
	for i := range nodes {
		nodes[i].X, nodes[i].Y = x[i], y[i]
	}
}

// spiral returns n points on a golden-angle spiral, centered in the picture.
func spiral(n int, width, height float64) (x, y []float64) {
	x, y = make([]float64, n), make([]float64, n)
	golden := math.Pi * (3 - math.Sqrt(5))
	for i := range n {
		r := math.Sqrt(float64(i)+0.5) / math.Sqrt(float64(n)) * math.Min(width, height) / 2
		angle := float64(i) * golden
		x[i] = width/2 + r*math.Cos(angle)
		y[i] = height/2 + r*math.Sin(angle)
	}
	return x, y
}

// direction returns the unit vector and length of (dx, dy). Coincident points
// get a direction from their indexes, so that they still move apart.
func direction(dx, dy float64, i, j int) (ux, uy, d float64) {
	d = math.Hypot(dx, dy)
	if d < 1e-6 {
		angle := float64(i*31+j*17) * 0.7
		return math.Cos(angle), math.Sin(angle), 1e-3
	}
	return dx / d, dy / d, d
}

// fit scales the points to fill the picture, inside its margin.
func fit(x, y []float64, width, height float64) {
	rescale(x, margin, width-margin)
	rescale(y, margin, height-margin)
}

func rescale(values []float64, low, high float64) {
	lowest, highest := slices.Min(values), slices.Max(values)
	if highest-lowest < 1e-9 {
		for i := range values {
			values[i] = (low + high) / 2
		}
		return
	}
	for i, v := range values {
		values[i] = low + (v-lowest)/(highest-lowest)*(high-low)
	}
}

// separate pushes apart nodes whose circles or labels would overlap, as far as
// the picture has room to.
func separate(nodes []Node, x, y []float64, width, height float64) {
	reach := make([]float64, len(nodes))
	for i, node := range nodes {
		label := float64(len([]rune(node.Label)))*labelRuneSize/2 + 2
		reach[i] = math.Max(node.R, label)
	}
	for range collisionPasses {
		moved := false
		for i := range nodes {
			for j := i + 1; j < len(nodes); j++ {
				ux, uy, d := direction(x[j]-x[i], y[j]-y[i], i, j)
				overlap := reach[i] + reach[j] + collisionPadding - d
				if overlap <= 0 {
					continue
				}
				moved = true
				x[i] -= ux * overlap / 2
				y[i] -= uy * overlap / 2
				x[j] += ux * overlap / 2
				y[j] += uy * overlap / 2
			}
		}
		for i := range nodes {
			x[i] = math.Min(math.Max(x[i], margin), width-margin)
			y[i] = math.Min(math.Max(y[i], margin), height-margin)
		}
		if !moved {
			return
		}
	}
}

func newEdge(nodes []Node, l link, width, height float64) Edge {
	from, to := nodes[l.from], nodes[l.to]
	e := Edge{
		From:        from.Token,
		To:          to.Token,
		Probability: l.probability,
		Width:       1 + 4.5*l.probability,
		Opacity:     0.25 + 0.6*l.probability,
	}
	if l.from == l.to {
		e.Path, e.LabelX, e.LabelY = loopPath(from, width, height)
	} else {
		e.Path, e.LabelX, e.LabelY = curvePath(from, to)
	}
	return e
}

// curvePath bends an edge to its right, so that A to B and B to A don't lie on
// top of each other, and ends it at the edge of its nodes' circles.
func curvePath(from, to Node) (path string, labelX, labelY float64) {
	dx, dy := to.X-from.X, to.Y-from.Y
	d := math.Max(math.Hypot(dx, dy), 1e-6)
	bend := math.Min(d*0.18, 56)
	cx := (from.X+to.X)/2 + dy/d*bend
	cy := (from.Y+to.Y)/2 - dx/d*bend

	sx, sy := towards(from.X, from.Y, cx, cy, from.R)
	ex, ey := towards(to.X, to.Y, cx, cy, to.R+arrowGap)
	path = fmt.Sprintf("M%.1f %.1fQ%.1f %.1f %.1f %.1f", sx, sy, cx, cy, ex, ey)
	return path, 0.25*sx + 0.5*cx + 0.25*ex, 0.25*sy + 0.5*cy + 0.25*ey
}

// loopPath draws a token following itself as a loop on the node's outer side,
// away from the middle of the picture, where most of the edges are.
func loopPath(node Node, width, height float64) (path string, labelX, labelY float64) {
	angle := math.Atan2(node.Y-height/2, node.X-width/2)
	const spread = 0.6
	point := func(a, r float64) (float64, float64) {
		return node.X + r*math.Cos(a), node.Y + r*math.Sin(a)
	}
	sx, sy := point(angle-spread, node.R)
	c1x, c1y := point(angle-spread, node.R+loopSize)
	c2x, c2y := point(angle+spread, node.R+loopSize)
	ex, ey := point(angle+spread, node.R+arrowGap)
	labelX, labelY = point(angle, node.R+loopSize*0.9)
	return fmt.Sprintf("M%.1f %.1fC%.1f %.1f %.1f %.1f %.1f %.1f", sx, sy, c1x, c1y, c2x, c2y, ex, ey), labelX, labelY
}

// towards returns the point at distance r from (x, y), in the direction of
// (tx, ty).
func towards(x, y, tx, ty, r float64) (float64, float64) {
	d := math.Max(math.Hypot(tx-x, ty-y), 1e-6)
	return x + (tx-x)/d*r, y + (ty-y)/d*r
}

// highlight marks the selected token and what follows it, and moves the
// selected token's edges to the end, so that they are drawn on top.
func highlight(g *Graph, index map[string]int) {
	g.Nodes[index[g.Selected]].Highlighted = true
	for i := range g.Edges {
		if g.Edges[i].From == g.Selected {
			g.Edges[i].Highlighted = true
			g.Nodes[index[g.Edges[i].To]].Highlighted = true
		}
	}
	slices.SortStableFunc(g.Edges, func(a, b Edge) int {
		switch {
		case a.Highlighted == b.Highlighted:
			return 0
		case b.Highlighted:
			return -1
		default:
			return 1
		}
	})
}
