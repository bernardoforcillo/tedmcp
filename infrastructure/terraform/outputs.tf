output "service_url" {
  value = google_cloud_run_v2_service.tenders_mcp.uri
}

output "image_repository" {
  value = "${var.registry_region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "runtime_service_account" {
  value = google_service_account.runtime.email
}
