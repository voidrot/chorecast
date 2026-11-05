# Security Policy

Security is a priority for Chorecast. As a self-hosted application used by families, Chorecast handles sensitive household information, including children's profiles, chore and behavior records, authentication credentials, and uploaded media.

We appreciate responsible security research and encourage the coordinated disclosure of vulnerabilities that could compromise the confidentiality, integrity, or availability of Chorecast installations.

## Supported Versions

Chorecast consists of independently versioned components:

- **Server:** Go API, embedded web administration interface, database, and background services.
- **Android:** Native Android application, distributed in `play` and `foss` variants.

Security fixes are generally targeted at the latest available release of each affected component.

| Version | Security Support |
|---|---|
| Latest server release | Supported |
| Latest Android release | Supported |
| Older releases | Best effort; upgrade recommended |
| Unreleased development builds | No support guarantee |

Chorecast is under active development. Before version 1.0, breaking changes, including database schema changes, may occur between releases.

There is currently no commitment to long-term support (LTS) releases or backporting security fixes to older versions.

Users should follow the [upgrade guide](docs/guide/upgrading.md) and review release notes before updating.

## Reporting a Vulnerability

**Do not report security vulnerabilities through public GitHub issues, pull requests, discussions, or other public channels.**

If you believe you have discovered a security vulnerability, please report it privately.

### Preferred: GitHub Private Vulnerability Reporting

