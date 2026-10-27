package engine

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"regexp"
	"strings"
)

// MockWorld describes the market the mock provider pretends to know about.
// With it, the mock writes believable answers that list your brand and your
// competitors, so a run without API keys produces real-looking numbers. The
// answers are made up and reports label them as demo data.
type MockWorld struct {
	Brand       MockBrand
	Category    string
	Competitors []MockBrand
	// People and Products count as you in recognition answers.
	People   []string
	Products []string
	// Seed changes the answers, for example to fake a few weeks of history.
	Seed string
	// Boost raises or lowers how often engines bring up your brand.
	Boost float64
	// Affinity fixes how often an engine brings up your brand, by engine id,
	// between 0 and 1. Engines not listed get a value derived from their id.
	Affinity map[string]float64
}

// MockBrand is a product name and its website.
type MockBrand struct {
	Name   string
	Domain string
}

// otherProducts are names the mock mentions that nobody tracks, so the
// report has unknown brands to show. They are well-known placeholder names.
var otherProducts = []MockBrand{
	{Name: "Northwind", Domain: "northwind.example"},
	{Name: "Fabrikam", Domain: "fabrikam.example"},
	{Name: "Contoso", Domain: "contoso.example"},
}

var mockBlurbs = []string{
	"popular with small teams and quick to set up",
	"strong reporting and a free tier",
	"flexible, with many integrations",
	"a good pick if you want to host it yourself",
	"well documented and widely used",
	"simple pricing and a clean interface",
	"aimed at larger companies with advanced needs",
}

var mockThirdParty = []Citation{
	{URL: "https://www.reddit.com/", Title: "Reddit discussion", Domain: "reddit.com"},
	{URL: "https://www.g2.com/", Title: "G2 reviews", Domain: "g2.com"},
	{URL: "https://www.youtube.com/", Title: "YouTube comparison", Domain: "youtube.com"},
}

var recognitionQuery = regexp.MustCompile(`Query JSON: ("(?:[^"\\]|\\.)*")`)

// answer writes a deterministic demo answer for a request.
func (w *MockWorld) answer(engineID, model string, req Request) (Answer, bool) {
	if w == nil || w.Brand.Name == "" {
		return Answer{}, false
	}
	rng := w.rng(engineID, model, string(req.Mode), req.Prompt, req.Metadata["repetition"])
	// Each engine has its own taste, so the demo shows engines that differ.
	base, ok := w.Affinity[engineID]
	if !ok {
		base = 0.2 + 0.6*w.unit(engineID+model)
	}
	affinity := min(1, max(0, base+w.Boost))
	if req.Mode.IsSearchMode() {
		affinity = min(1, affinity+0.15)
	}
	if req.OutputSchema != nil {
		switch req.OutputSchema.Name {
		case "recognition_results.v1":
			return w.recognitionAnswer(req.Prompt, affinity, rng), true
		case "exact_answer.v1":
			return Answer{Text: `{"answer":null,"confidence":0}`, Parsed: json.RawMessage(`{"answer":null,"confidence":0}`)}, true
		}
		return Answer{}, false
	}
	return w.visibilityAnswer(req, affinity, rng), true
}

