job "workouts-to-plex" {
  datacenters = ["dc1"]
  type        = "service"
  namespace   = "plex"
  priority    = 50

  group "workouts-to-plex" {
    count = 1

    task "workouts-to-plex" {
      driver = "docker"

      config {
        image = "jheck90/workouts-to-plex:latest"

        volumes = [
          "/mnt/nfs-share/nomad/workouts-to-plex/workouts.yaml:/workouts.yaml",
          "/mnt/nfs-share/nomad/workouts-to-plex/images:/input",
          "/mnt/nfs-share/media/workouts:/output",
        ]
      }

      env {
        TZ            = "America/Denver"
        CONFIG_PATH   = "/workouts.yaml"
        INPUT_DIR     = "/input"
        OUTPUT_DIR    = "/output"
        TIMER_SECONDS = "60"
      }

      resources {
        cpu    = 500
        memory = 512
      }
    }
  }
}
