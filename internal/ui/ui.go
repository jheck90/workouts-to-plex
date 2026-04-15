package ui

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
)

const barWidth = 32

// SetVerbose re-enables the standard logger. By default it is silenced.
func SetVerbose(v bool) {
	if v {
		log.SetOutput(os.Stderr)
	} else {
		log.SetOutput(io.Discard)
	}
}

// WorkoutStatus describes one workout for the pre-run table.
type WorkoutStatus struct {
	Name        string
	Episode     int
	Regenerated bool // true = frames changed, will re-encode
}

// PrintTable prints the workout list before processing begins.
func PrintTable(statuses []WorkoutStatus) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	for _, s := range statuses {
		icon := "\033[90m✓\033[0m"
		label := "\033[90mcached\033[0m"
		if s.Regenerated {
			icon = "\033[33m↻\033[0m"
			label = "\033[33mregenerating\033[0m"
		}
		fmt.Fprintf(w, "  %s  %s\tep%02d\t%s\n", icon, s.Name, s.Episode, label)
	}
	w.Flush()
	fmt.Println()
}

// Display manages a two-line live progress area:
//
//	  <workout name>        |████████░░░░░░░░| N/M frames
//	  Overall               |████░░░░░░░░░░░░| N/M workouts
type Display struct {
	mu    sync.Mutex
	lines int // lines currently on screen (0 until first paint)

	workoutLabel string
	workoutCur   int
	workoutTot   int

	overallCur int
	overallTot int
}

// NewDisplay creates a Display for the given total number of workouts.
func NewDisplay(total int) *Display {
	return &Display{overallTot: total}
}

// StartWorkout resets the per-workout bar for a new workout.
func (d *Display) StartWorkout(name string, frames int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workoutLabel = name
	d.workoutCur = 0
	d.workoutTot = frames
	d.paint()
}

// FrameDone advances the per-workout bar by one encoded frame.
func (d *Display) FrameDone() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.workoutCur < d.workoutTot {
		d.workoutCur++
	}
	d.paint()
}

// WorkoutDone marks the current workout complete and advances the overall bar.
func (d *Display) WorkoutDone() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workoutCur = d.workoutTot
	if d.overallCur < d.overallTot {
		d.overallCur++
	}
	d.paint()
}

// Finish marks everything complete and moves the cursor past the display area.
func (d *Display) Finish() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.workoutCur = d.workoutTot
	d.overallCur = d.overallTot
	d.paint()
	fmt.Println()
}

func (d *Display) paint() {
	if d.lines > 0 {
		// Move cursor up to overwrite previous bars.
		fmt.Printf("\033[%dA", d.lines)
	}
	fmt.Printf("\r\033[K  %-22s %s\n", trunc(d.workoutLabel, 22), renderBar(d.workoutCur, d.workoutTot, "frames"))
	fmt.Printf("\r\033[K  %-22s %s\n", "Overall", renderBar(d.overallCur, d.overallTot, "workouts"))
	d.lines = 2
}

func renderBar(cur, total int, unit string) string {
	pct := 0.0
	if total > 0 {
		pct = float64(cur) / float64(total)
	}
	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	return fmt.Sprintf("|%s%s| %d/%d %s",
		strings.Repeat("█", filled),
		strings.Repeat("░", barWidth-filled),
		cur, total, unit,
	)
}

func trunc(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
