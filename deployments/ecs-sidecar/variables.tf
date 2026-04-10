variable "aws_region" {
  description = "AWS region to deploy into"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name (e.g. dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "cluster_name" {
  description = "ECS cluster name"
  type        = string
  default     = "drl-cluster"
}

variable "service_name" {
  description = "ECS service name for the workload"
  type        = string
  default     = "echo-server"
}

variable "service_replicas" {
  description = "Number of ECS task replicas"
  type        = number
  default     = 3
}

variable "drl_image" {
  description = "Docker image for DRL (e.g. 123456789.dkr.ecr.us-east-1.amazonaws.com/drl:latest)"
  type        = string
}

variable "echo_server_image" {
  description = "Docker image for the echo server backend"
  type        = string
  default     = "mccutchen/go-httpbin:latest"
}

variable "envoy_image" {
  description = "Docker image for the Envoy proxy sidecar"
  type        = string
  default     = "envoyproxy/envoy:v1.30-latest"
}

variable "drl_private_api_key" {
  description = "DRL private API key for internal API authentication"
  type        = string
  sensitive   = true
}

variable "drl_membership_primary_key" {
  description = "DRL memberlist encryption primary key (16 bytes hex)"
  type        = string
  sensitive   = true
}

variable "drl_membership_secondary_keys" {
  description = "DRL memberlist encryption secondary keys (comma-separated 16-byte hex values)"
  type        = string
  sensitive   = true
  default     = ""
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "Availability zones to use"
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b"]
}

variable "task_cpu" {
  description = "Total CPU units for the ECS task (shared across all containers)"
  type        = number
  default     = 1024 # 1 vCPU
}

variable "task_memory" {
  description = "Total memory (MiB) for the ECS task"
  type        = number
  default     = 2048 # 2 GB
}
