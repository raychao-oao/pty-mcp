# Contributing to pty-mcp

Thank you for your interest in contributing!

## Reporting Issues

- Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.md) for bugs
- Use the [feature request template](.github/ISSUE_TEMPLATE/feature_request.md) for new ideas
- Check existing issues before opening a new one

## Pull Requests

1. Fork the repo and create a branch from `main`
2. Make your changes and ensure `go build ./...` and `go test ./...` pass
3. Keep commits focused — one logical change per PR
4. Fill out the PR template

## Development Setup

```bash
git clone https://github.com/raychao-oao/pty-mcp.git
cd pty-mcp
go build .
```

Run tests:
```bash
go test ./... -timeout 15s
```

Build all platforms:
```bash
make VERSION=dev build-all
```

## Code Style

- Standard Go formatting (`gofmt`)
- No external dependencies beyond what's already in `go.mod`

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
