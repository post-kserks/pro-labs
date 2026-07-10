#!/bin/bash
set -euo pipefail

echo "Generating SBOM..."
go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest
cyclonedx-gomod mod -json -output sbom.json ./server
echo "SBOM generated: sbom.json"
