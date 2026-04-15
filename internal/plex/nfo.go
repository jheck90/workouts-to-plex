package plex

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jheck90/workouts-to-plex/internal/generator"
)

type episodeDetails struct {
	XMLName   xml.Name  `xml:"episodedetails"`
	Title     string    `xml:"title"`
	Tagline   string    `xml:"tagline,omitempty"`
	ShowTitle string    `xml:"showtitle"`
	Season    int       `xml:"season"`
	Episode   int       `xml:"episode"`
	Plot      string    `xml:"plot"`
	Runtime   int       `xml:"runtime"`
	Thumb     *nfoThumb `xml:"thumb,omitempty"`
}

type nfoThumb struct {
	Aspect string `xml:"aspect,attr"`
	Value  string `xml:",chardata"`
}

// WriteNFO writes a Plex-compatible .nfo file alongside outputPath.
// Requires Plex's "Local Media Assets" scanner to be enabled.
func WriteNFO(outputPath string, w generator.Workout, year int) error {
	thumbName := strings.TrimSuffix(filepath.Base(outputPath), ".mp4") + "-thumb.jpg"

	details := episodeDetails{
		Title:     w.Name,
		Tagline:   w.Subtitle,
		ShowTitle: w.Category,
		Season:    year,
		Episode:   w.Episode,
		Plot:      buildPlot(w),
		Runtime:   w.TotalMinutes,
		Thumb:     &nfoThumb{Aspect: "thumb", Value: thumbName},
	}

	data, err := xml.MarshalIndent(details, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal NFO: %w", err)
	}

	nfoPath := strings.TrimSuffix(outputPath, ".mp4") + ".nfo"
	return os.WriteFile(nfoPath, append([]byte(xml.Header), data...), 0644)
}

func buildPlot(w generator.Workout) string {
	var sb strings.Builder

	if len(w.Warmup) > 0 {
		sb.WriteString("WARMUP\n")
		for _, item := range w.Warmup {
			sb.WriteString("  • " + item + "\n")
		}
		sb.WriteString("\n")
	}

	if w.Heavy != nil {
		sb.WriteString(fmt.Sprintf("STRENGTH — %s: %d×%s", w.Heavy.Movement, w.Heavy.Sets, w.Heavy.Reps))
		if w.Heavy.Note != "" {
			sb.WriteString("  " + w.Heavy.Note)
		}
		sb.WriteString("\n")
	}

	for _, r := range w.Rounds {
		names := make([]string, len(r.Exercises))
		for i, ex := range r.Exercises {
			entry := ex.Movement
			if ex.Reps > 0 {
				entry = fmt.Sprintf("%dx %s", ex.Reps, ex.Movement)
			}
			if ex.Note != "" {
				entry += "  " + ex.Note
			}
			names[i] = entry
		}
		sb.WriteString(fmt.Sprintf("MIN %d — %s\n", r.Minute, strings.Join(names, " + ")))
	}

	if w.Notes != "" {
		sb.WriteString("\n" + w.Notes)
	}

	return sb.String()
}
