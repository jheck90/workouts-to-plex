package generator

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
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
}

type Round struct {
	Minute    int        `yaml:"minute"`
	Exercises []Exercise `yaml:"exercises"`
}

type Exercise struct {
	Movement string `yaml:"movement"`
	Reps     int    `yaml:"reps"`
	Note     string `yaml:"note"`
}

// --- Template data ---

type templateData struct {
	Name     string
	Subtitle string
	Notes    string
	Width    int
	Height   int
	Columns  int
	Warmup   []string
	Heavy    *Heavy
	Rounds   []Round
}

// --- Generator ---

type Generator struct {
	outputDir string
	tmpl      *template.Template
}

func New(outputDir string) (*Generator, error) {
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
	}, nil
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

// GenerateResult pairs a generated PNG path with its source Workout.
// Regenerated is true when the config changed and outputs were rebuilt.
type GenerateResult struct {
	PNGPath     string
	Workout     Workout
	Regenerated bool
}

// GenerateAll renders every workout in the config to a PNG in outputDir.
func (g *Generator) GenerateAll(cfg *Config) ([]GenerateResult, error) {
	var results []GenerateResult
	for _, w := range cfg.Workouts {
		path, regen, err := g.Generate(cfg.Settings, w)
		if err != nil {
			log.Printf("failed to generate %q: %v", w.Name, err)
			continue
		}
		results = append(results, GenerateResult{PNGPath: path, Workout: w, Regenerated: regen})
	}
	return results, nil
}

// Generate renders one workout card to a PNG.
// Returns the PNG path, whether it was regenerated, and any error.
func (g *Generator) Generate(s Settings, w Workout) (string, bool, error) {
	slug := slugify(w.Name)
	htmlPath := filepath.Join(os.TempDir(), slug+".html")
	pngPath := filepath.Join(g.outputDir, slug+".png")
	hashPath := filepath.Join(g.outputDir, slug+".sha256")

	hash := workoutHash(s, w)

	// Check if existing PNG matches the current config hash
	if storedHash, err := os.ReadFile(hashPath); err == nil {
		if strings.TrimSpace(string(storedHash)) == hash {
			log.Printf("skipping %q — config unchanged", w.Name)
			return pngPath, false, nil
		}
		log.Printf("config changed for %q — regenerating", w.Name)
		os.Remove(pngPath)
	}

	roundCount := len(w.Rounds)
	if w.Heavy != nil {
		roundCount++
	}

	data := templateData{
		Name:     w.Name,
		Subtitle: w.Subtitle,
		Notes:    w.Notes,
		Width:    s.Width,
		Height:   s.Height,
		Columns:  columns(roundCount),
		Warmup:   w.Warmup,
		Heavy:    w.Heavy,
		Rounds:   w.Rounds,
	}

	f, err := os.Create(htmlPath)
	if err != nil {
		return "", false, fmt.Errorf("could not create html file: %w", err)
	}
	if err := g.tmpl.Execute(f, data); err != nil {
		f.Close()
		return "", false, fmt.Errorf("template render failed: %w", err)
	}
	f.Close()

	log.Printf("screenshotting %q -> %s", w.Name, filepath.Base(pngPath))

	cmd := exec.Command("chromium-browser",
		"--headless",
		"--disable-gpu",
		"--no-sandbox",
		"--disable-dev-shm-usage",
		fmt.Sprintf("--window-size=%d,%d", s.Width, s.Height),
		fmt.Sprintf("--screenshot=%s", pngPath),
		"file://"+htmlPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("chromium screenshot failed: %w", err)
	}

	// Save hash so next run can detect changes
	if err := os.WriteFile(hashPath, []byte(hash), 0644); err != nil {
		log.Printf("warning: could not save hash for %q: %v", w.Name, err)
	}

	log.Printf("generated: %s", pngPath)
	return pngPath, true, nil
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

func Slugify(s string) string { return slugify(s) }

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
