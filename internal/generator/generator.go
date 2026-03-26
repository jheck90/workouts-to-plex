package generator

import (
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
	Name         string  `yaml:"name"`
	Subtitle     string  `yaml:"subtitle"`
	TimerSeconds int     `yaml:"timer_seconds"`
	Theme        string  `yaml:"theme"`
	Rounds       []Round `yaml:"rounds"`
	Notes        string  `yaml:"notes"`
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
	Rounds   []Round
}

// --- Generator ---

type Generator struct {
	outputDir string
	tmpl      *template.Template
}

func New(outputDir string) (*Generator, error) {
	tmplPath := filepath.Join(filepath.Dir(os.Args[0]), "template.html")

	// Fallback: look relative to binary location or embedded path
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

// GenerateAll renders every workout in the config to a PNG in outputDir.
func (g *Generator) GenerateAll(cfg *Config) ([]string, error) {
	var outputs []string
	for _, w := range cfg.Workouts {
		path, err := g.Generate(cfg.Settings, w)
		if err != nil {
			log.Printf("failed to generate %q: %v", w.Name, err)
			continue
		}
		outputs = append(outputs, path)
	}
	return outputs, nil
}

// Generate renders one workout card to a PNG and returns its path.
func (g *Generator) Generate(s Settings, w Workout) (string, error) {
	slug := slugify(w.Name)
	htmlPath := filepath.Join(os.TempDir(), slug+".html")
	pngPath := filepath.Join(g.outputDir, slug+".png")

	if _, err := os.Stat(pngPath); err == nil {
		log.Printf("skipping %q — PNG already exists", w.Name)
		return pngPath, nil
	}

	cols := columns(len(w.Rounds))

	data := templateData{
		Name:     w.Name,
		Subtitle: w.Subtitle,
		Notes:    w.Notes,
		Width:    s.Width,
		Height:   s.Height,
		Columns:  cols,
		Rounds:   w.Rounds,
	}

	f, err := os.Create(htmlPath)
	if err != nil {
		return "", fmt.Errorf("could not create html file: %w", err)
	}
	defer f.Close()

	if err := g.tmpl.Execute(f, data); err != nil {
		return "", fmt.Errorf("template render failed: %w", err)
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
		return "", fmt.Errorf("chromium screenshot failed: %w", err)
	}

	log.Printf("generated: %s", pngPath)
	return pngPath, nil
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
	if rounds <= 2 {
		return rounds
	}
	if rounds <= 4 {
		return 2
	}
	if rounds <= 6 {
		return 3
	}
	return 4
}
