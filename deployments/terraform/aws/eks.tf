module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 19.0"

  cluster_name    = "forge-${var.company_id}-${var.environment}"
  cluster_version = "1.29"

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  cluster_endpoint_public_access = true

  eks_managed_node_groups = {
    control_plane = {
      name           = "forge-control-plane"
      instance_types = ["t3.large"]
      min_size       = 2
      max_size       = 4
      desired_size   = 2
      
      labels = {
        "forge.io/node-type" = "control-plane"
      }
    }
    
    agent_runtime = {
      name           = "forge-agents"
      instance_types = ["t3.xlarge"]
      min_size       = 1
      max_size       = 10
      desired_size   = 2
      
      labels = {
        "forge.io/node-type" = "agent-runtime"
      }
      
      taints = [{
        key    = "forge.io/agent-only"
        value  = "true"
        effect = "NO_SCHEDULE"
      }]
    }
  }

  tags = {
    Environment = var.environment
    Terraform   = "true"
    Company     = var.company_id
  }
}