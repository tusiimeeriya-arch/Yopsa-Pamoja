variable "region" {
  description = "Vultr region. Frankfurt is the decided region for staging, production and the backup bucket."
  type        = string
  default     = "fra"
}

variable "plan" {
  description = "2 vCPU / 4 GB RAM / 80 GB SSD."
  type        = string
  default     = "vc2-2c-4gb"
}

variable "label" {
  description = "Instance label."
  type        = string
  default     = "pamoja-staging"
}
