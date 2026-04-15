# Plan: Exercise Demo Video Injection

## Context
The workouts-to-plex pipeline generates looping MP4s where each 60-second cycle highlights one exercise tile. The goal is to inject a short demo video (from the Exercise Library on NFS) into the first pass of the video — highlight the tile for 3 seconds, cut to the demo video, cut back to the tile, then continue. Only the first EMOM cycle shows demos; all subsequent loops are plain. Warmup and strength tiles are excluded. Timer runs continuously throughout.

## Critical Files
- `internal/generator/generator.go` — `Round` struct, `frameHighlights` (unexported)
- `internal/converter/converter.go` — `encodeCycle`, `ConvertFramesTo`, `loopFrames`
- `cmd/main.go` — `runGenerator`, builds `PNGPaths` → calls `ConvertFramesTo`
- `poc/workouts-to-plex/workouts.yaml` — workout definitions (add `video:` to test rounds)

---

## Step 1 — YAML + Generator: Add `video` field to `Round`

**`internal/generator/generator.go`**

```go
type Round struct {
    Minute    int        `yaml:"minute"`
    Exercises []Exercise `yaml:"exercises"`
    Video     string     `yaml:"video,omitempty"` // abs path to demo MP4, optional
}
```

Export `frameHighlights` so `main.go` can correlate PNG paths to highlight keys:

```go
// FrameHighlights returns ordered highlight keys: "warmup", minute strings, "strength".
func FrameHighlights(w Workout) []string { ... } // rename from frameHighlights
```

Update all internal callers of `frameHighlights` → `FrameHighlights`.

---

## Step 2 — Converter: `FrameInput` type + video-aware encoding

**`internal/converter/converter.go`**

### 2a. New type
```go
type FrameInput struct {
    PNGPath   string
    VideoPath string // empty = plain tile cycle
}
```

### 2b. `videoDuration(path string) (float64, error)`
Uses `ffprobe` to get stream duration in seconds:
```
ffprobe -v quiet -print_format json -show_streams <path>
```
Parse `streams[0].duration` (string → float64).

### 2c. `encodeCycle` — add `fps int` parameter
Current `-r 1` becomes `-r {fps}`. Existing callers pass `fps=1`. Video-enhanced workouts pass `fps=30`.

### 2d. `encodeCycleWithVideo(tilePath, videoPath, outputPath string, timerSeconds, fps int) error`

Single FFmpeg call with `filter_complex`:

```
Inputs:
  -loop 1 -t {T}  -i tile.png           # 0: static tile, T seconds long
  -i demo.mp4                            # 1: demo video
  -f lavfi -i {beepFilter}              # 2: beep audio (T seconds)

filter_complex:
  [0:v]fps={fps},scale=W:H,split=2[tile_a][tile_b];

  ; Pre-tile: 0 → 3s, timer starts at T
  [tile_a]trim=end=3,setpts=PTS-STARTPTS,
    drawtext=...:text='%{eif\:T-t-1\:d}':x=...:y=...[pre_v];

  ; Demo video: scale/pad to WxH, timer offset by 3s
  [1:v]scale=W:H:force_original_aspect_ratio=decrease,
    pad=W:H:(ow-iw)/2:(oh-ih)/2,fps={fps},setpts=PTS-STARTPTS,
    trim=duration={cappedDemoDur},
    drawtext=...:text='%{eif\:T-3-t-1\:d}':x=...:y=...[demo_v];

  ; Post-tile: remaining seconds (T - 3 - cappedDemoDur), timer offset by 3+d
  [tile_b]trim=duration={postDur},setpts=PTS-STARTPTS,
    drawtext=...:text='%{eif\:T-{int(3+cappedDemoDur)}-t-1\:d}':x=...:y=...[post_v];

  [pre_v][demo_v][post_v]concat=n=3:v=1:a=0[outv];

-map [outv] -map 2:a
-t {T}
-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 64k
output.mp4
```

Where:
- `cappedDemoDur = min(videoDuration(demoPath), T - 3 - 1)` — leave ≥1s post-tile
- `postDur = T - 3 - cappedDemoDur`
- If `postDur <= 0`, drop the `[post_v]` segment, use 2-way concat
- Demo audio is never mapped → silence from demo, beep audio continues

