# Contributing to Trace Forge

Thank you for your interest in contributing to Trace Forge! This document outlines the guidelines for contributing to this open-source project.

## License

Trace Forge is licensed under the MIT License. By contributing, you agree that your contributions will be licensed under the same license.

## Code Style

- **Go**: Use `go fmt` for formatting. Run `make lint` to check for issues.
- **JavaScript/TypeScript**: Use ESLint and Prettier. Run `npm run lint` and `npm run format` in the `web/` directory.

## Branch Naming

Use descriptive branch names with prefixes:
- `feature/*` for new features (e.g., `feature/add-session-replay`)
- `bugfix/*` for bug fixes (e.g., `bugfix/fix-trace-correlation`)

## Running the Local Stack

1. Ensure Docker and Docker Compose are installed.
2. Start Cassandra: `docker compose -f deployments/docker-compose.yml up -d cassandra`
3. Run the backend: `go run ./cmd/traceforge`
4. Run the UI: `cd web && npm install && npm run dev`

## Adding a New Go Module

1. Create a new package in `internal/` (e.g., `internal/newmodule/`).
2. Add the module to `go.mod` if it has external dependencies: `go mod tidy`.
3. Write tests in the same directory (e.g., `newmodule_test.go`).
4. Update any relevant APIs in `api/` or documentation in `docs/`.

## Adding a New React Component

1. Create the component in `web/components/` (e.g., `NewComponent.tsx`).
2. Use TypeScript and follow the existing patterns (hooks in `web/hooks/`, styles with Tailwind).
3. Add the component to the appropriate page or layout.
4. Run `npm run lint` and `npm run format` to ensure code quality.

## Commit Message Convention

Use [Conventional Commits](https://www.conventionalcommits.org/):
- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation
- `style:` for formatting
- `refactor:` for code restructuring
- `test:` for tests
- `chore:` for maintenance

Example: `feat: add session replay visualization`

## Submitting Changes

1. Fork the repository and create a feature branch.
2. Make your changes and ensure tests pass.
3. Run `make lint` and `make test`.
4. Commit with a conventional message.
5. Push and open a pull request with a clear description.

For questions, open an issue or join our discussions!