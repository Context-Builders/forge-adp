resource "google_container_cluster" "forge" {
  name     = "forge-${var.company_id}-${var.environment}"
  location = var.gcp_region

  # Use a separately managed node pool
  remove_default_node_pool = true
  initial_node_count       = 1

  network    = google_compute_network.forge.id
  subnetwork = google_compute_subnetwork.forge.id

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  workload_identity_config {
    workload_pool = "${var.gcp_project}.svc.id.goog"
  }

  release_channel {
    channel = "REGULAR"
  }

  resource_labels = {
    environment = var.environment
    company     = var.company_id
  }
}

# Control plane node pool
resource "google_container_node_pool" "control_plane" {
  name       = "forge-control-plane"
  cluster    = google_container_cluster.forge.id
  node_count = 2

  autoscaling {
    min_node_count = 2
    max_node_count = 4
  }

  node_config {
    machine_type = "e2-standard-2"

    labels = {
      "forge.io/node-type" = "control-plane"
    }

    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform"
    ]
  }
}

# Agent runtime node pool
resource "google_container_node_pool" "agents" {
  name    = "forge-agents"
  cluster = google_container_cluster.forge.id

  autoscaling {
    min_node_count = 1
    max_node_count = 10
  }

  node_config {
    machine_type = "e2-standard-4"

    labels = {
      "forge.io/node-type" = "agent-runtime"
    }

    taint {
      key    = "forge.io/agent-only"
      value  = "true"
      effect = "NO_SCHEDULE"
    }

    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform"
    ]
  }
}
