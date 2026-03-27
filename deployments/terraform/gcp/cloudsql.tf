resource "google_sql_database_instance" "forge" {
  name             = "forge-${var.company_id}-${var.environment}"
  database_version = "POSTGRES_16"
  region           = var.gcp_region

  deletion_protection = var.environment == "prod"

  settings {
    tier = "db-custom-2-7680"

    disk_autoresize       = true
    disk_autoresize_limit = 200
    disk_size             = 50

    backup_configuration {
      enabled                        = true
      start_time                     = "02:00"
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = 7
      }
    }

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.forge.id
    }

    insights_config {
      query_insights_enabled = true
    }
  }

  depends_on = [google_service_networking_connection.private_vpc_connection]
}

resource "google_sql_database" "forge" {
  name     = "forge"
  instance = google_sql_database_instance.forge.name
}

resource "google_sql_user" "forge_admin" {
  name     = "forge_admin"
  instance = google_sql_database_instance.forge.name
  password = random_password.db_password.result
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_compute_global_address" "private_ip_range" {
  name          = "forge-private-ip-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.forge.id
}

resource "google_service_networking_connection" "private_vpc_connection" {
  network                 = google_compute_network.forge.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_ip_range.name]
}

resource "google_secret_manager_secret" "db_credentials" {
  secret_id = "forge-db-credentials-${var.company_id}-${var.environment}"

  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "db_credentials" {
  secret = google_secret_manager_secret.db_credentials.id
  secret_data = jsonencode({
    host     = google_sql_database_instance.forge.private_ip_address
    port     = 5432
    database = "forge"
    username = google_sql_user.forge_admin.name
    password = random_password.db_password.result
  })
}
