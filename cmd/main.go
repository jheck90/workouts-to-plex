package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jheck90/workouts-to-plex/internal/converter"
	"github.com/jheck90/workouts-to-plex/internal/generator"
	"github.com/jheck90/workouts-to-plex/internal/plex"
)

func main() {
	configPath := getEnv("CONFIG_PATH", "/config/workouts.yaml")
	inputDir := getEnv("INPUT_DIR", "/input")
	outputDir := getEnv("OUTPUT_DIR", "/output")

	log.Printf("workouts-to-plex starting")
	log.Printf("  config: %s", configPath)
	log.Printf("  input:  %s", inputDir)
	log.Printf("  output: %s", outputDir)

	// generatedFrames tracks PNGs written by the generator so the watcher and
	// processExisting don't re-convert them as standalone videos.
	generatedFrames := map[string]bool{}

	// --- Generate images from workouts.yaml ---
	if _, err := os.Stat(configPath); err == nil {
		frames, err := runGenerator(configPath, inputDir, outputDir)
		if err != nil {
			log.Printf("generator error: %v", err)
		}
		for _, p := range frames {
			generatedFrames[filepath.Clean(p)] = true
		}
	} else {
		log.Printf("no config found at %s, skipping generation step", configPath)
	}

	// --- Convert any manually dropped images to video ---
	conv := converter.New(outputDir)

	processExisting(inputDir, conv, generatedFrames)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("failed to create watcher: %v", err)
	}
	defer watcher.Close()

	if err := watcher.Add(inputDir); err != nil {
		log.Fatalf("failed to watch %s: %v", inputDir, err)
	}

	log.Printf("watching %s for new images...", inputDir)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
				clean := filepath.Clean(event.Name)
				if !generatedFrames[clean] && conv.IsSupported(event.Name) {
					if err := conv.Convert(event.Name); err != nil {
						log.Printf("convert error: %v", err)
					}
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

// runGenerator generates frame PNGs and converts them to Plex MP4s.
// Returns the paths of all frame PNGs that were written to inputDir.
func runGenerator(configPath, inputDir, outputDir string) ([]string, error) {
	cfg, err := generator.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	gen, err := generator.New(inputDir)
	if err != nil {
		return nil, err
	}

	conv := converter.New(outputDir)

	results, err := gen.GenerateAll(cfg)
	if err != nil {
		return nil, err
	}

	var allFrames []string
	year := time.Now().Year()
	for _, r := range results {
		allFrames = append(allFrames, r.PNGPaths...)
		outPath := plexOutputPath(outputDir, r.Workout, year)
		if r.Regenerated {
			if err := os.Remove(outPath); err == nil {
				log.Printf("removed stale MP4: %s", filepath.Base(outPath))
			}
		}
		timer := r.Workout.TimerSeconds
		if timer == 0 {
			timer = 60
		}
		totalMinutes := r.Workout.TotalMinutes
		if totalMinutes == 0 {
			totalMinutes = 60
		}
		warmupCount := 0
		if len(r.Workout.Warmup) > 0 {
			warmupCount = 1
		}

		warmupChapters, repeatingChapters := buildChapters(r.Workout, warmupCount)

		if err := conv.ConvertFramesTo(r.PNGPaths, outPath, timer, totalMinutes, warmupCount, warmupChapters, repeatingChapters); err != nil {
			log.Printf("convert error for %s: %v", r.Workout.Name, err)
			continue
		}

		// Plex metadata: NFO description + thumbnails.
		if err := plex.WriteNFO(outPath, r.Workout, year); err != nil {
			log.Printf("NFO error for %s: %v", r.Workout.Name, err)
		}
		if len(r.PNGPaths) > 0 {
			if err := plex.WriteEpisodeThumb(outPath, r.PNGPaths[0]); err != nil {
				log.Printf("thumb error for %s: %v", r.Workout.Name, err)
			}
			categoryDir := filepath.Dir(outPath)
			if err := plex.WriteShowPoster(categoryDir, r.PNGPaths[0]); err != nil {
				log.Printf("poster error for %s: %v", r.Workout.Name, err)
			}
		}
	}
	return allFrames, nil
}

// buildChapters constructs chapter title slices for the warmup and repeating frames.
func buildChapters(w generator.Workout, warmupCount int) (warmup []converter.Chapter, repeating []converter.Chapter) {
	if warmupCount > 0 {
		warmup = []converter.Chapter{{Title: "Warmup"}}
	}
	if w.Heavy != nil {
		repeating = append(repeating, converter.Chapter{
			Title: fmt.Sprintf("Min 1: %s", w.Heavy.Movement),
		})
	}
	for _, r := range w.Rounds {
		names := make([]string, len(r.Exercises))
		for i, ex := range r.Exercises {
			names[i] = ex.Movement
		}
		repeating = append(repeating, converter.Chapter{
			Title: fmt.Sprintf("Min %d: %s", r.Minute, strings.Join(names, " + ")),
		})
	}
	return
}

// plexOutputPath builds a Plex-friendly path:
// <outputDir>/<Category>/s<year>e<episode> - <Name>.mp4
func plexOutputPath(outputDir string, w generator.Workout, year int) string {
	category := w.Category
	if category == "" {
		category = "Workouts"
	}
	filename := fmt.Sprintf("s%de%02d - %s.mp4", year, w.Episode, w.Name)
	return filepath.Join(outputDir, category, filename)
}

func processExisting(dir string, conv *converter.Converter, skip map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("could not read input dir: %v", err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if skip[filepath.Clean(path)] {
			continue
		}
		if conv.IsSupported(path) {
			if err := conv.Convert(path); err != nil {
				log.Printf("error processing %s: %v", entry.Name(), err)
			}
		}
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