### 2e. Modify `ConvertFramesTo` signature

```go
func (c *Converter) ConvertFramesTo(
    frameInputs []FrameInput,        // was []string inputPaths
    outputPath string,
    timerSeconds, totalMinutes, warmupCount int,
    warmupChapters, repeatingChapters []Chapter,
    onFrameEncoded func(),
) error
```

**Framerate selection:**
```go
hasVideo := false
for _, fi := range frameInputs[warmupCount:] {
    if fi.VideoPath != "" { hasVideo = true; break }
}
fps := 1
if hasVideo { fps = 30 }
```

**Cycle encoding loop (replacing current):**

For each `frameInputs[i]`:
- If `i < warmupCount` OR `fi.VideoPath == ""`: `encodeCycle(fi.PNGPath, cycleFile, timerSeconds, fps)`
- Else (repeating frame with video): `encodeCycleWithVideo(...)` → `firstPassCycleFile` AND `encodeCycle(...)` → `plainCycleFile`

Two parallel slices for repeating frames:
```
firstPassCycles  []string  // video-enhanced where video present, plain otherwise
repeatCycles     []string  // always plain
```

Cleanup: defer remove all unique cycle files (deduplicate paths before removing).

### 2f. Modify `loopFrames` — accept two cycle lists for repeating

```go
func (c *Converter) loopFrames(
    warmupFiles []string,
    firstPassRepeating []string,  // used for pass 0 only
    repeatRepeating []string,     // used for passes 1+
    outputPath string,
    timerSeconds, passes int,
    warmupChapters, repeatingChapters []Chapter,
) error
```

Concat file construction:
```
warmupFiles (once)
firstPassRepeating (pass 0)
repeatRepeating × (passes-1)
```

---

## Step 3 — `cmd/main.go`: Build `[]FrameInput`

Replace `r.PNGPaths` with `frameInputs`:

```go
highlights := generator.FrameHighlights(r.Workout)
frameInputs := make([]converter.FrameInput, len(r.PNGPaths))
for i, png := range r.PNGPaths {
    fi := converter.FrameInput{PNGPath: png}
    if i < len(highlights) {
        h := highlights[i]
        if min, err := strconv.Atoi(h); err == nil {
            for _, round := range r.Workout.Rounds {
                if round.Minute == min && round.Video != "" {
                    fi.VideoPath = round.Video
                    break
                }
            }
        }
    }
    frameInputs[i] = fi
}
```

Pass `frameInputs` instead of `r.PNGPaths` to `conv.ConvertFramesTo`.
Add `"strconv"` import.

---

## Step 4 — Test YAML entries

Add `video:` to a few rounds in `poc/workouts-to-plex/workouts.yaml` to validate end-to-end:

```yaml
rounds:
  - minute: 2
    exercises:
      - movement: "Pull Ups"
        reps: 8
    video: "/mnt/nfs-share/media/youtube/Exercise Library - Pull/s2024e12 - Pull Up - OPEX Exercise Library.mp4"
```

---

## Behavioral Guarantees

| Scenario | Behavior |
|---|---|
| No `video` fields in YAML | Identical to today (1fps, no change) |
| Workout has ≥1 video round | All cycles encoded at 30fps for stream compat |
| First pass | warmup plain + round cycles video-enhanced where video present |
| Passes 2+ | warmup omitted, all repeating cycles plain |
| Warmup tile | Always plain, never injected |
| Strength tile | Always plain, never injected |
| Demo video > 57s | Capped to `timerSeconds - 3 - 1` seconds |
| Timer | Continuous 59→0 across all three segments in enhanced cycle |

---

## Verification

1. Add `video:` to one round in `workouts.yaml` (e.g., "Pull Ups" in ep9)
2. Delete the existing `.sha256` and `.mp4` for that workout to force regeneration
3. Run the Nomad batch job (or `docker compose up`)
4. Open the output MP4 in VLC: scrub to the Pull Ups cycle in the first pass
   - Should see: 3s highlighted tile → demo video plays (scaled, no audio) → tile returns → next tile
   - Timer counts continuously (shows 59, 58, 57 during pre-tile, continues during demo)
5. Scrub to the second loop of the same workout — plain tile only, no demo video
6. Verify a workout with no `video` fields produces identical output to before
