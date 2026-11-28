# Terraform example (UNTESTED)

This is an illustrative Vultr provider configuration for a staging instance in
Frankfurt. It has **not** been run against a Vultr account, and the real staging
server already exists (label `pamoja-staging`, IP `45.77.65.157`). Treat it as
documentation of the intended shape, not a deployment path.

- No API key is stored here. Export `VULTR_API_KEY` in the shell that runs
  Terraform; the provider reads it from the environment.
- State files are not committed. Use a local state only for a throwaway
  experiment, or a protected remote backend.
- The firewall mirrors the staging rule: SSH from any address. Production must
  restrict 22 to named admin addresses or a VPN.
- Object Storage (the backup bucket) is created in the Vultr console, not here:
  the access key and secret are secrets and must never enter Terraform state.

```sh
export VULTR_API_KEY=...        # never commit or paste into chat
terraform init
terraform plan
# terraform apply              # not run; staging already exists
```
