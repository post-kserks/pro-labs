# Contributing to VaultDB

Thank you for your interest in contributing to VaultDB!

---

## Development Setup

### Prerequisites

- Go 1.23+
- C++17 compiler (g++ or clang++)
- CMake 3.20+
- OpenSSL
- Node.js 18+ (for Web UI)

### Clone and Build

```bash
git clone https://github.com/post-kserks/vaultdb.git
cd vaultdb

# Build everything
./build.sh

# Or build server only
make build
```

### Run Tests

```bash
# Go tests
cd server && go test ./...

# Go tests with race detector
cd server && go test -race ./...

# C++ tests
cd client && cmake -S . -B build && cmake --build build && cd build && ctest
```

### Lint

```bash
# Go lint
cd server && golangci-lint run

# Format check
cd server && gofmt -l .
```

---

## Project Structure

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed architecture documentation.

```
vaultdb/
├── server/           # Go backend
│   ├── cmd/          # Entry points
│   └── internal/     # Internal packages
├── client/           # C++ clients
│   ├── lib/          # Shared library
│   ├── shell/        # CLI client
│   └── tui/          # Terminal UI
├── docs/             # Documentation
└── tools/            # Development tools
```

---

## Code Style

### Go

- Follow standard Go conventions
- Use `gofmt` for formatting
- Handle errors explicitly
- Write tests for new functionality

### C++

- C++17 standard
- RAII for resource management
- Use GoogleTest for testing
- Follow existing naming conventions

---

## Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Make your changes
4. Add tests for new functionality
5. Run the full test suite
6. Submit a pull request

### PR Requirements

- [ ] All tests pass
- [ ] No lint warnings
- [ ] New code has tests
- [ ] Documentation updated (if applicable)
- [ ] Commit messages are descriptive

---

## Reporting Issues

- Use GitHub Issues for bug reports
- Include reproduction steps
- Include Go version, OS, and architecture
- For security issues, email security@vaultdb.io

---

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
