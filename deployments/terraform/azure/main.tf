terraform {
  required_version = ">= 1.5.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.25"
    }
  }

  backend "azurerm" {
    resource_group_name  = "forge-terraform-state-rg"
    storage_account_name = "forgeterraformstate"
    container_name       = "tfstate"
    key                  = "forge/terraform.tfstate"
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

variable "azure_location" {
  description = "Azure region"
  type        = string
  default     = "eastus"
}

provider "azurerm" {
  features {}
}

resource "azurerm_resource_group" "forge" {
  name     = "forge-${var.company_id}-${var.environment}"
  location = var.azure_location

  tags = {
    environment = var.environment
    company     = var.company_id
  }
}

resource "azurerm_virtual_network" "forge" {
  name                = "forge-vnet"
  location            = azurerm_resource_group.forge.location
  resource_group_name = azurerm_resource_group.forge.name
  address_space       = ["10.0.0.0/8"]
}

resource "azurerm_subnet" "aks" {
  name                 = "forge-aks-subnet"
  resource_group_name  = azurerm_resource_group.forge.name
  virtual_network_name = azurerm_virtual_network.forge.name
  address_prefixes     = ["10.1.0.0/16"]
}

resource "azurerm_subnet" "db" {
  name                 = "forge-db-subnet"
  resource_group_name  = azurerm_resource_group.forge.name
  virtual_network_name = azurerm_virtual_network.forge.name
  address_prefixes     = ["10.2.0.0/24"]

  delegation {
    name = "fs"
    service_delegation {
      name = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = [
        "Microsoft.Network/virtualNetworks/subnets/join/action",
      ]
    }
  }
}
