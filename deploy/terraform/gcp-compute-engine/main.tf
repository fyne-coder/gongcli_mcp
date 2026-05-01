terraform {
  # Starter example only. Not production-ready as-is; add customer gateway/SSO,
  # WAF or equivalent controls, access logs, rate limits, token rotation,
  # least-privilege egress, release approvals, and audited operations before use.
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
  zone    = var.zone
}

resource "google_compute_instance" "gongmcp" {
  name         = "${var.name}-gongmcp"
  machine_type = var.machine_type
  zone         = var.zone
  tags         = ["${var.name}-gongmcp"]

  boot_disk {
    initialize_params {
      image = "cos-cloud/cos-stable"
      size  = 20
    }
  }

  attached_disk {
    source      = var.gong_data_disk_self_link
    device_name = "gong-data"
    mode        = "READ_ONLY"
  }

  network_interface {
    network    = var.network
    subnetwork = var.subnetwork

    dynamic "access_config" {
      for_each = var.assign_public_ip ? [1] : []
      content {}
    }
  }

  metadata = {
    user-data = templatefile("${path.module}/startup.yaml.tftpl", {
      image                  = var.gongmcp_image
      tool_allowlist         = var.tool_allowlist
      allowed_origins        = var.allowed_origins
      db_path                = "/data/gong.db"
      bearer_token_file_path = var.bearer_token_file_path
    })
  }

  service_account {
    email  = var.service_account_email
    scopes = var.service_account_scopes
  }
}

output "instance_name" {
  value = google_compute_instance.gongmcp.name
}

output "mcp_path" {
  value       = "/mcp"
  description = "Put a customer HTTPS load balancer or reverse proxy in front of the VM before user testing."
}

output "health_path" {
  value       = "/healthz"
  description = "Use this path for infrastructure health checks instead of probing MCP JSON-RPC."
}
