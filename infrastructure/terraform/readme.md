# GCP infrastructure (OpenTofu)

Artifact Registry in `europe-west8`, Cloud Run in `europe-west1`, built and deployed by Cloud Build (`/cloudbuild.yaml`) on every push to `main`.

## First setup

1. Install the Cloud Build GitHub app on the repository (console → Cloud Build → Triggers → Connect repository). One-off, not scriptable.
2. Apply:

   ```bash
   cp terraform.tfvars.example terraform.tfvars   # set project_id
   tofu init
   tofu apply
   ```

   The service starts on a placeholder image; the first push to `main` replaces it.
3. The service is public (`allow_unauthenticated = true`, the default). The server has no authentication of its own; set it to `false` to require `roles/run.invoker`.

Tofu ignores the image and args of the service, so applying never rolls back a deploy.
