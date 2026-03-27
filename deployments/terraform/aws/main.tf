terraform {
  required_version = ">= 1.5.0"
  
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.25"
    }
  }
  
  backend "s3" {
    bucket         = "forge-terraform-state"
    key            = "forge/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "forge-terraform-locks"
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

variable "aws_region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}