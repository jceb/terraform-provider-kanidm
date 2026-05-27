# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Terraform provider for Kanidm, an identity management system. Built using the Terraform Plugin Framework (v1.17.0) and Go 1.24.

## Common Commands

### Build and Installation
```bash
make build              # Build the provider binary
make install            # Install provider locally to ~/.terraform.d/plugins/
make clean              # Remove build artifacts
```

### Testing
```bash
make test               # Run unit tests
make testacc            # Run acceptance tests (requires KANIDM_URL and KANIDM_TOKEN)
```

### Code Quality
```bash
make fmt                # Format Go code
make lint               # Run golangci-lint
```

### Documentation
```bash
make docs               # Generate provider documentation using tfplugindocs
```

### Running Single Tests
```bash
# Run specific test function
go test -v -run TestPersonResource_Create ./internal/provider/

# Run acceptance test with timeout
TF_ACC=1 go test -v -timeout 30m -run TestAccPersonResource ./internal/provider/
```

## Architecture

### Two-Layer Structure

The provider follows a clear separation of concerns:

1. **API Client Layer** (`internal/client/`)
   - `client.go` - Core HTTP client with auth, error handling, and common types
   - `person.go`, `service_account.go`, `group.go`, `oauth2.go` - Resource-specific API operations
   - All functions accept `context.Context` as first parameter
   - Returns typed errors: `ErrNotFound`, `ErrUnauthorized`, `ErrForbidden`

2. **Terraform Resource Layer** (`internal/provider/`)
   - `provider.go` - Provider configuration, creates API client from `KANIDM_URL`/`KANIDM_TOKEN`
   - `*_resource.go` files - Implement Terraform CRUD operations using the client layer
   - Each resource has a corresponding model struct with `tfsdk` tags

### Kanidm API Patterns

**Important behaviors to understand:**

- **Attribute Format**: Kanidm stores all attributes as arrays, even single values
  - Use `Entry.GetString()` for single-value attributes (extracts first element)
  - Use `Entry.GetStringSlice()` for multi-value attributes

- **Fully-Qualified Names**: Group members and references are returned as FQN format `{name}@{domain}`
  - Always use FQN format in Terraform configs to avoid drift

- **Shared Namespace**: Person accounts, service accounts, and OAuth2 clients share the same name namespace
  - Use suffixes like `-oidc` or `-oauth2` for OAuth2 clients to avoid collisions

- **Sensitive Data Retrieval**: Some resources require separate API calls to retrieve secrets
  - OAuth2 client secrets: Create returns `null`, must call `GET /v1/oauth2/{name}/_basic_secret`
  - Service account tokens: Only available immediately after creation

### Known Issues and Gotchas

See `BUGFIXES.md` for detailed history of bugs and resolutions. Key points:

1. **OAuth2 Secrets**: Client secrets must be retrieved via separate GET request after creation
2. **Group Members**: Use `SetAttribute` (not `ListAttribute`) to avoid ordering drift
3. **Empty Collections**: Return empty slice `[]string{}`, not `nil`, to avoid null vs empty list drift
4. **Service Account DisplayName**: Not supported by Kanidm API (persons only)
5. **OAuth2 Type Detection**: Check for attribute key presence, not value (value is "hidden")

### Resource Implementation Pattern

Each resource follows this structure:
1. Define model struct with `tfsdk` tags matching schema
2. Implement `Metadata()`, `Schema()`, `Configure()` methods
3. Implement CRUD: `Create()`, `Read()`, `Update()`, `Delete()`
4. Implement `ImportState()` for `terraform import` support
5. Use plan modifiers like `RequiresReplace()` for immutable fields

### Provider Configuration

The provider accepts configuration via:
- HCL attributes: `url`, `token`
- Environment variables: `KANIDM_URL`, `KANIDM_TOKEN` (preferred for sensitive data)

### Credential Reset Tokens

Person resources support generating credential reset tokens:
- Set `generate_initial_credential_reset_token = true`
- Optionally set `initial_credential_reset_token_ttl` (seconds, default: 3600)
- Token available in computed attribute `initial_credential_reset_token` (sensitive)
- Token is one-time use for setting up passkeys/passwords via web UI

## Development Environment

Uses devbox for reproducible development environment. See `devbox.json` for tool versions.

## Conventional Commits

This project uses conventional commit format:
```
feat(resource): add new OAuth2 public client resource
fix(client): handle 404 errors in person resource
docs(readme): update usage examples
chore(deps): update terraform-plugin-framework
```

Scope should be one of: `resource`, `client`, `provider`, `docs`, `deps`, `build`
