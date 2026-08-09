// Package ui renders the scout/fetch lifecycle as a bubbletea program.
package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vmfunc/baleen/internal/fetch"
	"github.com/vmfunc/baleen/internal/scout"
)

// rosé pine
const (
	colText   = lipgloss.Color("#e0def4")
	colSubtle = lipgloss.Color("#908caa")
	colIris   = lipgloss.Color("#c4a7e7")
	colFoam   = lipgloss.Color("#9ccfd8")
	colGold   = lipgloss.Color("#f6c177")
	colLove   = lipgloss.Color("#eb6f92")
	colPine   = lipgloss.Color("#31748f")
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(colIris).Bold(true)
	subtleStyle = lipgloss.NewStyle().Foreground(colSubtle)
	okStyle     = lipgloss.NewStyle().Foreground(colFoam)
	skipStyle   = lipgloss.NewStyle().Foreground(colGold)
	failStyle   = lipgloss.NewStyle().Foreground(colLove)
	textStyle   = lipgloss.NewStyle().Foreground(colText)
)

const (
	maxVisibleRows = 14
	barWidth       = 24
	titleClip      = 58
)

type trackState int

const (
	statePending trackState = iota
	stateActive
	stateDone
	stateSkipped
	stateFailed
)

type row struct {
	track  scout.Track
	state  trackState
	pct    float64
	detail string
}

type phase int

const (
	phaseScout phase = iota
	phaseFetch
	phaseDone
)

// Summary is what main prints after the program exits.
type Summary struct {
	Done, Skipped, Failed, Total int
	Failures                     []string
}

type scoutedMsg struct {
	url    string
	tracks []scout.Track
	err    error
}

type eventMsg fetch.Event

type poolClosedMsg struct{}

// Model runs the whole session: scout every url, then fetch every track.
type Model struct {
	ctx     context.Context
	cancel  context.CancelFunc
	urls    []string
	opts    fetch.Options
	pending int
	rows    []row
	events  <-chan fetch.Event
	spin    spinner.Model
	bar     progress.Model
	ph      phase
	scouted []string
	errs    []string
	sum     Summary
}

// New builds a model; cancel is invoked when the user quits early.
func New(ctx context.Context, cancel context.CancelFunc, urls []string, opts fetch.Options) *Model {
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	sp.Style = lipgloss.NewStyle().Foreground(colIris)
	bar := progress.New(progress.WithGradient("#31748f", "#c4a7e7"), progress.WithWidth(barWidth))
	return &Model{
		ctx: ctx, cancel: cancel,
		urls: urls, opts: opts, pending: len(urls),
		spin: sp, bar: bar,
	}
}

// Summary reports final counts; valid after the program returns.
func (m *Model) Summary() Summary { return m.sum }

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spin.Tick}
	for _, u := range m.urls {
		cmds = append(cmds, m.scoutCmd(u))
	}
	return tea.Batch(cmds...)
}

func (m *Model) scoutCmd(url string) tea.Cmd {
	return func() tea.Msg {
		tracks, err := scout.Expand(m.ctx, url)
		return scoutedMsg{url: url, tracks: tracks, err: err}
	}
}

func (m *Model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return poolClosedMsg{}
		}
		return eventMsg(ev)
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			m.cancel()
			m.finalize()
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case scoutedMsg:
		return m.onScouted(msg)
	case eventMsg:
		m.onEvent(fetch.Event(msg))
		return m, m.waitEvent()
	case poolClosedMsg:
		m.ph = phaseDone
		m.finalize()
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) onScouted(msg scoutedMsg) (tea.Model, tea.Cmd) {
	m.pending--
	if msg.err != nil {
		m.errs = append(m.errs, msg.err.Error())
	} else {
		m.scouted = append(m.scouted, fmt.Sprintf("%s → %d tracks", msg.url, len(msg.tracks)))
		for _, t := range msg.tracks {
			m.rows = append(m.rows, row{track: t})
		}
	}
	if m.pending > 0 {
		return m, nil
	}
	if len(m.rows) == 0 {
		m.finalize()
		return m, tea.Quit
	}
	m.ph = phaseFetch
	tracks := make([]scout.Track, len(m.rows))
	for i, r := range m.rows {
		tracks[i] = r.track
	}
	m.events = fetch.Run(m.ctx, tracks, m.opts)
	return m, m.waitEvent()
}

