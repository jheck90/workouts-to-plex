package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jheck90/workouts-to-plex/internal/converter"
	"github.com/jheck90/workouts-to-plex/internal/generator"
	"github.com/jheck90/workouts-to-plex/internal/plex"
	"github.com/jheck90/workouts-to-plex/internal/ui"
)

func main() {
	configPath := getEnv("CONFIG_PATH", "/config/workouts.yaml")
	inputDir   := getEnv("INPUT_DIR", "/input")
	outputDir  := getEnv("OUTPUT_DIR", "/output")
	verbose    := getEnv("VERBOSE", "") == "true"

	ui.SetVerbose(verbose)

	var logOut io.Writer = io.Discard
	if verbose {
		logOut = os.Stderr
	}

	fmt.Println("workouts-to-plex")

	// generatedFrames tracks PNGs written by the generator so the watcher and
	// processExisting don't re-convert them as standalone videos.
	generatedFrames := map[string]bool{}

	if _, err := os.Stat(configPath); err == nil {
		frames, err := runGenerator(configPath, inputDir, outputDir, logOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "generator error: %v\n", err)
		}
		for _, p := range frames {
			generatedFrames[filepath.Clean(p)] = true
		}
	} else {
		fmt.Fprintf(os.Stderr, "no config found at %s, skipping generation step\n", configPath)
	}

	conv := converter.New(outputDir, logOut)
	processExisting(inputDir, conv, generatedFrames)

	if getEnv("BATCH_MODE", "") == "true" {
		fmt.Println("done.")
		return
	}

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
func runGenerator(configPath, inputDir, outputDir string, logOut io.Writer) ([]string, error) {
	cfg, err := generator.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	gen, err := generator.New(inputDir, logOut)
	if err != nil {
		return nil, err
	}

	// Pre-check cache status and show the workout table before any work starts.
	statuses := gen.PrecheckAll(cfg)
	uiStatuses := make([]ui.WorkoutStatus, len(statuses))
	for i, s := range statuses {
		uiStatuses[i] = ui.WorkoutStatus{Name: s.Name, Episode: s.Episode, Regenerated: s.Changed}
	}
	ui.PrintTable(uiStatuses)

	conv := converter.New(outputDir, logOut)

	results, err := gen.GenerateAll(cfg)
	if err != nil {
		return nil, err
	}

	disp := ui.NewDisplay(len(results))

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

		// Build FrameInput slice: correlate each PNG with its demo video (if any).
		// Warmup frames never carry a video. Strength and round frames use the
		// video path from Heavy.Video and Round.Video respectively.
		highlights := generator.FrameHighlights(r.Workout)
		frameInputs := make([]converter.FrameInput, len(r.PNGPaths))
		for i, png := range r.PNGPaths {
			fi := converter.FrameInput{PNGPath: png}
			if i >= warmupCount && i < len(highlights) {
				h := highlights[i]
				switch h {
				case "strength":
					if r.Workout.Heavy != nil {
						fi.VideoPath = r.Workout.Heavy.Video
					}
				default:
					if min, err := strconv.Atoi(h); err == nil {
						for _, round := range r.Workout.Rounds {
							if round.Minute == min && round.Video != "" {
								fi.VideoPath = round.Video
								break
							}
						}
					}
				}
			}
			frameInputs[i] = fi
		}

		w := r.Settings.Width
		h := r.Settings.Height
		if w == 0 {
			w = 1920
		}
		if h == 0 {
			h = 1080
		}

		warmupChapters, repeatingChapters := buildChapters(r.Workout, warmupCount)

		disp.StartWorkout(r.Workout.Name, len(r.PNGPaths))
		if err := conv.ConvertFramesTo(frameInputs, outPath, timer, totalMinutes, warmupCount, w, h, warmupChapters, repeatingChapters, func() {
			disp.FrameDone()
		}); err != nil {
			log.Printf("convert error for %s: %v", r.Workout.Name, err)
			disp.WorkoutDone()
			continue
		}
		disp.WorkoutDone()

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
	disp.Finish()

	return allFrames, nil
}

// buildChapters constructs chapter title slices for the warmup and repeating frames.
func buildChapters(w generator.Workout, warmupCount int) (warmup []converter.Chapter, repeating []converter.Chapter) {
	if warmupCount > 0 {
		warmup = []converter.Chapter{{Title: "Warmup"}}
	}
	if w.Heavy != nil {
		repeating = append(repeating, converter.Chapter{
			Title: fmt.Sprintf("Strength: %s", w.Heavy.Movement),
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
