resource "azurerm_kubernetes_cluster" "forge" {
  name                = "forge-${var.company_id}-${var.environment}"
  location            = azurerm_resource_group.forge.location
  resource_group_name = azurerm_resource_group.forge.name
  dns_prefix          = "forge-${var.company_id}"
  kubernetes_version  = "1.29"

  default_node_pool {
    name            = "controlplane"
    node_count      = 2
    vm_size         = "Standard_D2s_v3"
    vnet_subnet_id  = azurerm_subnet.aks.id
    os_disk_size_gb = 50

    enable_auto_scaling = true
    min_count           = 2
    max_count           = 4

    node_labels = {
      "forge.io/node-type" = "control-plane"
    }
  }

  identity {
    type = "SystemAssigned"
  }

  network_profile {
    network_plugin = "azure"
    network_policy = "azure"
  }

  oms_agent {
    log_analytics_workspace_id = azurerm_log_analytics_workspace.forge.id
  }

  tags = {
    environment = var.environment
    company     = var.company_id
  }
}

# Agent runtime node pool
resource "azurerm_kubernetes_cluster_node_pool" "agents" {
  name                  = "agentruntime"
  kubernetes_cluster_id = azurerm_kubernetes_cluster.forge.id
  vm_size               = "Standard_D4s_v3"
  vnet_subnet_id        = azurerm_subnet.aks.id

  enable_auto_scaling = true
  min_count           = 1
  max_count           = 10

  node_labels = {
    "forge.io/node-type" = "agent-runtime"
  }

  node_taints = [
    "forge.io/agent-only=true:NoSchedule"
  ]

  tags = {
    environment = var.environment
    company     = var.company_id
  }
}

resource "azurerm_log_analytics_workspace" "forge" {
  name                = "forge-logs-${var.company_id}-${var.environment}"
  location            = azurerm_resource_group.forge.location
  resource_group_name = azurerm_resource_group.forge.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}
