package report

import (
	"cmp"
	"fmt"
	"html"
	"html/template"
	"io"
	"math"
	"slices"
	"strings"

	"github.com/vincentsch/llmention/internal/config"
	"github.com/vincentsch/llmention/internal/version"
	"github.com/vincentsch/llmention/internal/visibility"
)

// Series colors in a fixed order, light and dark steps. Line i of the trend
// chart always uses slot i, so an engine has the same color in each report.
var seriesLight = []string{"#2a78d6", "#eb6834", "#1baf7a", "#eda100", "#e87ba4", "#008300", "#4a3aa7", "#e34948"}
var seriesDark = []string{"#3987e5", "#d95926", "#199e70", "#c98500", "#d55181", "#008300", "#9085e9", "#e66767"}

// WriteHTML writes one HTML page that loads nothing from the network, so the
// file can be mailed or attached to a CI run as it is.
func WriteHTML(w io.Writer, r Report) error {
	return htmlPage.Execute(w, buildPage(r))
}

type page struct {
	Title, Subtitle string
	Notes           []string
	Tiles           []tile
	Bars            []bar
	Brand           string
	Matrix          *matrix
	Trend           template.HTML
	TrendTable      *matrix
	Domains         []domainRow
	Unknown         []visibility.UnknownBrand
	Steps           []string
	NamingYou       string
	Recognition     []recognitionSection
	Suites          []SuiteMetrics
	Gates           []gateRow
	Prompts         []promptBlock
	Version         string
	SeriesLight     []string
	SeriesDark      []string
}

type tile struct{ Label, Value, Detail string }

type bar struct {
	Label, Value, Tip string
	Width             float64 // percent of the track
	// Status replaces the bar when the engine and mode has no answers.
	Status string
}

type matrix struct {
	Header []string
	Rows   []matrixRow
}

type matrixRow struct {
	Label string
	Cells []matrixCell
	// Status replaces the cells when the engine and mode has no answers.
	Status string
	Span   int
}

type matrixCell struct {
	Text  string
	Width float64
	You   bool
}

type domainRow struct {
	Domain, Owner string
	Citations     int
	Width         float64
}

type gateRow struct{ Status, Selector, Message string }

type recognitionSection struct {
	Title  string
	Header []string
	Rows   [][]string
	Totals []string
	Notes  []string
}

type promptBlock struct {
	Prompt, ID string
	Answers    []answerBlock
}

type answerBlock struct {
	Label, Text, Note, Flag string
	Citations               []string
}

