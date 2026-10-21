locals {
  services = [
    "artifactregistry.googleapis.com",
    "cloudbuild.googleapis.com",
    "run.googleapis.com",
    "iam.googleapis.com",
  ]

  registry = "${var.registry_region}-docker.pkg.dev/${var.project_id}/${var.service_name}"

  # Cloud Run needs an image to create a revision, and the image does not exist
  # until the first build. This public sample stands in; Cloud Build replaces
  # it on every push and the lifecycle block below keeps tofu from reverting it.
  placeholder_image = "us-docker.pkg.dev/cloudrun/container/hello"
}

resource "google_project_service" "enabled" {
  for_each           = toset(local.services)
  service            = each.value
  disable_on_destroy = false
}

# --- images --------------------------------------------------------------

resource "google_artifact_registry_repository" "images" {
  location      = var.registry_region
  repository_id = var.service_name
  format        = "DOCKER"
  description   = "tenders-mcp container images"

  # Every push adds a tag named after the commit; keep the recent ones.
  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 10
    }
  }
  cleanup_policies {
    id     = "delete-old"
    action = "DELETE"
    condition {
      older_than = "2592000s" # 30 days
    }
  }

  depends_on = [google_project_service.enabled]
}

# --- identities ----------------------------------------------------------

# What the service runs as. No roles: tedmcp only calls public APIs.
resource "google_service_account" "runtime" {
  account_id   = "${var.service_name}-run"
  display_name = "tenders-mcp Cloud Run runtime"
}

# What Cloud Build runs as.
resource "google_service_account" "build" {
  account_id   = "${var.service_name}-build"
  display_name = "tenders-mcp Cloud Build"
}

resource "google_project_iam_member" "build_logs" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.build.email}"
}

resource "google_artifact_registry_repository_iam_member" "build_push" {
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.build.email}"
}

resource "google_cloud_run_v2_service_iam_member" "build_deploy" {
  location = google_cloud_run_v2_service.tenders_mcp.location
  name     = google_cloud_run_v2_service.tenders_mcp.name
  role     = "roles/run.developer"
  member   = "serviceAccount:${google_service_account.build.email}"
}

resource "google_service_account_iam_member" "build_acts_as_runtime" {
  service_account_id = google_service_account.runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.build.email}"
}

# --- service -------------------------------------------------------------

resource "google_cloud_run_v2_service" "tenders_mcp" {
  name                = var.service_name
  location            = var.run_region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account                  = google_service_account.runtime.email
    max_instance_request_concurrency = 80

    scaling {
      min_instance_count = 0
      max_instance_count = var.max_instances
    }

    containers {
      image = local.placeholder_image

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        cpu_idle = true
      }
    }
  }

  # Revisions are created by Cloud Build, not by tofu.
  lifecycle {
    ignore_changes = [
      template[0].containers[0].image,
      template[0].containers[0].args,
      template[0].labels,
      template[0].revision,
      client,
      client_version,
    ]
  }

  depends_on = [google_project_service.enabled]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  count    = var.allow_unauthenticated ? 1 : 0
  location = google_cloud_run_v2_service.tenders_mcp.location
  name     = google_cloud_run_v2_service.tenders_mcp.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# --- build trigger -------------------------------------------------------

# Needs the Cloud Build GitHub app installed on the repository once, by hand:
# console.cloud.google.com/cloud-build/triggers -> Connect repository.
resource "google_cloudbuild_trigger" "main" {
  name            = "${var.service_name}-${var.branch}"
  description     = "Build the image and deploy it to Cloud Run on every push to ${var.branch}"
  filename        = "cloudbuild.yaml"
  service_account = google_service_account.build.id

  github {
    owner = var.github_owner
    name  = var.github_repo
    push {
      branch = "^${var.branch}$"
    }
  }

  # Docs and manifest edits do not change the binary.
  included_files = ["**.go", "go.mod", "go.sum", "Dockerfile", "cloudbuild.yaml"]

  substitutions = {
    _REGISTRY = local.registry
    _SERVICE  = google_cloud_run_v2_service.tenders_mcp.name
    _REGION   = var.run_region
    _RUNTIME  = google_service_account.runtime.email
  }

  depends_on = [
    google_project_service.enabled,
    google_project_iam_member.build_logs,
  ]
}
