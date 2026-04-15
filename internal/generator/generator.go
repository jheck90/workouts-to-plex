package generator

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// --- YAML config structures ---

type Config struct {
	Settings Settings  `yaml:"settings"`
	Workouts []Workout `yaml:"workouts"`
}

type Settings struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
}

type Workout struct {
	Name         string   `yaml:"name"`
	Subtitle     string   `yaml:"subtitle"`
	Category     string   `yaml:"category"`
	Episode      int      `yaml:"episode"`
	TimerSeconds int      `yaml:"timer_seconds"`
	TotalMinutes int      `yaml:"total_minutes"`
	Theme        string   `yaml:"theme"`
	Warmup       []string `yaml:"warmup"`
	Heavy        *Heavy   `yaml:"heavy"`
	Rounds       []Round  `yaml:"rounds"`
	Notes        string   `yaml:"notes"`
}

type Heavy struct {
	Movement string `yaml:"movement"`
	Sets     int    `yaml:"sets"`
	Reps     string `yaml:"reps"`
	Note     string `yaml:"note"`
	Video    string `yaml:"video,omitempty"` // abs path inside container to a demo MP4; injected into first pass only
}

type Round struct {
	Minute    int        `yaml:"minute"`
	Exercises []Exercise `yaml:"exercises"`
	Video     string     `yaml:"video,omitempty"` // abs path inside container to a demo MP4; injected into first EMOM pass only
}

type Exercise struct {
	Movement string `yaml:"movement"`
	Reps     int    `yaml:"reps"`
	Note     string `yaml:"note"`
}

// --- Template data ---

type templateData struct {
	Name         string
	Subtitle     string
	Notes        string
	Width        int
	Height       int
	Columns      int
	TimerSeconds int
	Warmup       []string
	Heavy        *Heavy
	Rounds       []Round
	PaddedRounds []*Round // always 8 entries, nil = empty tile
	Highlight    string   // "warmup", "1", "2", "3", ... — active slide
}

// CacheStatus describes whether a workout's frames are up-to-date.
type CacheStatus struct {
	Name    string
	Episode int
	Changed bool // true = hash mismatch or missing frames; will be regenerated
}

// --- Generator ---

type Generator struct {
	outputDir string
	tmpl      *template.Template
	logOutput io.Writer
}

func New(outputDir string, logOutput io.Writer) (*Generator, error) {
	if logOutput == nil {
		logOutput = io.Discard
	}
	tmplPath := filepath.Join(filepath.Dir(os.Args[0]), "template.html")
	if _, err := os.Stat(tmplPath); err != nil {
		tmplPath = "/app/template.html"
	}

	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}

	return &Generator{
		outputDir: outputDir,
		tmpl:      tmpl,
		logOutput: logOutput,
	}, nil
}

// PrecheckAll performs a hash-and-file check for every workout without generating
// anything, so the caller can display a status table before work begins.
func (g *Generator) PrecheckAll(cfg *Config) []CacheStatus {
	var out []CacheStatus
	for _, w := range cfg.Workouts {
		slug := fmt.Sprintf("%s_ep%d", slugify(w.Name), w.Episode)
		hash := workoutHash(cfg.Settings, w)
		hashPath := filepath.Join(g.outputDir, slug+".sha256")
		changed := true
		if stored, err := os.ReadFile(hashPath); err == nil {
			if strings.TrimSpace(string(stored)) == hash {
				highlights := FrameHighlights(w)
				allExist := true
				for _, h := range highlights {
					if _, err := os.Stat(filepath.Join(g.outputDir, slug+"_"+h+".png")); err != nil {
						allExist = false
						break
					}
				}
				changed = !allExist
			}
		}
		out = append(out, CacheStatus{Name: w.Name, Episode: w.Episode, Changed: changed})
	}
	return out
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return &cfg, nil
}

// GenerateResult holds the ordered frame PNGs for one workout.
// Regenerated is true when the config changed and frames were rebuilt.
type GenerateResult struct {
	PNGPaths    []string // ordered: warmup, min1, min2, ...
	Workout     Workout
	Settings    Settings
	Regenerated bool
}

// GenerateAll renders every workout's frame sequence to the input dir.
func (g *Generator) GenerateAll(cfg *Config) ([]GenerateResult, error) {
	var results []GenerateResult
	for _, w := range cfg.Workouts {
		paths, regen, err := g.GenerateFrames(cfg.Settings, w)
		if err != nil {
			log.Printf("failed to generate %q: %v", w.Name, err)
			continue
		}
		results = append(results, GenerateResult{PNGPaths: paths, Workout: w, Settings: cfg.Settings, Regenerated: regen})
	}
	return results, nil
}