func buildPage(r Report) page {
	p := page{
		Title:       r.Run.Brand,
		Notes:       r.Notes,
		Version:     version.Version,
		SeriesLight: seriesLight,
		SeriesDark:  seriesDark,
		Suites:      r.Suites,
	}
	if p.Title == "" {
		p.Title = strings.Join(r.Run.Suites, ", ")
	}
	var engines []string
	for _, e := range r.Run.Engines {
		engines = append(engines, e.ID+" ("+e.Model+")")
	}
	p.Subtitle = fmt.Sprintf("Run %s · %s · %s · %s", r.Run.ID, localTime(r.Run.CreatedAt), strings.Join(engines, ", "), plural(r.Run.Counts.Answered, "answer"))
	if v := r.Visibility; v != nil && len(v.Cells) > 0 {
		p.Brand = v.Brand
		totals := v.Totals()
		you := totals[0]
		rateValue, ok := you.MentionRate()
		p.Tiles = append(p.Tiles, tile{"Mention rate", percent(rateValue, ok), fraction(you.Mentioned, you.Eligible) + " answers name " + v.Brand})
		p.Tiles = append(p.Tiles, tile{"Share of voice", ptrPercent(you.ShareOfVoice), "of all mentions of tracked brands"})
		p.Tiles = append(p.Tiles, tile{"Average rank", avgRank(you), "when listed, 1 is first"})
		for _, c := range v.Cells {
			y := c.You()
			value, ok := y.MentionRate()
			p.Bars = append(p.Bars, bar{
				Label:  c.Engine + " · " + config.ModeLabel(c.Mode),
				Value:  percent(value, ok),
				Tip:    fmt.Sprintf("%s of %s answers", fmt.Sprint(y.Mentioned), fmt.Sprint(y.Eligible)),
				Width:  value * 100,
				Status: noAnswers(c),
			})
		}
		m := &matrix{Header: []string{"Engine · mode"}}
		for i, b := range v.Cells[0].Brands {
			name := b.Name
			if i == 0 {
				name += " (you)"
			}
			m.Header = append(m.Header, name)
		}
		for _, c := range v.Cells {
			row := matrixRow{Label: c.Engine + " · " + config.ModeLabel(c.Mode), Status: noAnswers(c), Span: len(c.Brands)}
			for i, b := range c.Brands {
				value, ok := b.MentionRate()
				row.Cells = append(row.Cells, matrixCell{Text: percent(value, ok), Width: value * 100, You: i == 0})
			}
			m.Rows = append(m.Rows, row)
		}
		p.Matrix = m
		most := 1
		for _, d := range v.Domains {
			most = max(most, d.Citations)
		}
		for i, d := range v.Domains {
			if i == 12 {
				break
			}
			p.Domains = append(p.Domains, domainRow{Domain: d.Domain, Owner: owner(d.Owner), Citations: d.Citations, Width: 100 * float64(d.Citations) / float64(most)})
		}
		p.Unknown = v.Unknown
		if len(p.Unknown) > 10 {
			p.Unknown = p.Unknown[:10]
		}
		p.Steps = v.Highlights
		p.NamingYou = namingYouNote(len(v.PromptsNamingYou), v.Brand)
	}
	if r.Trend != nil && len(r.Trend.Points) > 1 {
		p.Trend = template.HTML(trendSVG(*r.Trend))
		t := &matrix{Header: []string{"Run"}}
		for _, c := range r.Trend.Cells {
			t.Header = append(t.Header, c.Engine+" · "+config.ModeLabel(c.Mode))
		}
		for _, pt := range r.Trend.Points {
			row := matrixRow{Label: runLabel(pt.CreatedAt)}
			for _, v := range pt.MentionRate {
				row.Cells = append(row.Cells, matrixCell{Text: ptrPercent(v)})
			}
			t.Rows = append(t.Rows, row)
		}
		p.TrendTable = t
	}
	for _, rec := range r.Recognition {
		sec := recognitionSection{Title: rec.Title, Header: append([]string{"Name", "Kind"}, rec.Columns...), Notes: recognitionNotes(rec)}
		for _, row := range rec.Rows {
			cells := []string{row.Name, row.Kind}
			for _, c := range row.Cells {
				cells = append(cells, c.Text())
			}
			sec.Rows = append(sec.Rows, cells)
		}
		if len(rec.Rows) > 1 {
			sec.Totals = []string{"Knew", ""}
			for _, t := range rec.Totals {
				sec.Totals = append(sec.Totals, knewText(t))
			}
		}
		p.Recognition = append(p.Recognition, sec)
	}
	for _, g := range r.Gates {
		p.Gates = append(p.Gates, gateRow{Status: g.Status, Selector: g.Selector, Message: g.Message})
	}
	byPrompt := map[string]int{}
	for _, a := range r.Answers {
		key := a.Suite + "/" + a.PromptID
		i, ok := byPrompt[key]
		if !ok {
			i = len(p.Prompts)
			byPrompt[key] = i
			p.Prompts = append(p.Prompts, promptBlock{Prompt: cmp.Or(a.Question, a.Prompt), ID: a.PromptID})
		}
		label := a.Engine + " · " + config.ModeLabel(a.Mode)
		if a.Repetition > 1 {
			label += fmt.Sprintf(" · repetition %d", a.Repetition)
		}
		block := answerBlock{Label: label, Text: strings.TrimSpace(cmp.Or(a.readable, a.Text))}
		switch a.Status {
		case "success":
		case "unfinished":
			block.Flag = "Unfinished: " + UnfinishedReason(a.StopReason) + ". Left out of the numbers."
		default:
			block.Note = "No answer: " + cmp.Or(a.Error, strings.ReplaceAll(a.Status, "_", " "))
		}
		for _, c := range a.Citations {
			block.Citations = append(block.Citations, c.URL)
		}
		p.Prompts[i].Answers = append(p.Prompts[i].Answers, block)
	}
	return p
}

