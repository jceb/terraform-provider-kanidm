# Example: Service account for automation
resource "kanidm_service_account" "terraform" {
  name        = "terraform-automation"
  displayname = "Terraform Automation Account"
}

# Store the API token securely in 1Password
output "terraform_api_token" {
  description = "API token for Terraform service account"
  value       = kanidm_service_account.terraform.api_token
  sensitive   = true
}

# Example: Service account for CI/CD
resource "kanidm_service_account" "argocd" {
  name        = "argocd"
  displayname = "ArgoCD Service Account"
}

# Example: Service account for monitoring
resource "kanidm_service_account" "prometheus" {
  name        = "prometheus"
  displayname = "Prometheus Monitoring"
}

# Example: Imported existing service account
# Import command: terraform import kanidm_service_account.existing existing_account_id
resource "kanidm_service_account" "existing" {
  name        = "existing-service"
  displayname = "Existing Service Account"
}