func (w *MockWorld) visibilityAnswer(req Request, affinity float64, rng *rand.Rand) Answer {
	prompt := strings.ToLower(req.Prompt)
	namesBrand := strings.Contains(prompt, strings.ToLower(w.Brand.Name))
	var picks []MockBrand
	if !namesBrand && rng.Float64() < affinity {
		picks = append(picks, w.Brand)
	}
	for _, c := range w.Competitors {
		if strings.Contains(prompt, strings.ToLower(c.Name)) {
			continue
		}
		if rng.Float64() < 0.45+0.5*w.unit(c.Name) {
			picks = append(picks, c)
		}
	}
	for _, o := range otherProducts {
		if rng.Float64() < 0.05+0.2*w.unit(o.Name) {
			picks = append(picks, o)
		}
	}
	if len(picks) == 0 && len(w.Competitors) > 0 {
		picks = append(picks, w.Competitors[0])
	}
	rng.Shuffle(len(picks), func(i, j int) { picks[i], picks[j] = picks[j], picks[i] })

	var b strings.Builder
	topic := strings.TrimSpace(w.Category)
	switch {
	case namesBrand:
		fmt.Fprintf(&b, "If %s is not the right fit, these are worth a look:\n\n", w.Brand.Name)
	case topic != "":
		fmt.Fprintf(&b, "Here are some good options for %s:\n\n", topic)
	default:
		b.WriteString("Here are some good options:\n\n")
	}
	for i, p := range picks {
		fmt.Fprintf(&b, "%d. **%s**: %s.\n", i+1, p.Name, mockBlurbs[rng.IntN(len(mockBlurbs))])
	}
	b.WriteString("\nThe right choice depends on your budget and how much setup you want.")
	answer := Answer{Text: b.String(), StopReason: "stop"}
	if !req.Mode.IsSearchMode() {
		return answer
	}
	query := strings.TrimSuffix(strings.TrimSpace(req.Prompt), "?")
	answer.ToolCalls = []ToolCall{{Type: "web_search", Query: query, Status: "completed"}}
	answer.ToolsAvailable = []string{"web_search"}
	answer.ToolsUsed = []string{"web_search"}
	for _, p := range picks {
		if p.Domain != "" && rng.Float64() < 0.6 {
			answer.Citations = append(answer.Citations, Citation{URL: "https://" + p.Domain + "/", Title: p.Name, Domain: p.Domain})
		}
	}
	for _, c := range mockThirdParty {
		if rng.Float64() < 0.4 {
			answer.Citations = append(answer.Citations, c)
		}
	}
	for _, c := range answer.Citations {
		answer.SourcesConsulted = append(answer.SourcesConsulted, Source{URL: c.URL, Title: c.Title, Domain: c.Domain, Kind: "web"})
	}
	return answer
}

func (w *MockWorld) recognitionAnswer(prompt string, affinity float64, rng *rand.Rand) Answer {
	query := ""
	if m := recognitionQuery.FindStringSubmatch(prompt); m != nil {
		_ = json.Unmarshal([]byte(m[1]), &query)
	}
	type row struct {
		Rank       int    `json:"rank"`
		Name       string `json:"name"`
		Type       string `json:"type"`
		Confidence int    `json:"confidence"`
		Snippet    string `json:"snippet"`
	}
	var rows []row
	if rng.Float64() < affinity {
		q := strings.ToLower(strings.TrimSpace(query))
		switch {
		case containsFold(w.People, q):
			rows = append(rows, row{1, query, "person", 55 + rng.IntN(30), fmt.Sprintf("Founder associated with %s.", w.Brand.Name)})
		case containsFold(w.Products, q):
			rows = append(rows, row{1, query, "product", 55 + rng.IntN(30), fmt.Sprintf("A product from %s.", w.Brand.Name)})
		default:
			rows = append(rows, row{1, w.Brand.Name, "product", 55 + rng.IntN(35), fmt.Sprintf("%s is a tool for %s.", w.Brand.Name, w.Category)})
		}
	}
	text, _ := json.Marshal(map[string]any{"results": rows})
	if rows == nil {
		text = []byte(`{"results":[]}`)
	}
	return Answer{Text: string(text), Parsed: json.RawMessage(text), StopReason: "stop"}
}

func containsFold(values []string, q string) bool {
	for _, v := range values {
		if strings.ToLower(strings.TrimSpace(v)) == q {
			return true
		}
	}
	return false
}

func (w *MockWorld) rng(parts ...string) *rand.Rand {
	h := fnv.New64a()
	h.Write([]byte(w.Seed))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	sum := h.Sum64()
	return rand.New(rand.NewPCG(sum, sum>>17|1))
}

// unit maps a name to a stable number between 0 and 1.
func (w *MockWorld) unit(name string) float64 {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(name)))
	return float64(h.Sum32()%1000) / 999
}
