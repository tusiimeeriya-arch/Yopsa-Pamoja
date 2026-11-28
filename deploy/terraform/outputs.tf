output "instance_id" {
  value = vultr_instance.staging.id
}

output "main_ip" {
  value = vultr_instance.staging.main_ip
}

output "firewall_group_id" {
  value = vultr_firewall_group.staging.id
}