1. Navigate to the [Chorecast Security page](https://github.com/voidrot/chorecast/security).
2. Select **Report a vulnerability**, if available.
3. Submit a detailed description of the issue and its potential impact.

Private vulnerability reporting allows maintainers and researchers to collaborate on a fix without exposing the vulnerability publicly.

If private reporting is unavailable, contact the repository maintainer privately to establish a secure reporting channel before sharing sensitive details.

### What to Include

Please provide as much of the following information as possible:

- A clear description of the vulnerability and its security impact.
- The affected component (`server`, `web`, `android`, or deployment infrastructure).
- The affected version, release tag, or commit SHA.
- Steps to reproduce the issue or a minimal proof of concept.
- Relevant configuration details, including authentication and deployment settings.
- Whether exploitation requires authentication, user interaction, or network access.
- Any known mitigations or workarounds.
- Relevant logs or screenshots with credentials and personal information removed.

Please do not include real household data, children's information, passwords, authentication tokens, or other sensitive information in reports unless a secure transmission method has been explicitly arranged.

## Disclosure Process

The Chorecast maintainers aim to handle vulnerability reports through coordinated disclosure.

The general process is:

1. **Acknowledgment:** Confirm receipt of the report when practical.
2. **Triage:** Evaluate reproducibility, affected versions, prerequisites, and potential impact.
3. **Remediation:** Develop and validate a fix, including regression tests where appropriate.
4. **Release:** Publish an appropriate security fix for the affected component.
5. **Disclosure:** Coordinate publication of vulnerability details and any applicable GitHub Security Advisory.

Response and remediation times depend on severity, complexity, and maintainer availability. No guaranteed service-level agreement is currently provided.

We ask researchers to avoid public disclosure until a fix is available or a disclosure timeline has been mutually agreed upon.

Maintainers may request additional information to validate findings and coordinate remediation.

## Vulnerabilities of Interest

Security reports are particularly valuable in the following areas.

### Authentication and Authorization

- Authentication bypass or privilege escalation.
- Unauthorized access to parent, owner, or child accounts.
- Improper enforcement of API scopes or child-specific data access restrictions.
- Session fixation, hijacking, or unintended persistence.
- Weaknesses in password, PIN, TOTP, recovery-code, or OIDC authentication.
- Unauthorized creation, reuse, or disclosure of authentication tokens.
- Device-pairing vulnerabilities, including QR-code or pairing-code replay.
- Shared-device session isolation or parent-elevation bypasses.

### Data Privacy and Isolation

- Unauthorized access to household or child information.
- Exposure of birth dates, behavior records, chore history, or other personal data.
- Insecure access to uploaded photographs or evidence.
- Cross-profile access on shared family devices.
- Sensitive data disclosure through API responses, logs, diagnostics, or error messages.
- Improper validation of media access tokens or signed URLs.

### API and Web Security

- Injection vulnerabilities.
- Cross-site scripting (XSS).
- Cross-site request forgery (CSRF).
- Server-side request forgery (SSRF).
- Insecure direct object references (IDOR).
- Authorization bypasses in API endpoints.
- Unsafe file uploads or media processing.
- Security-sensitive race conditions.
- Resource exhaustion or denial-of-service vulnerabilities with demonstrable security impact.

### Android Security

- Insecure storage or exposure of authentication credentials.
- Android Keystore misuse.
- Intent, deep-link, or application-component vulnerabilities.
- Unauthorized access to cached household information.
- Device-session isolation failures.
- Insecure transport or server identity validation.
- Vulnerabilities affecting either the `play` or `foss` application variant.

### Deployment and Supply Chain

- Insecure default configurations.
- Exposure of credentials through container images or build artifacts.
- Vulnerabilities in release packaging or distribution.
- Compromised dependency or build workflows.
- Unsafe database backup or restore operations.
- Path traversal, archive extraction, or filesystem permission vulnerabilities.
- Insecure handling of environment variables or application secrets.

## Responsible Disclosure Guidelines

We welcome good-faith security research conducted against environments that researchers own or are explicitly authorized to test.

Researchers should:

- Use isolated test environments and synthetic data whenever possible.
- Limit testing to what is necessary to demonstrate a vulnerability.
- Avoid accessing, modifying, or deleting other users' data.
- Avoid collecting or retaining personal information.
- Avoid disrupting production installations or shared infrastructure.
- Refrain from publishing working exploits before coordinated disclosure.
- Report discovered vulnerabilities promptly through a private channel.

The following activities are not authorized by this policy:

- Testing third-party Chorecast installations without permission.
- Social engineering, phishing, or credential theft.
- Denial-of-service attacks against systems not owned by the researcher.
- Accessing private household information beyond what is necessary for an authorized test.
- Attacking unrelated infrastructure, identity providers, or third-party services.

This policy does not grant permission to access systems or data owned by others, nor does it establish a legal safe harbor.

## Security Recommendations for Self-Hosting

Chorecast is designed to be self-hosted. Administrators are responsible for securing their own infrastructure and deployment environments.

### Network and Transport Security

- Use HTTPS when accessing Chorecast over untrusted networks.
- Place the application behind a properly configured TLS-terminating reverse proxy when remote access is required.
- Prefer private network access or a VPN over direct public exposure.
- Restrict access to the application's port (`8080`) using appropriate firewall rules.
- Set `CHORECAST_PUBLIC_URL` to the correct externally accessible HTTPS address.
- Do not enable `CHORECAST_DEV` in production deployments.

### Authentication and Access Control

- Use unique, strong passwords for parent accounts.
- Enable TOTP or use an appropriately secured OIDC identity provider.
- Protect OIDC client secrets and related credentials.
- Review active sessions and paired devices periodically.
- Revoke lost, stolen, or unused device credentials.
- Use PIN protection on shared devices where appropriate.
- Treat setup links, pairing codes, session tokens, and recovery codes as sensitive credentials.

### Persistent Data and Backups

Chorecast stores its database, uploaded media, and scheduled backups in its data directory, typically `/data` in container deployments.

This directory contains sensitive information, including authentication-related material.

Administrators should:

- Restrict filesystem access to authorized users and services.
- Encrypt sensitive backups at rest and during transfer.
- Keep backup copies in a secure location separate from the application host.
- Protect backup archives with the same care as the live database.
- Test backup restoration procedures periodically.
- Never publish database files, backups, media directories, or production environment configurations.
- Restore only trusted backup archives.

**Important:** Chorecast backups contain sensitive authentication material, including TOTP secrets and the media-signing key. Treat exposure of a backup archive as a potential credential compromise.

See [Backups and restore](docs/guide/backups.md) for operational guidance.

### Updates and Dependencies

- Use current, supported Chorecast releases.
- Prefer versioned container image tags over floating tags for production deployments.
- Monitor [GitHub Releases](https://github.com/voidrot/chorecast/releases) for updates.
- Review release notes and upgrade instructions before applying updates.
- Keep the container runtime, host operating system, reverse proxy, and identity provider patched.
- Update Android clients when compatible releases become available.

### Child and Household Privacy

Chorecast may contain information relating to minors.

Administrators should minimize unnecessary collection and retention of personal information, restrict access to household records, and avoid sharing exported data or uploaded evidence publicly.

Self-hosted administrators are responsible for protecting data stored within their installations and complying with any applicable privacy requirements.

## Security Fixes and Advisories

Confirmed vulnerabilities may be addressed through:

- Patches to the affected server or Android component.
- Updated container images or Android release artifacts.
- Configuration changes or documented mitigations.
- GitHub Security Advisories, where appropriate.
- Release notes describing the impact and recommended action.

When applicable, advisories will describe affected versions, severity, remediation guidance, and any known workarounds.

Security-related releases follow the project's existing release and versioning processes.

See [Releasing](docs/releasing.md) for details.

## Scope and Limitations

Chorecast is provided as open-source software under the [GNU Affero General Public License v3.0](LICENSE).

This security policy describes how vulnerabilities are reported and handled. It does not provide a warranty, guarantee uninterrupted service, or establish a contractual obligation to supply security fixes.

The project maintainers cannot guarantee the security of independently operated installations, customized builds, third-party integrations, or underlying infrastructure.

## Acknowledgments

We appreciate researchers and contributors who responsibly disclose security vulnerabilities and help improve Chorecast.

With the researcher's consent, significant contributions may be acknowledged in the associated GitHub Security Advisory or release notes.

Thank you for helping keep Chorecast and its users secure.
