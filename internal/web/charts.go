package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"bidos/internal/ai"
	"bidos/internal/app"
	"bidos/internal/pipeline"
	"bidos/internal/retrieval"
	"bidos/internal/store"
)

// Inline SVG charts rendered server-side. Colours come from CSS custom properties
// (--c1..--c6, --accent) so the three themes restyle them without re-rendering.

func esc(s string) string { return template.HTMLEscapeString(s) }

// BarChart draws horizontal bars with direct labels.
func BarChart(items []app.NamedCount, width int) template.HTML {
	if len(items) == 0 {
		return `<p class="empty-inline">No data yet.</p>`
	}
	max := 1
	for _, it := range items {
		if it.Value > max {
			max = it.Value
		}
	}
	rowH, labelW := 26, 150
	h := len(items) * rowH
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart bars" viewBox="0 0 %d %d" width="100%%" role="img" aria-label="Bar chart">`, width, h)
	for i, it := range items {
		y := i * rowH
		w := float64(width-labelW-48) * float64(it.Value) / float64(max)
		fmt.Fprintf(&b, `<text x="0" y="%d" class="lbl">%s</text>`, y+17, esc(truncateStr(it.Name, 22)))
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%.1f" height="16" rx="6" style="fill:var(--c%d)"/>`, labelW, y+4, math.Max(w, 2), i%6+1)
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="val">%d</text>`, float64(labelW)+w+6, y+17, it.Value)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// Donut draws a ring with a centred total and a legend.
func Donut(items []app.NamedCount, total int, centre string) template.HTML {
	sum := 0
	for _, it := range items {
		sum += it.Value
	}
	if sum == 0 {
		return `<p class="empty-inline">Nothing to show yet.</p>`
	}
	r, cx, cy, stroke := 44.0, 60.0, 60.0, 14.0
	circ := 2 * math.Pi * r
	var b strings.Builder
	b.WriteString(`<div class="donut-wrap"><svg class="chart donut" viewBox="0 0 120 120" width="140" role="img" aria-label="Donut chart">`)
	offset := 0.0
	for i, it := range items {
		if it.Value == 0 {
			continue
		}
		frac := float64(it.Value) / float64(sum)
		fmt.Fprintf(&b, `<circle cx="%.0f" cy="%.0f" r="%.0f" fill="none" stroke-width="%.0f" style="stroke:var(--c%d)" stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f" transform="rotate(-90 60 60)"/>`, cx, cy, r, stroke, i%6+1, frac*circ, circ, -offset*circ)
		offset += frac
	}
	if centre == "" {
		centre = fmt.Sprint(total)
	}
	fmt.Fprintf(&b, `<text x="60" y="58" text-anchor="middle" class="big">%s</text><text x="60" y="74" text-anchor="middle" class="lbl">total</text></svg><ul class="legend">`, esc(centre))
	for i, it := range items {
		fmt.Fprintf(&b, `<li><i style="background:var(--c%d)"></i>%s <b>%d</b></li>`, i%6+1, esc(it.Name), it.Value)
	}
	b.WriteString(`</ul></div>`)
	return template.HTML(b.String())
}

