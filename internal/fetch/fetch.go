// Package fetch drives yt-dlp downloads with a bounded worker pool and turns
// its line output into progress events.
package fetch

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vmfunc/baleen/internal/scout"
)

// Kind tags an Event.
type Kind int

const (
	Started Kind = iota
	Progress
	Done
	Skipped
	Failed
	Backoff
)

// Event reports one state change for the track at Idx.
type Event struct {
	Idx    int
	Kind   Kind
	Pct    float64
	Detail string
}

// Options configures a pool run.
type Options struct {
	Dest    string        // library root, e.g. ~/Music
	Archive string        // yt-dlp download archive path
	Jobs    int
	Pace    time.Duration // min interval between yt-dlp launches, shared by all workers
	Retries int           // extra attempts per track after a rate-limit 403
}

// backoffBase is the first 403 cool-down; each further attempt doubles it.
// soundcloud's ip block decays on the order of minutes, so short retries
// inside one track are pointless and long ones stall the whole pool.
const backoffBase = 45 * time.Second

var progressRe = regexp.MustCompile(`\[download\]\s+([0-9.]+)%`)

// audioFormat prefers the direct mp3 stream and falls back to whatever is
// best; transcoding is never worth the quality loss on already-lossy sources.
const audioFormat = "bestaudio[ext=mp3]/bestaudio"

// Run downloads every track and streams events until all workers finish, then
// closes the channel. Cancel ctx to stop early.
func Run(ctx context.Context, tracks []scout.Track, o Options) <-chan Event {
	events := make(chan Event, o.Jobs*4)
	go func() {
		defer close(events)
		var wg sync.WaitGroup
		sem := make(chan struct{}, o.Jobs)
		pacer := time.NewTicker(o.Pace)
		defer pacer.Stop()
		for i, t := range tracks {
			if ctx.Err() != nil {
				break
			}
			select {
			case <-pacer.C: // one launch per pace interval, across all workers
			case <-ctx.Done():
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(idx int, tr scout.Track) {
				defer wg.Done()
				defer func() { <-sem }()
				attempt(ctx, idx, tr, o, events)
			}(i, t)
		}
		wg.Wait()
	}()
	return events
}

// attempt runs download with 403-aware backoff: a rate-limit refusal parks the
// worker instead of burning the track, because the block is per-ip and decays.
func attempt(ctx context.Context, idx int, t scout.Track, o Options, events chan<- Event) {
	for try := 0; ; try++ {
		detail, ok := download(ctx, idx, t, o, events)
		if ok || ctx.Err() != nil {
			return
		}
		rateLimited := strings.Contains(detail, "403")
		if !rateLimited || try >= o.Retries {
			events <- Event{Idx: idx, Kind: Failed, Detail: detail}
			return
		}
		wait := backoffBase << try
		events <- Event{Idx: idx, Kind: Backoff, Detail: fmt.Sprintf("rate limited, waiting %s", wait)}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
	}
}

// download runs one yt-dlp invocation; on success it emits Done/Skipped and
// returns ok, on failure it returns the error detail for attempt to judge.
func download(ctx context.Context, idx int, t scout.Track, o Options, events chan<- Event) (string, bool) {
	events <- Event{Idx: idx, Kind: Started}
	cmd := exec.CommandContext(ctx, "yt-dlp", args(t, o)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Sprintf("pipe: %v", err), false
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Sprintf("start: %v", err), false
	}

	skipped := scanProgress(stdout, idx, events)
	if err := cmd.Wait(); err != nil {
		return lastLine(stderr.String()), false
	}
	if skipped {
		events <- Event{Idx: idx, Kind: Skipped}
		return "", true
	}
	events <- Event{Idx: idx, Kind: Done}
	return "", true
}

// scanProgress forwards percent updates and reports whether the archive
// already had this track.
func scanProgress(r interface{ Read([]byte) (int, error) }, idx int, events chan<- Event) bool {
	skipped := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "has already been recorded in the archive") {
			skipped = true
			continue
		}
		if m := progressRe.FindStringSubmatch(line); m != nil {
			if pct, err := strconv.ParseFloat(m[1], 64); err == nil {
				events <- Event{Idx: idx, Kind: Progress, Pct: pct}
			}
		}
	}
	return skipped
}

func args(t scout.Track, o Options) []string {
	return []string{
		"--newline", "--no-warnings",
		"-f", audioFormat,
		"--embed-metadata", "--embed-thumbnail",
		"--windows-filenames",
		"--download-archive", o.Archive,
		"-o", template(t, o.Dest),
		t.URL,
	}
}

// template decides where a track lives: sets become numbered album folders,
// loose singles go flat under the site folder as "uploader - title".
func template(t scout.Track, dest string) string {
	site := siteDir(t.URL)
	if t.Collection != "" {
		prefix := fmt.Sprintf("%02d ", t.Index)
		return filepath.Join(dest, site, sanitize(t.Collection), prefix+"%(title)s.%(ext)s")
	}
	return filepath.Join(dest, site, "%(uploader)s - %(title)s.%(ext)s")
}

func siteDir(url string) string {
	switch {
	case strings.Contains(url, "soundcloud"):
		return "SoundCloud"
	case strings.Contains(url, "youtu"):
		return "YouTube"
	case strings.Contains(url, "bandcamp"):
		return "Bandcamp"
	default:
		return "Other"
	}
}

func sanitize(name string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, name), ". ")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		return "yt-dlp failed with no output"
	}
	return lines[len(lines)-1]
}
