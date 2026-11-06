# Project Governance

Chorecast is an open-source, self-hosted chore and behavior management system for families.

This document defines how the project is governed, how technical and security decisions are made, and who is responsible for maintaining the project.

## Governance Model

Chorecast follows a **maintainer-led governance model**.

The project is currently maintained by a single individual who serves as both Project Maintainer and Security Maintainer.

Contributions and community participation are encouraged, but final decision-making authority remains with the designated maintainer.

Governance may evolve as the project and contributor community grow.

## Maintainer Registry

| Maintainer | GitHub | Roles | Status |
|---|---|---|---|
| voidrot | [@voidrot](https://github.com/voidrot) | Project Maintainer, Security Maintainer | Active |

**Repository:** https://github.com/voidrot/chorecast

The Project Maintainer and Security Maintainer are distinct roles with separately defined responsibilities, even when held by the same individual.

## 1. Project Maintainer

**Designated maintainer:** [@voidrot](https://github.com/voidrot)

The Project Maintainer is responsible for the overall direction, development, operation of project-owned infrastructure, and sustainability of Chorecast.

### Responsibilities

- Establish and maintain the technical architecture.
- Review, approve, and merge contributions.
- Maintain coding standards and development practices.
- Manage issues, feature requests, and project priorities.
- Maintain automated testing and CI/CD workflows.
- Manage project dependencies and development tooling.
- Coordinate releases and versioning.
- Maintain documentation and contributor policies.
- Review compatibility and migration requirements.
- Manage repository permissions and contributor access.
- Appoint or remove maintainers when appropriate.

### Decision Authority

The Project Maintainer has final authority over architectural decisions, development priorities, code acceptance, releases, repository administration, and changes to governance policies.

Significant technical decisions should be documented in issues, pull requests, or architecture documentation when practical.

## 2. Security Maintainer

**Designated security maintainer:** [@voidrot](https://github.com/voidrot)

The Security Maintainer is the explicitly named individual responsible for the project's security maintenance and coordinated vulnerability disclosure process.

Chorecast processes household information, authentication credentials, child profiles, behavior records, and uploaded media. These considerations make security stewardship an important project responsibility.

### Responsibilities

**Vulnerability management**

- Receive and triage private vulnerability reports.
- Assess impact, severity, exploitability, and affected versions.
- Reproduce and validate reported vulnerabilities.
- Coordinate development and verification of security fixes.
- Track unresolved security issues.
- Coordinate responsible disclosure.

**Security engineering**

- Review authentication and authorization changes.
- Evaluate session management and credential handling.
- Review device pairing, shared-device isolation, and privilege elevation.
- Review sensitive-data storage, media access, backup, and restore functionality.
- Evaluate dependency and supply-chain risks.
- Assess container deployment and configuration security.

**Security releases and advisories**

- Prioritize remediation based on severity and impact.
- Coordinate security fixes and emergency releases when warranted.
- Maintain security-related release documentation.
- Publish or coordinate GitHub Security Advisories where appropriate.
- Communicate affected versions, mitigations, and upgrade guidance.

**Security governance**

- Maintain [SECURITY.md](SECURITY.md).
- Review security-relevant sections of [PRIVACY.md](PRIVACY.md).
- Maintain a documented private vulnerability reporting process.
- Review security-sensitive architectural decisions.
- Promote secure development and responsible disclosure practices.

### Security Authority

The Security Maintainer may:

- Request additional review or testing of security-sensitive changes.
- Block approval of changes presenting unacceptable security risks.
- Restrict public discussion of unresolved vulnerabilities.
- Coordinate confidential remediation activities.
- Recommend emergency releases or temporary mitigations.
- Coordinate the publication of security advisories.

When security concerns conflict with release schedules or feature priorities, the Security Maintainer is responsible for evaluating and documenting the associated risks.

### Accountability

The Security Maintainer is accountable for coordinating a reasonable response to security reports, subject to available maintainer capacity.

This designation does not imply a guaranteed response time, a paid security service, or a certification of software security.

## 3. Code Ownership

Code ownership is defined in [.github/CODEOWNERS](.github/CODEOWNERS).

The designated code owner is responsible for reviewing changes within assigned areas of the repository.

Ownership currently covers:

- Go server and API.
- Vue web administration interface.
- Android application.
- Database and migrations.
- Deployment configuration.
- CI/CD and release workflows.
- Security policies and governance documentation.

Code ownership does not replace the specific responsibilities of the Security Maintainer.

## 4. Contributions and Review

Contributions are accepted through GitHub pull requests.

All contributors should follow [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

Maintainers evaluate contributions based on:

- Alignment with project goals and architecture.
- Correctness and maintainability.
- Security and privacy implications.
- Test coverage and validation.
- Compatibility with supported clients and deployments.
- Documentation quality.
- Long-term maintenance cost.

Contributions are not guaranteed acceptance.

The Project Maintainer may request revisions, defer decisions, or decline changes that do not fit the project's direction.

## 5. Decision-Making Process

Chorecast uses a lightweight decision-making process appropriate for a small open-source project.

### Routine Changes

Bug fixes, documentation updates, refactoring, and narrowly scoped features are generally reviewed through pull requests.

The Project Maintainer makes the final merge decision.

### Significant Changes

Major architectural changes, breaking API changes, database compatibility changes, and new integrations should be discussed before substantial implementation work begins.

Where practical, these decisions should be captured in an issue, design proposal, or architecture document.

### Security-Sensitive Changes

Changes affecting authentication, authorization, cryptographic operations, secret management, or sensitive household data require security consideration.

The Security Maintainer determines whether additional testing or review is necessary.

### Disagreements

Contributors should discuss disagreements respectfully and support proposals with technical reasoning.

When consensus cannot be reached, the Project Maintainer makes the final decision.

Security-related disagreements must also consider the assessment of the Security Maintainer.

## 6. Security Disclosure and Incident Response

Potential vulnerabilities must be reported privately according to [SECURITY.md](SECURITY.md).

The Security Maintainer coordinates:

1. Confidential report intake.
2. Vulnerability validation and risk assessment.
3. Remediation planning.
4. Fix implementation and testing.
5. Release coordination.
6. Public disclosure when appropriate.
7. Follow-up security improvements.

The project does not currently guarantee specific remediation deadlines or support multiple maintained release branches.

## 7. Release Governance

Chorecast maintains independently versioned server and Android components.

Releases are managed using the project's documented Conventional Commit and release automation workflow.

The Project Maintainer is responsible for approving release readiness.

The Security Maintainer is responsible for evaluating unresolved security risks that may affect a release.

See [Releasing](docs/releasing.md) for the release process.

## 8. Maintainer Appointments and Succession

The Project Maintainer may appoint additional maintainers based on sustained contributions, technical judgment, demonstrated reliability, and alignment with project responsibilities.

Maintainer appointments should be documented in this file and reflected in CODEOWNERS and repository permissions where appropriate.

A Security Maintainer appointment requires an explicitly identified individual who agrees to assume the responsibilities of the role.

If the designated Security Maintainer becomes unavailable, the Project Maintainer is responsible for appointing a replacement when practical.

A transition should address:

- Outstanding private vulnerability reports.
- Pending security advisories and fixes.
- Required repository permissions.
- Release and signing responsibilities.
- Continuity of security reporting channels.

Changes to project ownership or maintainer authority should be recorded in the repository.

## 9. Transparency and Confidentiality

Project decisions should be made openly whenever practical.

Issues, pull requests, and repository documentation are the preferred locations for ordinary technical discussions.

Exceptions include:

- Unpatched security vulnerabilities.
- Credentials and signing material.
- Personal or household information.
- Confidential conduct reports.
- Other information requiring restricted access.

Maintainers must avoid disclosing sensitive information without authorization.

## 10. Community Standards

All project participants, including maintainers, are expected to comply with the [Code of Conduct](CODE_OF_CONDUCT.md).

Maintainers are responsible for applying community standards consistently and addressing reported violations.

## 11. Policy Changes

Changes to this governance document require approval by the Project Maintainer.

Changes affecting security responsibilities should also be reviewed by the designated Security Maintainer.

Governance changes should be documented through the normal pull request process whenever practical.

## Related Documents

- [CODEOWNERS](.github/CODEOWNERS) — Repository code ownership.
- [CONTRIBUTING.md](CONTRIBUTING.md) — Contribution guidelines.
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Community standards.
- [SECURITY.md](SECURITY.md) — Vulnerability disclosure and security policy.
- [PRIVACY.md](PRIVACY.md) — Privacy and data-handling policy.
- [Architecture](docs/architecture.md) — Technical architecture and decisions.
- [Releasing](docs/releasing.md) — Release management.

---

## Formal Maintainer Designation

The Chorecast project formally designates **[@voidrot](https://github.com/voidrot)** as:

**Project Maintainer:** Responsible for technical direction, repository maintenance, contributions, releases, and overall project governance.

**Security Maintainer:** Responsible for security vulnerability triage, remediation coordination, security review, disclosure management, and security advisories.

This public designation establishes named ownership of the project's ongoing security maintenance responsibilities.