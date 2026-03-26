# workouts-to-plex

Converts workout plan images into Plex-compatible videos with a countdown timer overlay.

Drop a static image (or define workouts in `workouts.yaml`) and the app generates a looping MP4 with a configurable countdown timer in the corner — perfect for EMOM, AMRAP, or any timed workout format.

## How it works

```
workouts.yaml → HTML template → Chromium screenshot → PNG → FFmpeg → MP4 (Plex)
```

1. On startup, reads `workouts.yaml` and renders each workout as a styled HTML card
2. Chromium (headless) screenshots each card at the configured resolution (default 1920×1080)
3. FFmpeg wraps the PNG into an MP4 with a countdown timer overlaid in the bottom-right corner
4. Output MP4s land in the output directory, ready for Plex to index
5. The app then watches the input directory for any manually dropped images and converts those too

## Volumes

| Path | Purpose |
|------|---------|
| `/workouts.yaml` | Workout definitions (see below) |
| `/input` | Intermediate PNGs — auto-generated, or drop your own images here |
| `/output` | Plex media library — point your Plex library at this directory |

## Quick start

```bash
git clone https://github.com/jheck90/workouts-to-plex
cd workouts-to-plex
mkdir images plex
docker compose up --build
```

Your Plex library should point at `./plex/`.

## Configuration

Edit `workouts.yaml` to define your workouts:

```yaml
settings:
  width: 1920   # output resolution width
  height: 1080  # output resolution height

workouts:
  - name: "20 Min EMOM"
    subtitle: "Every Minute On the Minute"
    timer_seconds: 60
    theme: dark
    rounds:
      - minute: 1
        exercises:
          - movement: "Burpees"
            reps: 10
      - minute: 2
        exercises:
          - movement: "Air Squats"
            reps: 15
    notes: "Rest for remaining time each minute. Repeat for 20 minutes."
```

Re-run the container after editing `workouts.yaml` to regenerate the videos. Existing outputs are skipped unless you delete the corresponding MP4.

## Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CONFIG_PATH` | `/workouts.yaml` | Path to workout definitions file |
| `INPUT_DIR` | `/input` | Directory to watch for new images |
| `OUTPUT_DIR` | `/output` | Directory to write MP4s into |
| `TIMER_SECONDS` | `60` | Default countdown duration (overridden per-workout in yaml) |

## Nomad

A Nomad job spec is included at `workouts-to-plex.nomad.hcl`. Update the volume paths to match your NFS mount layout, then:

```bash
nomad job run workouts-to-plex.nomad.hcl
```

The job expects the Docker image to be published to `ghcr.io/jheck90/workouts-to-plex:latest`. See the [GitHub Actions](#cicd) section below for publishing.

## CI/CD

To publish the image to GHCR automatically on push, add a `.github/workflows/docker.yml`:

```yaml
name: Build and push Docker image

on:
  push:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/build-push-action@v5
        with:
          push: true
          tags: ghcr.io/jheck90/workouts-to-plex:latest
```
