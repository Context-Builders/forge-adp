terraform {
  required_version = ">= 1.5.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.25"
    }
  }

  backend "gcs" {
    bucket = "forge-terraform-state"
    prefix = "forge/terraform.tfstate"
  }
}

variable "environment" {
  description = "Deployment environment"
  type        = string
  default     = "dev"
}

variable "company_id" {
  description = "Company/tenant identifier"
  type        = string
}

variable "gcp_project" {
  description = "GCP project ID"
  type        = string
}

variable "gcp_region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

provider "google" {
  project = var.gcp_project
  region  = var.gcp_region
}

# VPC network
resource "google_compute_network" "forge" {
  name                    = "forge-${var.company_id}-${var.environment}"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "forge" {
  name          = "forge-${var.company_id}-${var.environment}"
  ip_cidr_range = "10.0.0.0/16"
  region        = var.gcp_region
  network       = google_compute_network.forge.id

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.1.0.0/16"
  }

  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.2.0.0/16"
  }
}
