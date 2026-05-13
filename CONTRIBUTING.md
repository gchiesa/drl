# Contributing to DRL

Thank you for your interest in contributing to DRL — a high-performance, distributed rate-limiting service for Envoy
sidecars. This document explains how to get started, how the repository is structured, and the conventions we follow.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Repository Structure](#repository-structure)
- [How to Contribute](#how-to-contribute)
  - [Standard Contribution](#standard-contribution)
  - [Spec-Driven Development with AI](#spec-driven-development-with-ai)
- [Tracking Issues](#tracking-issues)
- [Testing](#testing)
- [Documentation](#documentation)
- [Commit Message Convention](#commit-message-convention)
- [Pull Request Checklist](#pull-request-checklist)

---

## Prerequisites

DRL uses [mise](https://mise.jdx.dev/) as its task runner and toolchain manager. Install it first:

```sh
curl https://mise.run | sh
```

Then install all project tooling from the repo root:

```sh
mise install
```

This provisions Go, golangci-lint, and any other tools declared in `mise.toml`.

---

## Repository Structure

```
drl/
├── main.go                      # Binary entrypoint
├── internal/                    # Internal packages — not exported
│   ├── accounting/              # Distributed counter engine & UDP flusher
│   ├── api/                     # HTTP management API (Fiber + Swagger)
│   │   ├── docs/                # Auto-generated OpenAPI/Swagger docs
│   │   ├── models/              # API request/response models
│   │   └── resources/           # Embedded static assets (UI)
│   ├── cache/                   # In-memory caches (blocklist, accounting)
│   ├── cmd/                     # CLI flags and command wiring (Cobra/Viper)
│   ├── config/                  # KDL configuration parsing & defaults
│   │   └── resources/           # Embedded default config (default.kdl)
│   ├── grpc/                    # Envoy ratelimit.v3 gRPC server
│   ├── membership/              # Hashicorp Serf/Memberlist cluster layer
│   ├── metrics/                 # Prometheus metrics definitions
│   ├── model/                   # Core domain models (Entity, etc.)
│   ├── proto/                   # Protobuf definitions & generated Go code
│   └── ratelimit/               # Rate limiting algorithms (token bucket, sliding window)
├── docs/                        # Hugo-based documentation site
│   └── content/docs/            # Documentation pages — extend here
├── deployments/                 # Deployment manifests and examples
│   ├── docker-compose/          # Local integration testing stack
│   ├── k8s-fleet/               # Kubernetes fleet deployment
│   ├── k8s-sidecar/             # Kubernetes sidecar deployment
│   ├── ecs-sidecar/             # AWS ECS sidecar deployment
│   └── istio/                   # Istio service mesh integration
├── ci/scripts/                  # MISE task scripts (must have +x bit)
├── .github/                     # GitHub Actions workflows and templates
│   ├── ISSUE_TEMPLATE/          # Issue templates
│   └── workflows/               # CI/CD pipelines
├── mise.toml                    # MISE task runner and toolchain config
└── .golangci-lint.yaml          # Linting rules
```

> **Rule:** never import from `internal/` across package boundaries that are not already established. New stable
> packages belong in `pkg/` once their API is considered stable.

---

## How to Contribute

### Standard Contribution

1. **Open a tracking issue** before writing any code (see [Tracking Issues](#tracking-issues)).
2. Fork the repository and create a feature branch from `main`:
   ```sh
   git checkout -b feat/short-description
   ```
3. Make your changes, following the coding conventions below.
4. Add or update tests in the same package as your changes.
5. Run linting and tests locally (see [Testing](#testing)).
6. Push your branch and open a Pull Request referencing the tracking issue.

### Spec-Driven Development with AI

DRL uses a milestone-driven, spec-first workflow that pairs well with AI coding assistants. If you are using an AI to
help implement a feature or milestone, follow these guidelines:

1. **Write the spec first.** Create a milestone document under `.junie/workflow/milestones/` that describes:
   - The goal and acceptance criteria.
   - The packages and interfaces that will change.
   - A test plan.
2. **Open a tracking issue** describing the spec (see [Tracking Issues](#tracking-issues)).
3. **Implement** using the AI assistant against the spec.
4. **Review the diff yourself.** You are responsible for the code the AI produces. Treat AI output as a first draft that
   requires human review.
5. **Record the AI's assistance** in the commit message using the `Assisted-By` trailer (see
   [Commit Message Convention](#commit-message-convention)).
6. Run the full test suite before opening a PR.

> The `Assisted-By` trailer is modelled after the Linux kernel's `Signed-off-by` convention. It makes AI-assisted
> contributions transparent to reviewers without changing the authorship of the commit.

---

## Tracking Issues

**Every contribution must be linked to a GitHub issue.** This gives reviewers context and keeps the project history
navigable.

- For bugs: use the **Bug Report** template.
- For features or improvements: use the **Feature Request** template.
- For AI-assisted spec implementations: reference the issue in both the milestone document and the PR description.

If no suitable issue exists, open one before opening a PR. Use `closes #<issue>` or `fixes #<issue>` in the PR
description so GitHub closes the issue automatically on merge.

---

## Testing

All tests live next to the code they test (`foo_test.go` alongside `foo.go`).

```sh
# Run the full test suite with race detection and coverage
mise run tests

# Build the binary (includes -race for dev)
mise run build

# Run linter
mise run lint
```

Tests must pass and linter must report no errors before a PR can be merged. CI enforces this automatically.

### Integration / Manual Testing

A full local stack is provided via Docker Compose under `deployments/docker-compose/`. It includes:

- An echo-server workload behind an Envoy reverse proxy.
- A 3-node DRL fleet.
- A k6 traffic producer with configurable ramp-up.

```sh
cd deployments/docker-compose
docker compose up
```

See `deployments/docker-compose/README.md` for configuration variables.

---

## Documentation

User-facing documentation lives under `docs/content/docs/`. It is built as a Hugo site.

**When to extend the docs:**

- Every new configuration option must be documented in `docs/content/docs/configuration.md`.
- Every new API endpoint must appear in `docs/content/docs/api.md` (or the auto-generated Swagger at
  `internal/api/docs/`).
- Every new architectural concept or component gets its own page under `docs/content/docs/`.

Do not add documentation directly to `README.md` for concepts that belong in the structured docs site. Keep
`README.md` focused on quick-start information.

---

## Commit Message Convention

DRL follows the [Conventional Commits](https://www.conventionalcommits.org/) specification. Commit messages must match:

```
<type>(<scope>): <subject>

[optional body]

[optional trailers]
```

**Types:** `feat`, `fix`, `docs`, `refactor`, `test`, `chore`, `perf`, `ci`

**Scopes** map to top-level packages: `membership`, `cache`, `accounting`, `grpc`, `api`, `config`, `ratelimit`, etc.

### The `Assisted-By` Trailer

If an AI assistant contributed meaningfully to writing the code in a commit, add an `Assisted-By` trailer after all
other trailers. The format mirrors the Linux kernel's `Signed-off-by` convention:

```
Assisted-By: <Model Name> (<model-id>) <contact>
```

Examples:

```
feat(ratelimit): implement token-bucket sliding window

Closes #42

Assisted-By: Claude (claude-sonnet-4-6) <noreply@anthropic.com>
```

```
feat(membership): add encryption for gossip traffic

Closes #17

Assisted-By: GPT-4o (gpt-4o-2024-11-20) <noreply@openai.com>
```

Rules:
- One `Assisted-By` line per AI model used.
- The human author remains the `Author` of the commit and is responsible for the content.
- If the AI only suggested a small fix (e.g., a typo), the trailer is optional.

---

## Pull Request Checklist

Before requesting a review, verify:

- [ ] A tracking GitHub issue exists and is referenced in the PR description.
- [ ] All new code has corresponding unit tests.
- [ ] `mise run lint` passes with no errors.
- [ ] `mise run tests` passes with no failures.
- [ ] Documentation in `docs/content/docs/` is updated if behaviour changed.
- [ ] Commit messages follow the Conventional Commits format.
- [ ] `Assisted-By` trailers are present where an AI assistant contributed.
