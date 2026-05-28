---
page_title: "Getting Started with the kanidm Provider"
description: |-
  Configure the kanidm provider and create your first person, group, and OAuth2 resources.
---

# Getting Started with the kanidm Provider

This guide shows the minimal steps to authenticate to Kanidm and create a few common resources.

## Prerequisites

- A running Kanidm instance
- An API token with permission to manage the resources you want to create
- OpenTofu or Terraform configured to use `seanlatimer/kanidm`

## Provider Configuration

```hcl
terraform {
  required_providers {
    kanidm = {
      source = "seanlatimer/kanidm"
    }
  }
}

provider "kanidm" {
  url   = var.kanidm_url
  token = var.kanidm_token
}
```

For self-signed development environments:

```hcl
provider "kanidm" {
  url                  = var.kanidm_url
  token                = var.kanidm_token
  insecure_skip_verify = true
}
```

## Create a Person and Group

```hcl
resource "kanidm_person" "alice" {
  name        = "alice"
  displayname = "Alice Example"
  mail        = ["alice@example.com"]
}

resource "kanidm_group" "developers" {
  name        = "developers"
  description = "Development team"

  members = [
    kanidm_person.alice.id,
  ]
}
```

Use UUID-backed `id` references where possible. This keeps state stable even if Kanidm entries are renamed out-of-band.

## Create an OAuth2 Client

```hcl
resource "kanidm_oauth2_basic" "grafana" {
  name        = "grafana"
  displayname = "Grafana"
  origin      = "https://grafana.example.com"

  redirect_uris = [
    "https://grafana.example.com/login/generic_oauth",
  ]

  scope_map {
    group  = kanidm_group.developers.id
    scopes = ["openid", "profile", "email"]
  }
}
```

`redirect_uris`, scope sets, and claim values are treated as unordered sets to match Kanidm storage behavior.

## Create an Application Entry

```hcl
resource "kanidm_application" "mail" {
  name         = "mail"
  displayname  = "Mail"
  linked_group = kanidm_group.developers.id
}
```

Use `kanidm_application` when you want to model an application that gates per-user application passwords through a linked group.

## Next Steps

- Use `kanidm_group_members` when you want to manage only part of a group's membership.
- Use `kanidm_account_policy` to attach policy to built-in or managed groups.
- Use `kanidm_oauth2_public` for public/native clients.
