// baleen sifts music links (tracks, sets, likes pages) into a tidy local
// library. Links come from argv or stdin, one per line.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vmfunc/baleen/internal/fetch"
	"github.com/vmfunc/baleen/internal/ui"
)

const (
	defaultJobs = 3
	// soundcloud's api starts refusing after a burst of unthrottled metadata
	// calls; one launch every 2s stays comfortably under it.
	defaultPace    = 2 * time.Second
	defaultRetries = 4
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "baleen:", err)
		os.Exit(1)
	}
}

func run() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home: %w", err)
	}
	dest := flag.String("dest", filepath.Join(home, "Music"), "library root")
	jobs := flag.Int("jobs", defaultJobs, "concurrent downloads")
	archive := flag.String("archive", "", "download archive (default <dest>/.baleen-archive.txt)")
	pace := flag.Duration("pace", defaultPace, "min interval between download launches")
	retries := flag.Int("retries", defaultRetries, "extra attempts per track after a rate-limit 403")
	flag.Parse()

	if *archive == "" {
		*archive = filepath.Join(*dest, ".baleen-archive.txt")
	}
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return fmt.Errorf("yt-dlp not found on PATH: %w", err)
	}
	urls, err := gatherURLs(flag.Args())
	if err != nil {
		return err
	}
	if len(urls) == 0 {
		return fmt.Errorf("no links given (argv or stdin, one per line)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts := fetch.Options{Dest: *dest, Archive: *archive, Jobs: *jobs, Pace: *pace, Retries: *retries}
	model := ui.New(ctx, cancel, urls, opts)
	prog := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithInputTTY())
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("running ui: %w", err)
	}
	return report(model.Summary(), *dest)
}

// gatherURLs merges argv links with stdin lines when stdin is a pipe,
// dropping duplicates while preserving order.
func gatherURLs(args []string) ([]string, error) {
	urls := append([]string{}, args...)
	stat, err := os.Stdin.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspecting stdin: %w", err)
	}
	if stat.Mode()&os.ModeCharDevice == 0 {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				urls = append(urls, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
	}
	seen := make(map[string]bool, len(urls))
	uniq := urls[:0]
	for _, u := range urls {
		if !seen[u] {
			seen[u] = true
			uniq = append(uniq, u)
		}
	}
	return uniq, nil
}

func report(s ui.Summary, dest string) error {
	fmt.Printf("baleen: %d fetched, %d already had, %d failed (of %d) → %s\n",
		s.Done, s.Skipped, s.Failed, s.Total, dest)
	for _, f := range s.Failures {
		fmt.Println("  ✗", f)
	}
	if s.Failed > 0 {
		return fmt.Errorf("%d tracks failed", s.Failed)
	}
	return nil
}
