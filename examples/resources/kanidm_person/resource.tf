# Example: Person account with password-based authentication
resource "kanidm_person" "alice_password" {
  name        = "alice"
  displayname = "Alice Smith"
  mail        = ["alice@example.com"]
  password    = var.alice_password

  lifecycle {
    # Ignore password changes after creation (managed externally)
    ignore_changes = [password]
  }
}

# Example: Person account with passkey/modern authentication (recommended)
resource "kanidm_person" "bob_passkey" {
  name                                   = "bob"
  displayname                            = "Bob Johnson"
  mail                                   = ["bob@example.com"]
  generate_initial_credential_reset_token = true
  initial_credential_reset_token_ttl      = 7200 # 2 hours
}

# Output the credential reset token for Bob
output "bob_credential_reset_token" {
  description = "One-time token for Bob to set up credentials via Kanidm web UI"
  value       = kanidm_person.bob_passkey.initial_credential_reset_token
  sensitive   = true
}

# Example: Person account without initial credentials
resource "kanidm_person" "charlie" {
  name        = "charlie"
  displayname = "Charlie Brown"
  mail        = ["charlie@example.com", "cbrown@example.com"]
}

# Example: Imported existing person account
# Import command: terraform import kanidm_person.existing_user username
resource "kanidm_person" "existing_user" {
  name        = "existing"
  displayname = "Existing User"
}
