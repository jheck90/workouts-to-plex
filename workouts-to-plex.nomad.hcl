job "workouts-to-plex" {
  datacenters = ["dc1"]
  type        = "service"
  namespace   = "plex"
  priority    = 50

  group "workouts-to-plex" {
    count = 1

    task "workouts-to-plex" {
      driver = "docker"

      template {
        data        = file("workouts.yaml")
        destination = "local/workouts.yaml"
      }

      config {
        image = "jheck90/workouts-to-plex:latest"

        volumes = [
          "local/workouts.yaml:/config/workouts.yaml",
          "/mnt/nfs-share/nomad/workouts-to-plex/images:/input",
          "/mnt/nfs-share/media/workouts:/output",
        ]
      }

      env {
        TZ            = "America/Denver"
        CONFIG_PATH   = "/config/workouts.yaml"
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