// ColumnChart draws vertical rounded columns over a soft track, one label per column.
// Bars alternate a full-strength accent for the peak so the series reads at a glance.
func ColumnChart(items []app.NamedCount, width, height int) template.HTML {
	if len(items) == 0 {
		return `<p class="empty-inline">No data yet.</p>`
	}
	max, peak := 1, 0
	for i, it := range items {
		if it.Value > max {
			max, peak = it.Value, i
		}
	}
	padB, padT := 20, 10
	h := float64(height - padB - padT)
	slot := float64(width) / float64(len(items))
	bw := math.Min(slot*0.52, 26)
	step := len(items)/12 + 1
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart cols" viewBox="0 0 %d %d" width="100%%" role="img" aria-label="Column chart">`, width, height)
	for i, it := range items {
		x := slot*float64(i) + (slot-bw)/2
		bh := math.Max(h*float64(it.Value)/float64(max), 3)
		fmt.Fprintf(&b, `<rect class="track" x="%.1f" y="%d" width="%.1f" height="%.1f" rx="%.1f"/>`, x, padT, bw, h, bw/2)
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="%.1f" style="fill:var(--c%d)"><title>%s: %d</title></rect>`,
			x, float64(padT)+h-bh, bw, bh, bw/2, map[bool]int{true: 1, false: 2}[i == peak], esc(it.Name), it.Value)
		if i%step == 0 || i == len(items)-1 {
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" text-anchor="middle" class="lbl">%s</text>`, x+bw/2, height-5, esc(shortDay(it.Name)))
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// AreaChart draws a line + soft area for a series (e.g. activity by day).
func AreaChart(items []app.NamedCount, width, height int) template.HTML {
	if len(items) < 2 {
		return `<p class="empty-inline">Not enough history yet.</p>`
	}
	max := 1
	for _, it := range items {
		if it.Value > max {
			max = it.Value
		}
	}
	padL, padB, padT := 8, 22, 8
	w := float64(width - padL*2)
	h := float64(height - padB - padT)
	var pts []string
	for i, it := range items {
		x := float64(padL) + w*float64(i)/float64(len(items)-1)
		y := float64(padT) + h - h*float64(it.Value)/float64(max)
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart area" viewBox="0 0 %d %d" width="100%%" role="img" aria-label="Trend">`, width, height)
	fmt.Fprintf(&b, `<polygon points="%d,%d %s %d,%d" style="fill:var(--accent);opacity:.18"/>`, padL, height-padB, strings.Join(pts, " "), width-padL, height-padB)
	fmt.Fprintf(&b, `<polyline points="%s" fill="none" style="stroke:var(--accent)" stroke-width="2.5" stroke-linejoin="round"/>`, strings.Join(pts, " "))
	step := len(items) / 6
	if step < 1 {
		step = 1
	}
	for i, it := range items {
		if i%step == 0 || i == len(items)-1 {
			x := float64(padL) + w*float64(i)/float64(len(items)-1)
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" text-anchor="middle" class="lbl">%s</text>`, x, height-6, esc(shortDay(it.Name)))
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// Radar draws the qualification scorecard (0..1 per criterion).
func Radar(labels []string, values []float64) template.HTML {
	n := len(labels)
	if n < 3 {
		return `<p class="empty-inline">Qualification needs at least three criteria.</p>`
	}
	cx, cy, r := 130.0, 120.0, 82.0
	pt := func(i int, f float64) (float64, float64) {
		a := -math.Pi/2 + 2*math.Pi*float64(i)/float64(n)
		return cx + r*f*math.Cos(a), cy + r*f*math.Sin(a)
	}
	var b strings.Builder
	b.WriteString(`<svg class="chart radar" viewBox="0 0 260 240" width="100%" role="img" aria-label="Qualification radar">`)
	for _, ring := range []float64{0.25, 0.5, 0.75, 1} {
		var pts []string
		for i := 0; i < n; i++ {
			x, y := pt(i, ring)
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
		}
		fmt.Fprintf(&b, `<polygon points="%s" class="grid"/>`, strings.Join(pts, " "))
	}
	var pts []string
	for i := 0; i < n; i++ {
		x, y := pt(i, 1)
		fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.1f" y2="%.1f" class="grid"/>`, cx, cy, x, y)
		v := values[i]
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		vx, vy := pt(i, v)
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", vx, vy))
		lx, ly := pt(i, 1.22)
		anchor := "middle"
		if lx < cx-10 {
			anchor = "end"
		} else if lx > cx+10 {
			anchor = "start"
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="%s" class="lbl">%s</text>`, lx, ly+4, anchor, esc(truncateStr(labels[i], 16)))
	}
	fmt.Fprintf(&b, `<polygon points="%s" style="fill:var(--accent);opacity:.28;stroke:var(--accent)" stroke-width="2"/>`, strings.Join(pts, " "))
	for _, p := range pts {
		xy := strings.Split(p, ",")
		fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="3.5" style="fill:var(--accent)"/>`, xy[0], xy[1])
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// Progress renders an accessible progress bar.
func Progress(pct int, tone string) template.HTML {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if tone == "" {
		tone = "accent"
	}
	return template.HTML(fmt.Sprintf(`<div class="progress" role="progressbar" aria-valuenow="%d" aria-valuemin="0" aria-valuemax="100"><span style="width:%d%%;background:var(--%s)"></span></div>`, pct, pct, tone))
}

// Sparkline renders a tiny inline trend.
func Sparkline(items []app.NamedCount) template.HTML {
	if len(items) < 2 {
		return ""
	}
	max := 1
	for _, it := range items {
		if it.Value > max {
			max = it.Value
		}
	}
	var pts []string
	for i, it := range items {
		x := 100 * float64(i) / float64(len(items)-1)
		y := 28 - 26*float64(it.Value)/float64(max)
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	return template.HTML(fmt.Sprintf(`<svg class="spark" viewBox="0 0 100 30" width="100" height="30" aria-hidden="true"><polyline points="%s" fill="none" style="stroke:var(--accent)" stroke-width="2"/></svg>`, strings.Join(pts, " ")))
}

// MarkedDraft renders draft text with [c#] markers as citation chips and unsupported
// sentences highlighted (from the stored verification).
func MarkedDraft(text string, v ai.Verification) template.HTML {
	unsupported := map[string]bool{}
	for _, s := range v.Sentences {
		if s.Status == "unsupported" {
			unsupported[strings.TrimSpace(s.Text)] = true
		}
	}
	var b strings.Builder
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		for _, sent := range splitKeep(para) {
			key := strings.TrimSpace(sent)
			cls := ""
			if unsupported[key] || unsupported[stripMarkers(key)] {
				cls = ` class="unsupported" title="Unsupported: no cited evidence backs this sentence"`
			}
			fmt.Fprintf(&b, `<span%s>%s</span> `, cls, chipMarkers(esc(sent)))
		}
		b.WriteString("</p>")
	}
	return template.HTML(b.String())
}

func chipMarkers(s string) string {
	out := s
	for i := 1; i <= 12; i++ {
		m := fmt.Sprintf("[c%d]", i)
		out = strings.ReplaceAll(out, m, fmt.Sprintf(`<a class="cite" href="#cite-%d" title="Citation %d">%d</a>`, i, i, i))
	}
	return out
}

func stripMarkers(s string) string {
	for i := 1; i <= 12; i++ {
		s = strings.ReplaceAll(s, fmt.Sprintf(" [c%d]", i), "")
		s = strings.ReplaceAll(s, fmt.Sprintf("[c%d]", i), "")
	}
	return strings.TrimSpace(s)
}

// splitKeep splits a paragraph into sentences using the verifier's splitter so the
// unsupported lookup matches exactly.
func splitKeep(p string) []string {
	return retrieval.SplitSentences(p)
}

// ---- template functions -------------------------------------------------------------

func funcMap() template.FuncMap {
	return template.FuncMap{
		"bar":      BarChart,
		"column":   ColumnChart,
		"donut":    Donut,
		"area":     AreaChart,
		"radar":    Radar,
		"progress": Progress,
		"spark":    Sparkline,
		"marked":   MarkedDraft,
		"pct": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a * 100 / b
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(n int) []int {
			out := make([]int, n)
			for i := range out {
				out[i] = i + 1
			}
			return out
		},
		"date":      shortDate,
		"datetime":  shortDateTime,
		"ago":       ago,
		"money":     money,
		"num":       func(f float64) string { return fmt.Sprintf("%.1f", f) },
		"int":       func(f float64) int { return int(math.Round(f)) },
		"lower":     strings.ToLower,
		"upper":     strings.ToUpper,
		"title":     titleCase,
		"status":    statusLabel,
		"icon":      statusIcon,
		"tone":      statusTone,
		"stage":     pipeline.HumanStage,
		"trunc":     truncateStr,
		"join":      strings.Join,
		"contains":  strings.Contains,
		"hasPrefix": strings.HasPrefix,
		"replace":   strings.ReplaceAll,
		"nl2br": func(s string) template.HTML {
			return template.HTML(strings.ReplaceAll(esc(s), "\n", "<br>"))
		},
		"paras": func(s string) template.HTML {
			var b strings.Builder
			for _, p := range strings.Split(strings.TrimSpace(s), "\n") {
				if strings.TrimSpace(p) != "" {
					b.WriteString("<p>" + chipMarkers(esc(p)) + "</p>")
				}
			}
			return template.HTML(b.String())
		},
		"json": func(v any) string { b, _ := json.Marshal(v); return string(b) },
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"default": func(def string, v string) string {
			if v == "" {
				return def
			}
			return v
		},
		"safe":       func(s string) template.HTML { return template.HTML(s) },
		"label":      labelTone,
		"initials":   initials,
		"stageViews": pipeline.StageViews,
		"runPct":     pipeline.Progress,
		"isGate":     pipeline.IsGate,
		"stepCount":  func(m map[string]int, k string) int { return m[k] },
		"role":       roleLabel,
		"greeting":   greeting,
		"firstName":  firstName,
		"basename": func(s string) string {
			if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
				return s[i+1:]
			}
			return s
		},
		"selected": func(a, b string) template.HTMLAttr {
			if a == b {
				return "selected"
			}
			return ""
		},
		"checked": func(b bool) template.HTMLAttr {
			if b {
				return "checked"
			}
			return ""
		},
		"days": app.DaysUntil,
		"mulf": func(a, b float64) float64 { return a * b },
		"list": func(v ...string) []string { return v },
	}
}

func shortDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

func shortDateTime(ts string) string {
	if t, ok := store.ParseTime(ts); ok {
		return t.Format("2 Jan 2006 15:04")
	}
	return ts
}

func shortDay(ts string) string {
	if t, ok := store.ParseTime(ts); ok {
		return t.Format("2 Jan")
	}
	if len(ts) >= 10 {
		if t, err := time.Parse("2006-01-02", ts[:10]); err == nil {
			return t.Format("2 Jan")
		}
	}
	return ts
}

func ago(ts string) string {
	t, ok := store.ParseTime(ts)
	if !ok {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func money(f float64) string {
	switch {
	case f >= 1e6:
		return fmt.Sprintf("$%.1fM", f/1e6)
	case f >= 1e3:
		return fmt.Sprintf("$%.0fk", f/1e3)
	default:
		return fmt.Sprintf("$%.0f", f)
	}
}

func titleCase(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-1]) + "…"
}

func statusLabel(s string) string {
	switch s {
	case store.StatusNotStarted:
		return "Not started"
	case store.StatusDrafted:
		return "Draft"
	case store.StatusNeedsSME:
		return "Needs SME"
	case store.StatusInReview:
		return "In review"
	case store.StatusApproved:
		return "Approved"
	case store.StatusNeedsEvidence:
		return "Needs evidence"
	case store.StatusRejected:
		return "Rejected"
	case store.StatusNeedsReReview:
		return "Needs re-review"
	case store.StatusRemoved:
		return "Removed"
	case store.JobWaiting:
		return "Waiting for human"
	}
	return titleCase(s)
}

func statusIcon(s string) string {
	switch s {
	case store.StatusApproved, store.JobSucceeded:
		return "✓"
	case store.StatusNeedsEvidence, store.StatusNeedsSME:
		return "!"
	case store.StatusInReview, store.StatusNeedsReReview, store.JobWaiting:
		return "↻"
	case store.StatusRejected, store.JobFailed:
		return "×"
	case store.JobRunning:
		return "…"
	}
	return "●"
}

func statusTone(s string) string {
	switch s {
	case store.StatusApproved, store.JobSucceeded, "passed", "resolved", "Connected":
		return "ok"
	case store.StatusNeedsEvidence, store.StatusNeedsSME, store.StatusRejected, store.JobFailed, "blocker", "Error":
		return "danger"
	case store.StatusInReview, store.StatusNeedsReReview, store.JobWaiting, "warning", "Rate-limited", "dismissed":
		return "warn"
	case store.StatusDrafted, store.JobRunning, "info", "Available":
		return "info"
	}
	return "muted"
}

func labelTone(label string) string {
	switch label {
	case store.LabelStrong:
		return "ok"
	case store.LabelModerate:
		return "info"
	case store.LabelWeak:
		return "warn"
	case store.LabelInsufficient:
		return "danger"
	}
	return "muted"
}

func roleLabel(r string) string { return titleCase(r) }

func initials(name string) string {
	parts := strings.Fields(name)
	out := ""
	for i, p := range parts {
		if i > 1 {
			break
		}
		out += strings.ToUpper(p[:1])
	}
	return out
}

// greeting is the time-of-day salutation in the header.
func greeting() string {
	switch h := time.Now().Hour(); {
	case h < 12:
		return "Good morning"
	case h < 18:
		return "Good afternoon"
	default:
		return "Good evening"
	}
}

// firstName keeps the header greeting short.
func firstName(name string) string {
	if parts := strings.Fields(name); len(parts) > 0 {
		return parts[0]
	}
	return "there"
}
