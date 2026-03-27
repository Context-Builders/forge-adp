resource "azurerm_private_dns_zone" "postgres" {
  name                = "forge-${var.company_id}.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.forge.name
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                  = "forge-postgres-dns-link"
  private_dns_zone_name = azurerm_private_dns_zone.postgres.name
  virtual_network_id    = azurerm_virtual_network.forge.id
  resource_group_name   = azurerm_resource_group.forge.name
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "azurerm_postgresql_flexible_server" "forge" {
  name                   = "forge-${var.company_id}-${var.environment}"
  resource_group_name    = azurerm_resource_group.forge.name
  location               = azurerm_resource_group.forge.location
  version                = "16"
  delegated_subnet_id    = azurerm_subnet.db.id
  private_dns_zone_id    = azurerm_private_dns_zone.postgres.id
  administrator_login    = "forge_admin"
  administrator_password = random_password.db_password.result

  storage_mb = 51200

  sku_name = "GP_Standard_D2s_v3"

  backup_retention_days        = 7
  geo_redundant_backup_enabled = var.environment == "prod"

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]

  tags = {
    environment = var.environment
    company     = var.company_id
  }
}

resource "azurerm_postgresql_flexible_server_database" "forge" {
  name      = "forge"
  server_id = azurerm_postgresql_flexible_server.forge.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

# Store credentials in Azure Key Vault
resource "azurerm_key_vault" "forge" {
  name                = "forge-kv-${var.company_id}"
  location            = azurerm_resource_group.forge.location
  resource_group_name = azurerm_resource_group.forge.name
  sku_name            = "standard"
  tenant_id           = data.azurerm_client_config.current.tenant_id

  purge_protection_enabled = var.environment == "prod"
}

data "azurerm_client_config" "current" {}

resource "azurerm_key_vault_secret" "db_password" {
  name         = "forge-db-password"
  value        = random_password.db_password.result
  key_vault_id = azurerm_key_vault.forge.id
}

resource "azurerm_key_vault_secret" "db_host" {
  name         = "forge-db-host"
  value        = azurerm_postgresql_flexible_server.forge.fqdn
  key_vault_id = azurerm_key_vault.forge.id
}