// trendSVG draws one 2px line per engine and mode on a 0-100% axis, with a
// legend above it. Colors follow the cell's position and are never reused:
// cells past the last color appear only in the table below the chart.
func trendSVG(t Trend) string {
	const width, height = 760.0, 260.0
	const left, right, top, bottom = 64.0, 196.0, 16.0, 32.0
	plotW, plotH := width-left-right, height-top-bottom
	n := len(t.Points)
	x := func(i int) float64 {
		if n == 1 {
			return left + plotW/2
		}
		return left + plotW*float64(i)/float64(n-1)
	}
	y := func(v float64) float64 { return top + plotH*(1-v) }
	var b strings.Builder
	drawn := min(len(t.Cells), len(seriesLight))
	if drawn > 1 {
		b.WriteString(`<div class="legend">`)
		for ci := range drawn {
			fmt.Fprintf(&b, `<span><i class="sw s%d"></i>%s</span>`, ci, html.EscapeString(trendLabel(t.Cells[ci])))
		}
		b.WriteString(`</div>`)
	}
	if len(t.Cells) > drawn {
		fmt.Fprintf(&b, `<p class="sub">The table below also has %s.</p>`, plural(len(t.Cells)-drawn, "more engine and mode pair"))
	}
	fmt.Fprintf(&b, `<svg class="trend" viewBox="0 0 %g %g" role="img" aria-label="Mention rate over time">`, width, height)
	for _, tick := range []float64{0, 0.25, 0.5, 0.75, 1} {
		fmt.Fprintf(&b, `<line class="grid" x1="%g" x2="%g" y1="%.1f" y2="%.1f"/>`, left, left+plotW, y(tick), y(tick))
		fmt.Fprintf(&b, `<text class="tick" x="%g" y="%.1f" text-anchor="end" dominant-baseline="middle">%.0f%%</text>`, left-8, y(tick), tick*100)
	}
	// Runs on the same day need the time to tell them apart.
	axisFormat := "2 Jan"
	for i := 1; i < n; i++ {
		if t.Points[i].CreatedAt.Local().Format("2006-01-02") == t.Points[i-1].CreatedAt.Local().Format("2006-01-02") {
			axisFormat = "2 Jan 15:04"
		}
	}
	for i, pt := range t.Points {
		if i == 0 || i == n-1 || n <= 6 {
			anchor := "middle"
			if n > 1 && i == 0 {
				anchor = "start"
			} else if n > 1 && i == n-1 {
				anchor = "end"
			}
			fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%g" text-anchor="%s">%s</text>`, x(i), height-10, anchor, html.EscapeString(pt.CreatedAt.Local().Format(axisFormat)))
		}
	}
	type endLabel struct {
		slot   int
		x, y   float64
		labelY float64
		text   string
	}
	var ends []endLabel
	var hits strings.Builder
	for ci := range drawn {
		var path strings.Builder
		started := false
		lastX, lastY := 0.0, 0.0
		for i, pt := range t.Points {
			v := pt.MentionRate[ci]
			if v == nil {
				started = false
				continue
			}
			cmd := "L"
			if !started {
				cmd = "M"
				started = true
			}
			lastX, lastY = x(i), y(*v)
			fmt.Fprintf(&path, "%s%.1f %.1f ", cmd, lastX, lastY)
			// A larger transparent circle gives each point a hover target.
			fmt.Fprintf(&hits, `<circle class="hit" cx="%.1f" cy="%.1f" r="9"><title>%s, %s: %.0f%%</title></circle>`,
				lastX, lastY, html.EscapeString(trendLabel(t.Cells[ci])), runLabel(pt.CreatedAt), math.Round(*v*100))
		}
		if path.Len() == 0 {
			continue
		}
		fmt.Fprintf(&b, `<path class="line s%d" d="%s"/>`, ci, strings.TrimSpace(path.String()))
		fmt.Fprintf(&b, `<circle class="dot s%d" cx="%.1f" cy="%.1f" r="4"/>`, ci, lastX, lastY)
		ends = append(ends, endLabel{slot: ci, x: lastX, y: lastY, labelY: lastY, text: shorten(trendLabel(t.Cells[ci]), 30)})
	}
	// Move end labels apart so lines that end at the same value stay readable.
	const gap = 15.0
	slices.SortStableFunc(ends, func(a, b endLabel) int { return cmp.Compare(a.y, b.y) })
	for i := 1; i < len(ends); i++ {
		ends[i].labelY = max(ends[i].labelY, ends[i-1].labelY+gap)
	}
	if k := len(ends) - 1; k >= 0 && ends[k].labelY > top+plotH+8 {
		ends[k].labelY = top + plotH + 8
		for i := k - 1; i >= 0; i-- {
			ends[i].labelY = min(ends[i].labelY, ends[i+1].labelY-gap)
		}
	}
	for _, e := range ends {
		fmt.Fprintf(&b, `<text class="endlabel" x="%.1f" y="%.1f" dominant-baseline="middle">%s</text>`, e.x+10, e.labelY, html.EscapeString(e.text))
	}
	b.WriteString(hits.String())
	b.WriteString(`</svg>`)
	return b.String()
}

