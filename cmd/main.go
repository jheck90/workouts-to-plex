package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fsnotify/fsnotify"
	"github.com/jheck90/workouts-to-plex/internal/converter"
)

func main() {
	inputDir := getEnv("INPUT_DIR", "/input")
	outputDir := getEnv("OUTPUT_DIR", "/output")
	timerSeconds, _ := strconv.Atoi(getEnv("TIMER_SECONDS", "60"))

	conv := converter.New(outputDir, timerSeconds)

	log.Printf("workouts-to-plex starting")
	log.Printf("  input:  %s", inputDir)
	log.Printf("  output: %s", outputDir)
	log.Printf("  timer:  %ds", timerSeconds)

	// Process any images already in the input directory on startup
	processExisting(inputDir, conv)

	// Watch for new files
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
						log.Printf("error: %v", err)
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