func (m *Model) onEvent(ev fetch.Event) {
	r := &m.rows[ev.Idx]
	switch ev.Kind {
	case fetch.Started:
		r.state = stateActive
	case fetch.Progress:
		r.pct = ev.Pct
	case fetch.Done:
		r.state, r.pct = stateDone, 100
	case fetch.Skipped:
		r.state = stateSkipped
	case fetch.Failed:
		r.state, r.detail = stateFailed, ev.Detail
	}
}

func (m *Model) finalize() {
	m.sum = Summary{Total: len(m.rows)}
	for _, r := range m.rows {
		switch r.state {
		case stateDone:
			m.sum.Done++
		case stateSkipped:
			m.sum.Skipped++
		case stateFailed:
			m.sum.Failed++
			m.sum.Failures = append(m.sum.Failures, name(r.track)+": "+r.detail)
		}
	}
}

func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("baleen") + subtleStyle.Render("  sifting the stream") + "\n\n")
	if m.ph == phaseScout {
		b.WriteString(m.spin.View() + textStyle.Render(" scouting links...") + "\n")
		for _, s := range m.scouted {
			b.WriteString(subtleStyle.Render("  "+s) + "\n")
		}
	} else {
		m.viewFetch(&b)
	}
	for _, e := range m.errs {
		b.WriteString(failStyle.Render("  ! "+e) + "\n")
	}
	b.WriteString("\n" + subtleStyle.Render("q to quit") + "\n")
	return b.String()
}

func (m *Model) viewFetch(b *strings.Builder) {
	done, skipped, failed, active := 0, 0, 0, 0
	for _, r := range m.rows {
		switch r.state {
		case stateDone:
			done++
		case stateSkipped:
			skipped++
		case stateFailed:
			failed++
		case stateActive:
			active++
		}
	}
	finished := done + skipped + failed
	overall := 0.0
	if len(m.rows) > 0 {
		overall = float64(finished) / float64(len(m.rows))
	}
	fmt.Fprintf(b, "%s %s\n\n", m.bar.ViewAs(overall),
		textStyle.Render(fmt.Sprintf("%d/%d", finished, len(m.rows))))
	shown := 0
	for _, pass := range []trackState{stateActive, stateFailed, stateDone, stateSkipped} {
		for i := len(m.rows) - 1; i >= 0 && shown < maxVisibleRows; i-- {
			if m.rows[i].state != pass {
				continue
			}
			if line, ok := renderRow(m.rows[i], m.spin.View(), m.bar); ok {
				b.WriteString(line + "\n")
				shown++
			}
		}
	}
	fmt.Fprintf(b, "\n%s\n", subtleStyle.Render(fmt.Sprintf(
		"✓ %d done   ↷ %d already had   ✗ %d failed   %d running", done, skipped, failed, active)))
}

func renderRow(r row, spin string, bar progress.Model) (string, bool) {
	switch r.state {
	case stateActive:
		return fmt.Sprintf("  %s %s %s", spin, bar.ViewAs(r.pct/100),
			textStyle.Render(clip(name(r.track)))), true
	case stateFailed:
		return failStyle.Render("  ✗ " + clip(name(r.track)+" — "+r.detail)), true
	case stateSkipped:
		return skipStyle.Render("  ↷ " + clip(name(r.track))), true
	case stateDone:
		return okStyle.Render("  ✓ " + clip(name(r.track))), true
	default:
		return "", false
	}
}

func name(t scout.Track) string {
	if t.Collection != "" {
		return fmt.Sprintf("%s · %02d %s", t.Collection, t.Index, t.Title)
	}
	if t.Uploader != "" {
		return t.Uploader + " - " + t.Title
	}
	return t.Title
}

func clip(s string) string {
	if len(s) <= titleClip {
		return s
	}
	return s[:titleClip-1] + "…"
}