func trendLabel(c TrendCell) string {
	return c.Engine + " · " + config.ModeLabel(c.Mode)
}

// codeSpans shows the `command` parts of a note as code instead of as
// backticks. Everything else is escaped.
func codeSpans(text string) template.HTML {
	parts := strings.Split(text, "`")
	if len(parts)%2 == 0 {
		return template.HTML(html.EscapeString(text))
	}
	var b strings.Builder
	for i, part := range parts {
		if i%2 == 1 {
			b.WriteString("<code>" + html.EscapeString(part) + "</code>")
		} else {
			b.WriteString(html.EscapeString(part))
		}
	}
	return template.HTML(b.String())
}

func widthStyle(v float64) template.CSS {
	if math.IsNaN(v) || v < 0 {
		v = 0
	}
	return template.CSS(fmt.Sprintf("width:%.1f%%", min(v, 100)))
}

var htmlPage = template.Must(template.New("report").Funcs(template.FuncMap{
	"width": widthStyle,
	"series": func(colors []string) template.CSS {
		var b strings.Builder
		for i, c := range colors {
			fmt.Fprintf(&b, "--s%d:%s;", i, c)
		}
		return template.CSS(b.String())
	},
	"metric": metricText,
	"code":   codeSpans,
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} in AI answers</title>
<style>
:root{color-scheme:light dark;--page:#f9f9f7;--surface:#fcfcfb;--ink:#0b0b0b;--ink2:#52514e;--muted:#898781;--grid:#e1e0d9;--track:#f0efec;--ring:rgba(11,11,11,.10);{{series .SeriesLight}}}
@media (prefers-color-scheme: dark){:root{--page:#0d0d0d;--surface:#1a1a19;--ink:#fff;--ink2:#c3c2b7;--grid:#2c2c2a;--track:#262624;--ring:rgba(255,255,255,.10);{{series .SeriesDark}}}}
*{box-sizing:border-box}
body{margin:0;background:var(--page);color:var(--ink);font:15px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:960px;margin:0 auto;padding:32px 20px 64px}
h1{font-size:28px;margin:0 0 4px}
h2{font-size:18px;margin:36px 0 12px}
.sub{color:var(--ink2);margin:0}
.note{background:var(--surface);border:1px solid var(--ring);border-radius:8px;padding:10px 14px;margin:12px 0;color:var(--ink2)}
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px;margin-top:24px}
.tile{background:var(--surface);border:1px solid var(--ring);border-radius:10px;padding:16px}
.tile .label{color:var(--ink2);font-size:13px}
.tile .value{font-size:36px;font-weight:600;line-height:1.2}
.tile .detail{color:var(--muted);font-size:13px}
.card{background:var(--surface);border:1px solid var(--ring);border-radius:10px;padding:16px;overflow-x:auto}
.bars{display:grid;grid-template-columns:minmax(120px,max-content) 1fr 48px;gap:8px 12px;align-items:center}
.track{background:var(--track);border-radius:0 4px 4px 0;height:20px}
.fill{background:var(--s0);height:20px;border-radius:0 4px 4px 0;min-width:2px}
.fill:hover{filter:brightness(1.12)}
.status{color:var(--ink2);font-size:13px}
.foot{color:var(--ink2);margin:8px 0 0}
.num{font-variant-numeric:tabular-nums;text-align:right}
table{border-collapse:collapse;width:100%}
th,td{text-align:left;padding:6px 10px;border-bottom:1px solid var(--grid);vertical-align:middle}
th{color:var(--ink2);font-weight:500;font-size:13px}
.mini{display:flex;align-items:center;gap:8px;min-width:110px}
.mini .t{flex:1;background:var(--track);height:8px;border-radius:0 4px 4px 0}
.mini .f{height:8px;border-radius:0 4px 4px 0;background:var(--muted)}
.mini .f.you{background:var(--s0)}
.mini span{width:40px;text-align:right;font-variant-numeric:tabular-nums}
svg.trend{width:100%;height:auto}
.grid{stroke:var(--grid);stroke-width:1}
.tick{fill:var(--muted);font-size:12px}
.line{fill:none;stroke-width:2;stroke-linejoin:round;stroke-linecap:round}
.dot{stroke:var(--surface);stroke-width:2}
.endlabel{fill:var(--ink2);font-size:12px}
.hit{fill:transparent}
.hit:hover{fill:var(--ring)}
.legend{display:flex;flex-wrap:wrap;gap:6px 16px;margin:0 0 8px;color:var(--ink2);font-size:13px}
.legend span{display:inline-flex;align-items:center;gap:6px}
.sw{display:inline-block;width:12px;height:3px;border-radius:2px}
{{range $i, $c := .SeriesLight}}.line.s{{$i}}{stroke:var(--s{{$i}})}.dot.s{{$i}}{fill:var(--s{{$i}})}.sw.s{{$i}}{background:var(--s{{$i}})}
{{end}}
details{margin:6px 0}
summary{cursor:pointer}
.answer{border-left:2px solid var(--grid);padding:4px 0 4px 12px;margin:10px 0}
.answer h4{margin:0 0 4px;font-size:13px;color:var(--ink2);font-weight:500}
.answer pre{white-space:pre-wrap;font:inherit;margin:0}
.answer ul{margin:6px 0 0;padding-left:18px;color:var(--ink2);font-size:13px;word-break:break-all}
.pass{color:#006300}.fail,.error,.inconclusive{color:#d03b3b}
.flag{color:var(--ink2);font-style:italic;margin:0 0 4px}
@media (prefers-color-scheme: dark){.pass{color:#0ca30c}}
footer{margin-top:48px;color:var(--muted);font-size:13px}
@media (max-width:600px){main{padding:20px 12px 48px}th,td{padding:6px}.mini{min-width:0}.mini .t{min-width:24px}.mini span{width:auto;min-width:34px}.endlabel{display:none}.tick{font-size:24px}}
</style>
</head>
<body>
<main>
<h1>{{.Title}} in AI answers</h1>
<p class="sub">{{.Subtitle}}</p>
{{range .Notes}}<p class="note">{{code .}}</p>{{end}}
{{if .Tiles}}<div class="tiles">{{range .Tiles}}<div class="tile"><div class="label">{{.Label}}</div><div class="value">{{.Value}}</div><div class="detail">{{.Detail}}</div></div>{{end}}</div>{{end}}
{{if .Bars}}<h2>How often each assistant mentions {{.Brand}}</h2>
<div class="card"><div class="bars">{{range .Bars}}<div>{{.Label}}</div>{{if .Status}}<div class="status">{{.Status}}</div>{{else}}<div class="track" title="{{.Tip}}"><div class="fill" style="{{width .Width}}"></div></div>{{end}}<div class="num">{{.Value}}</div>{{end}}</div></div>{{end}}
{{with .Matrix}}<h2>Mention rate next to your competitors</h2>
<div class="card"><table><thead><tr>{{range .Header}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr><td>{{.Label}}</td>{{if .Status}}<td class="status" colspan="{{.Span}}">{{.Status}}</td>{{else}}{{range .Cells}}<td><div class="mini"><div class="t"><div class="f{{if .You}} you{{end}}" style="{{width .Width}}"></div></div><span>{{.Text}}</span></div></td>{{end}}{{end}}</tr>{{end}}</tbody></table></div>{{end}}
{{if .Trend}}<h2>Mention rate over time</h2>
<div class="card">{{.Trend}}
<details><summary>Table</summary><table><thead><tr>{{range .TrendTable.Header}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .TrendTable.Rows}}<tr><td>{{.Label}}</td>{{range .Cells}}<td class="num">{{.Text}}</td>{{end}}</tr>{{end}}</tbody></table></details></div>{{end}}
{{if .Domains}}<h2>Sites the answers cited</h2>
<div class="card"><table><thead><tr><th>Site</th><th>Whose</th><th>Citations</th></tr></thead><tbody>{{range .Domains}}<tr><td>{{.Domain}}</td><td>{{.Owner}}</td><td><div class="mini"><div class="t"><div class="f" style="{{width .Width}}"></div></div><span>{{.Citations}}</span></div></td></tr>{{end}}</tbody></table></div>{{end}}
{{if .Unknown}}<h2>Other names in the answers, possibly competitors</h2>
<div class="card"><table><thead><tr><th>Name</th><th>Answers</th><th>Named by</th></tr></thead><tbody>{{range .Unknown}}<tr><td>{{.Name}}</td><td class="num">{{.Answers}}</td><td>{{range $i, $e := .Engines}}{{if $i}}, {{end}}{{$e}}{{end}}</td></tr>{{end}}</tbody></table></div>{{end}}
{{if .Steps}}<h2>What stands out</h2><div class="card"><ul>{{range .Steps}}<li>{{.}}</li>{{end}}</ul></div>{{end}}
{{with .NamingYou}}<p class="foot">{{.}}</p>{{end}}
{{range .Recognition}}<h2>{{.Title}}</h2><div class="card"><table><thead><tr>{{range .Header}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}{{if .Totals}}<tr>{{range .Totals}}<th>{{.}}</th>{{end}}</tr>{{end}}</tbody></table></div>{{range .Notes}}<p class="foot">{{.}}</p>{{end}}{{end}}
{{range .Suites}}<h2>Suite {{.Suite}}</h2><div class="card"><table><thead><tr><th>Metric</th>{{range .Columns}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr><td>{{.Metric}}</td>{{$m := .Metric}}{{range .Values}}<td class="num">{{metric $m .}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
{{if .Gates}}<h2>Checks</h2><div class="card"><table><tbody>{{range .Gates}}<tr><td class="{{.Status}}">{{.Status}}</td><td><code>{{.Selector}}</code></td><td>{{.Message}}</td></tr>{{end}}</tbody></table></div>{{end}}
{{if .Prompts}}<h2>What the assistants said</h2>
{{range .Prompts}}<details><summary>{{.Prompt}}</summary>{{range .Answers}}<div class="answer"><h4>{{.Label}}</h4>{{if .Flag}}<p class="flag">{{.Flag}}</p>{{end}}{{if .Note}}<p>{{.Note}}</p>{{else}}<pre>{{.Text}}</pre>{{end}}{{if .Citations}}<ul>{{range .Citations}}<li>{{.}}</li>{{end}}</ul>{{end}}</div>{{end}}</details>
{{end}}{{end}}
<footer>Made with llmention {{.Version}}. These answers come from the providers' APIs. A signed-in user of a consumer chat app can see different answers.</footer>
</main>
</body>
</html>
`))
