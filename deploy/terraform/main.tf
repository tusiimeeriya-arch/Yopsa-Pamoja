# Example Vultr staging instance. UNTESTED — this file is documentation, not a
# deployment path. The real staging server already exists and is managed by the
# steps in docs/DEPLOYMENT.md. Do not run this against production.
#
# The API key is never written here: export VULTR_API_KEY in the shell that
# runs Terraform. State contains resource ids, not secrets, but keep it out of
# git anyway.

terraform {
  required_version = ">= 1.6"
  required_providers {
    vultr = {
      source  = "vultr/vultr"
      version = "~> 2.26"
    }
  }
}

provider "vultr" {
  # api_key defaults to the VULTR_API_KEY environment variable.
  rate_limit = 100
  retry_limit = 3
}

data "vultr_os" "ubuntu" {
  filter {
    name   = "name"
    values = ["Ubuntu 24.04 LTS x64"]
  }
}

data "vultr_plan" "staging" {
  filter {
    name   = "id"
    values = [var.plan]
  }
}

resource "vultr_firewall_group" "staging" {
  description = "pamoja-staging"
}

resource "vultr_firewall_rule" "ssh" {
  firewall_group_id = vultr_firewall_group.staging.id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = "0.0.0.0"
  subnet_size       = 0
  port              = "22"
  notes             = "SSH: staging allows any address by choice; production must restrict this"
}

resource "vultr_firewall_rule" "http" {
  firewall_group_id = vultr_firewall_group.staging.id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = "0.0.0.0"
  subnet_size       = 0
  port              = "80"
  notes             = "ACME HTTP-01 and HTTP-to-HTTPS redirect"
}

resource "vultr_firewall_rule" "https" {
  firewall_group_id = vultr_firewall_group.staging.id
  protocol          = "tcp"
  ip_type           = "v4"
  subnet            = "0.0.0.0"
  subnet_size       = 0
  port              = "443"
  notes             = "HTTPS"
}

resource "vultr_instance" "staging" {
  label             = var.label
  region            = var.region
  plan              = data.vultr_plan.staging.id
  os_id             = data.vultr_os.ubuntu.id
  hostname          = var.label
  firewall_group_id = vultr_firewall_group.staging.id
  backups           = "enabled"
  enable_ipv6       = true
  ddos_protection   = false

  # No SSH key is embedded here: add the operator's public key in the Vultr
  # console or with vultr_ssh_key, never a private key.
}
