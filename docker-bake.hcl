variable "IMAGE" {
  default = "ghcr.io/stupside/castor"
}

# Filled in by docker/metadata-action in CI; empty for local builds.
target "docker-metadata-action" {}

target "default" {
  inherits   = ["docker-metadata-action"]
  context    = "."
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64", "linux/arm64"]

  attest = [
    "type=sbom",
    "type=provenance,mode=max",
  ]

  cache-from = ["type=gha"]
  cache-to   = ["type=gha,mode=max"]
}
