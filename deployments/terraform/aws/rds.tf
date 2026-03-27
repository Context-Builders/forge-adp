module "rds" {
  source  = "terraform-aws-modules/rds/aws"
  version = "~> 6.0"

  identifier = "forge-${var.company_id}-${var.environment}"

  engine               = "postgres"
  engine_version       = "16.1"
  family               = "postgres16"
  major_engine_version = "16"
  instance_class       = "db.t3.medium"

  allocated_storage     = 50
  max_allocated_storage = 200

  db_name  = "forge"
  username = "forge_admin"
  port     = 5432

  vpc_security_group_ids = [aws_security_group.rds.id]
  db_subnet_group_name   = module.vpc.database_subnet_group_name

  backup_retention_period = 7
  deletion_protection     = var.environment == "prod"

  parameters = [
    {
      name  = "log_statement"
      value = "all"
    }
  ]

  tags = {
    Environment = var.environment
    Company     = var.company_id
  }
}