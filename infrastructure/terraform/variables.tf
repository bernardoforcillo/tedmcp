variable "project_id" {
  description = "GCP project that hosts everything."
  type        = string
}

variable "registry_region" {
  description = "Region of the Artifact Registry repository (Milan)."
  type        = string
  default     = "europe-west8"
}

variable "run_region" {
  description = "Region of the Cloud Run service (Belgium)."
  type        = string
  default     = "europe-west1"
}

variable "service_name" {
  type    = string
  default = "tenders-mcp"
}

variable "github_owner" {
  type    = string
  default = "bernardoforcillo"
}

variable "github_repo" {
  type    = string
  default = "tedmcp"
}

variable "branch" {
  description = "Branch whose pushes are built and deployed."
  type        = string
  default     = "main"
}

variable "allow_unauthenticated" {
  description = "Let anyone on the internet invoke the service. The server has no authentication of its own, so set this to false to put it behind IAM (grant roles/run.invoker to callers)."
  type        = bool
  default     = true
}

variable "max_instances" {
  type    = number
  default = 2
}

variable "domain" {
  description = "Custom domain mapped to the service."
  type        = string
  default     = "tenders.tools.bernardoforcillo.com"
}

variable "dns_zone" {
  description = "Existing Cloud DNS managed zone that holds var.domain."
  type        = string
  default     = "bernardoforcillo-com"
}
