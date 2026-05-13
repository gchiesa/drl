# Security Policy

## Supported Versions

Only the latest release of DRL receives security fixes. We do not backport patches to older versions.

| Version | Supported |
|---------|-----------|
| latest  | Yes       |
| older   | No        |

## Reporting a Vulnerability

If you believe you have found a security issue in DRL, please **do not open a public GitHub issue**.
Instead, report it privately so we can assess and address it before public disclosure.

**Contact:** hello@gchiesa.dev

Please include in your report:

- A description of the issue and its potential impact.
- The DRL version(s) affected.
- Steps to reproduce or a proof-of-concept (if safe to share).
- Any suggestions for a fix, if you have them.

## Response Process

1. **Acknowledgement** — We will acknowledge receipt within 3 business days.
2. **Assessment** — We will assess severity and scope and keep you informed of our progress.
3. **Fix & Release** — We will prepare a fix and coordinate a release date with you.
4. **Disclosure** — After the fix is released, we will publish a security advisory on GitHub.

We follow a **90-day coordinated disclosure** window. If we cannot ship a fix within that time, we will
communicate the situation and agree on an extended timeline with the reporter.

## Scope

In scope:

- The DRL binary and all packages under `internal/` and `pkg/`.
- The gRPC interface (`ShouldRateLimit`), internal HTTP API, and the Control Plane UI.
- The Memberlist/Serf gossip layer and UDP counter path.
- The Docker images published under `ghcr.io/gchiesa/drl`.

Out of scope:

- Third-party dependencies — please report those to their upstream maintainers.
- Issues in the `deployments/` examples that require an already-compromised environment to trigger.
- Denial-of-service issues caused by deliberately malformed traffic at unrealistic volumes (DRL is
  designed for high throughput; stress testing under normal operating conditions is the expected behavior).

## Security Considerations

DRL is designed as an internal sidecar service. It is not intended to be exposed directly to the
public internet. Operators should ensure:

- The internal HTTP API (default port 8082) and gRPC peer port are not reachable from untrusted networks.
- The Control Plane UI is accessed over a trusted internal network or behind an authenticated proxy.
- Gossip encryption is enabled for multi-tenant or multienvironment deployments (see the configuration docs).