// GenerateFrames produces one PNG per slide (warmup + each minute), each with a
// different section highlighted. Returns frame paths in display order.
func (g *Generator) GenerateFrames(s Settings, w Workout) ([]string, bool, error) {
	slug := slugify(w.Name)
	hashPath := filepath.Join(g.outputDir, slug+".sha256")
	hash := workoutHash(s, w)

	highlights := FrameHighlights(w)

	// Check stored hash — if unchanged and all frame files exist, skip regeneration
	if storedHash, err := os.ReadFile(hashPath); err == nil {
		if strings.TrimSpace(string(storedHash)) == hash {
			paths := make([]string, len(highlights))
			allExist := true
			for i, h := range highlights {
				p := filepath.Join(g.outputDir, slug+"_"+h+".png")
				paths[i] = p
				if _, err := os.Stat(p); err != nil {
					allExist = false
					break
				}
			}
			if allExist {
				log.Printf("skipping %q — config unchanged", w.Name)
				return paths, false, nil
			}
			log.Printf("regenerating %q — frame files missing", w.Name)
		}
		log.Printf("config changed for %q — regenerating all frames", w.Name)
		// Remove stale frames
		for _, h := range highlights {
			os.Remove(filepath.Join(g.outputDir, slug+"_"+h+".png"))
		}
	}

	roundCount := len(w.Rounds)
	if w.Heavy != nil {
		roundCount++
	}

	var paths []string
	for _, highlight := range highlights {
		path, err := g.renderFrame(s, w, slug, highlight, roundCount)
		if err != nil {
			return nil, false, err
		}
		paths = append(paths, path)
	}

	if err := os.WriteFile(hashPath, []byte(hash), 0644); err != nil {
		log.Printf("warning: could not save hash for %q: %v", w.Name, err)
	}

	return paths, true, nil
}

// renderFrame renders one PNG with the given section highlighted.
func (g *Generator) renderFrame(s Settings, w Workout, slug, highlight string, roundCount int) (string, error) {
	pngPath := filepath.Join(g.outputDir, slug+"_"+highlight+".png")
	htmlPath := filepath.Join(os.TempDir(), slug+"_"+highlight+".html")

	timer := w.TimerSeconds
	if timer == 0 {
		timer = 60
	}
	padded := make([]*Round, 8)
	for i, r := range w.Rounds {
		if i < 8 {
			rc := r
			padded[i] = &rc
		}
	}
	data := templateData{
		Name:         w.Name,
		Subtitle:     w.Subtitle,
		Notes:        w.Notes,
		Width:        s.Width,
		Height:       s.Height,
		Columns:      columns(roundCount),
		TimerSeconds: timer,
		Warmup:       w.Warmup,
		Heavy:        w.Heavy,
		Rounds:       w.Rounds,
		PaddedRounds: padded,
		Highlight:    highlight,
	}

	f, err := os.Create(htmlPath)
	if err != nil {
		return "", fmt.Errorf("could not create html file: %w", err)
	}
	if err := g.tmpl.Execute(f, data); err != nil {
		f.Close()
		return "", fmt.Errorf("template render failed: %w", err)
	}
	f.Close()

	log.Printf("screenshotting %q [%s] -> %s", w.Name, highlight, filepath.Base(pngPath))

	cmd := exec.Command("chromium-browser",
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		fmt.Sprintf("--window-size=%d,%d", s.Width, s.Height),
		fmt.Sprintf("--screenshot=%s", pngPath),
		"file://"+htmlPath,
	)
	cmd.Stdout = g.logOutput
	cmd.Stderr = g.logOutput

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("chromium screenshot failed for frame %s: %w", highlight, err)
	}

	return pngPath, nil
}

// FrameHighlights returns the ordered list of highlight keys for a workout.
// Heavy fires after the first 4 non-empty rounds (or all rounds if fewer than 4),
// then remaining rounds follow. Empty rounds (no exercises) are skipped.
func FrameHighlights(w Workout) []string {
	var h []string
	if len(w.Warmup) > 0 {
		h = append(h, "warmup")
	}

	// Collect non-empty round keys in order
	var populated []string
	for _, r := range w.Rounds {
		if len(r.Exercises) > 0 {
			populated = append(populated, fmt.Sprintf("%d", r.Minute))
		}
	}

	// Split at 4: first half → heavy → second half
	splitAt := 4
	if len(populated) < splitAt {
		splitAt = len(populated)
	}
	h = append(h, populated[:splitAt]...)
	if w.Heavy != nil {
		h = append(h, "strength")
	}
	h = append(h, populated[splitAt:]...)
	// Fire strength again at the end so the cycle closes: R1–4 → strength → R5–8 → strength → (repeat)
	if w.Heavy != nil && len(populated) > splitAt {
		h = append(h, "strength")
	}

	return h
}

// workoutHash returns a stable SHA256 of the workout config + settings.
func workoutHash(s Settings, w Workout) string {
	payload := struct {
		Settings Settings
		Workout  Workout
	}{s, w}
	b, _ := json.Marshal(payload)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "_")
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func columns(rounds int) int {
	switch {
	case rounds <= 2:
		return rounds
	case rounds <= 4:
		return 2
	case rounds <= 5:
		return 5
	case rounds <= 6:
		return 3
	default:
		return 4
	}
}
