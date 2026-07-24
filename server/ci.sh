#!/bin/bash
set -e

echo "Running gofmt..."
files="$(find . -type f -name '*.go')"
unformatted="$(gofmt -l ${files})"
if [ -n "${unformatted}" ]; then
  echo "These files are not gofmt-formatted:"
  echo "${unformatted}"
  exit 1
fi
echo "gofmt passed."

echo "Running go vet..."
go vet ./...

echo "Running staticcheck..."
if ! command -v staticcheck &> /dev/null; then
  echo "Installing staticcheck..."
  go install honnef.co/go/tools/cmd/staticcheck@v0.7.0
fi
# Add ~/go/bin to path if needed
export PATH=$PATH:$(go env GOPATH)/bin
staticcheck -checks="all,-SA6002,-ST1000,-ST1003,-ST1020,-ST1021,-ST1022,-ST1023" ./...

echo "Running gosec..."
if ! command -v gosec &> /dev/null; then
  echo "Installing gosec..."
  go install github.com/securego/gosec/v2/cmd/gosec@latest
fi
gosec -exclude=G101,G104,G110,G115,G122,G204,G301,G302,G304,G306,G401,G404,G505,G703 ./...

echo "Running govulncheck..."
if ! command -v govulncheck &> /dev/null; then
  echo "Installing govulncheck..."
  go install golang.org/x/vuln/cmd/govulncheck@latest
fi
govulncheck ./...

echo "Running go test..."
go test -count=1 ./...

echo "All CI checks passed successfully!"
