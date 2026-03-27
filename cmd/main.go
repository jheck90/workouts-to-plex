package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jheck90/workouts-to-plex/internal/converter"
	"github.com/jheck90/workouts-to-plex/internal/generator"
)

func main() {
	configPath := getEnv("CONFIG_PATH", "/config/workouts.yaml")
	inputDir := getEnv("INPUT_DIR", "/input")
	outputDir := getEnv("OUTPUT_DIR", "/output")

	log.Printf("workouts-to-plex starting")
	log.Printf("  config: %s", configPath)
	log.Printf("  input:  %s", inputDir)
	log.Printf("  output: %s", outputDir)

	// --- Generate images from workouts.yaml ---
	if _, err := os.Stat(configPath); err == nil {
		if err := runGenerator(configPath, inputDir, outputDir); err != nil {
			log.Printf("generator error: %v", err)
		}
	} else {
		log.Printf("no config found at %s, skipping generation step", configPath)
	}

	// --- Convert any images (pre-existing + newly dropped) to video ---
	conv := converter.New(outputDir)

	processExisting(inputDir, conv)

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
				if conv.IsSupported(event.Name) {
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

func runGenerator(configPath, inputDir, outputDir string) error {
	cfg, err := generator.LoadConfig(configPath)
	if err != nil {
		return err
	}

	gen, err := generator.New(inputDir)
	if err != nil {
		return err
	}

	conv := converter.New(outputDir)

	pngs, err := gen.GenerateAll(cfg)
	if err != nil {
		return err
	}

	year := time.Now().Year()
	for _, r := range pngs {
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
		if err := conv.ConvertFramesTo(r.PNGPaths, outPath, timer, totalMinutes, warmupCount); err != nil {
			log.Printf("convert error for %s: %v", r.Workout.Name, err)
		}
	}
	return nil
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

func processExisting(dir string, conv *converter.Converter) {
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
