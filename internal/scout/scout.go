// Package scout expands a link (track, set, likes page) into a flat list of
// downloadable tracks before any bytes move.
package scout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Track is one downloadable item with enough context to name its file well.
type Track struct {
	ID         string
	URL        string
	Title      string
	Uploader   string
	Collection string // set/album title; empty means loose single
	Index      int    // 1-based position within Collection
}

// maxDepth guards recursion: a likes page may contain sets, but sets don't
// contain sets, so two levels covers everything soundcloud can express.
const maxDepth = 2

type entry struct {
	Type     string  `json:"_type"`
	ID       string  `json:"id"`
	URL      string  `json:"url"`
	WebURL   string  `json:"webpage_url"`
	Title    string  `json:"title"`
	Uploader string  `json:"uploader"`
	Entries  []entry `json:"entries"`
}

// Expand resolves url into tracks, recursing into sets found inside likes
// pages. The context bounds the metadata calls, not any download.
func Expand(ctx context.Context, url string) ([]Track, error) {
	return expand(ctx, url, 1)
}

func expand(ctx context.Context, url string, depth int) ([]Track, error) {
	if depth > maxDepth {
		return nil, nil
	}
	root, err := probe(ctx, url)
	if err != nil {
		return nil, err
	}
	if root.Type != "playlist" {
		// a full (non-flat) probe fills `url` with the raw media stream;
		// the track's page url is what the downloader needs.
		single := *root
		if single.WebURL != "" {
			single.URL = single.WebURL
		}
		return []Track{trackFrom(single, "", 0)}, nil
	}

	collection := ""
	if isSet(url) {
		collection = root.Title
	}
	var tracks []Track
	for i, e := range root.Entries {
		child := e.URL
		if child == "" {
			child = e.WebURL
		}
		if isSet(child) {
			sub, err := expand(ctx, child, depth+1)
			if err != nil {
				return nil, fmt.Errorf("expanding nested set %s: %w", child, err)
			}
			tracks = append(tracks, sub...)
			continue
		}
		tracks = append(tracks, trackFrom(e, collection, i+1))
	}
	return tracks, nil
}

func probe(ctx context.Context, url string) (*entry, error) {
	// likes pages paginate through many api calls; unthrottled they trip the
	// same rate limiter the downloads do.
	cmd := exec.CommandContext(ctx, "yt-dlp", "-J", "--flat-playlist", "--no-warnings",
		"--sleep-requests", "1", url)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("probing %s: %w (%s)", url, err, exitDetail(err))
	}
	var e entry
	if err := json.Unmarshal(out, &e); err != nil {
		return nil, fmt.Errorf("parsing metadata for %s: %w", url, err)
	}
	return &e, nil
}

func trackFrom(e entry, collection string, index int) Track {
	url := e.URL
	if url == "" {
		url = e.WebURL
	}
	return Track{
		ID:         e.ID,
		URL:        url,
		Title:      e.Title,
		Uploader:   e.Uploader,
		Collection: collection,
		Index:      index,
	}
}

func isSet(url string) bool {
	return strings.Contains(url, "/sets/")
}

func exitDetail(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		lines := strings.Split(strings.TrimSpace(string(ee.Stderr)), "\n")
		return lines[len(lines)-1]
	}
	return "no stderr"
}
