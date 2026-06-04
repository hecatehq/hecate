# Security Policy

Hecate is public-alpha, local-first software. This file is the GitHub-facing
security policy: what versions receive fixes, how to report vulnerabilities, and
how dependency/code-scanning alerts are triaged.

For the detailed operator security model, read [docs/operator/security.md](docs/operator/security.md).
That document covers Hecate's runtime boundaries, workspace modes, approvals,
secrets/local state, native app sidecars, and advisory-handling notes.

## Supported versions

Security fixes target:

- the current `master` branch
- the latest published alpha release

Older alpha releases are not maintained. If a security issue affects an older
release, upgrade to the latest release after the fix ships.

## Reporting a vulnerability

Prefer GitHub private vulnerability reporting if it is available for this
repository. If private reporting is unavailable, open a minimal public issue
asking for a private security contact and do not include exploit details,
secret values, or proof-of-concept payloads in the public issue.

Useful reports include:

- affected version, commit, or release asset
- operating system and install path (desktop app, tarball, Docker, source)
- whether Hecate was bound to loopback only or exposed beyond the local machine
- reproduction steps with the smallest safe example
- expected impact and any known mitigations

## Response expectations

For actionable reports, maintainers aim to:

- acknowledge the report and ask clarifying questions when needed
- confirm whether the issue is in Hecate, an upstream dependency, or deployment
  configuration
- fix Hecate-owned issues on `master`
- document upstream-blocked dependency advisories until a safe compatible update
  exists
- publish a release when the fix affects shipped binaries, Docker images, or the
  native desktop app

## Dependency and code-scanning alerts

Dependabot and CodeQL alerts are triaged as security work. Fixable alerts should
be handled by updating dependencies or hardening the relevant code path.

Some transitive dependency alerts can be upstream-blocked. When that happens,
the expected handling is to document the blocker in the pull request or release
notes and revisit the alert when upstream publishes a compatible fix.
