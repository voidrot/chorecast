# Privacy Policy

**Project:** Chorecast  
**Last updated:** November 5, 2026

## Overview

Chorecast is an open-source, self-hosted chore and behavior management system designed for families.

Chorecast prioritizes user privacy, data ownership, and transparency. The application is designed to operate on infrastructure controlled by the person or organization deploying it, rather than requiring a centrally hosted service.

Because Chorecast may process personal information relating to children, protecting household information is an important consideration in its design and operation.

This policy describes the types of information Chorecast processes, how that information is used and stored, and the responsibilities of those operating Chorecast installations.

## 1. Data Ownership and Responsibility

Chorecast is primarily a self-hosted application.

The person or organization hosting a Chorecast instance (the **Instance Administrator**) controls the infrastructure on which household information is stored.

The Chorecast project and its maintainers do not automatically receive access to information stored in independently operated instances.

Instance Administrators are responsible for:

- Managing access to their installations.
- Determining what personal information is entered into Chorecast.
- Securing the hosting environment and associated storage.
- Managing data retention, backups, and deletion.
- Configuring any external integrations.
- Complying with applicable privacy and data protection requirements.

The Chorecast maintainers are responsible for developing and maintaining the open-source software but do not operate or control independently hosted installations.

## 2. Information Processed by Chorecast

Depending on how an instance is configured and used, Chorecast may process the following categories of information.

### Household Information

- Household name.
- Household timezone and scheduling preferences.
- Household settings and configuration.
- Chore assignments and schedules.

### Parent and Guardian Information

- Names and usernames.
- Authentication credentials and associated verification information.
- Account settings and preferences.
- Account roles and permissions.
- Account activity and security-related audit records.
- Linked identity provider identifiers when single sign-on is enabled.

Passwords and PINs are stored as password hashes rather than plaintext values.

### Children's Information

- Display names.
- Birth dates.
- Profile avatars and appearance settings.
- Chore assignments and completion history.
- Behavior records and related activity.
- Points, rewards, and progress information.
- Optional PIN authentication information.
- Uploaded evidence associated with completed chores.

This information is used to provide household management functionality and is not intended for public distribution.

### Device and Session Information

- Registered and paired devices.
- Device authentication tokens.
- Session information.
- Device names and associated profiles.
- Authentication and security-related activity.

Device information supports authentication, session management, shared-device functionality, and access control.

### Uploaded Media

Chorecast supports uploading photographic evidence associated with chores.

Uploaded media may contain personal or sensitive information, including identifiable individuals, household interiors, and location-related details.

Users should avoid uploading unnecessary personal information or sensitive images.

## 3. How Information Is Used

Chorecast processes information to provide its intended functionality, including:

- Managing households and family members.
- Creating, assigning, and scheduling chores.
- Tracking chore completion and behavior.
- Recording points and progress.
- Supporting parental review and approval workflows.
- Managing paired devices and shared family tablets.
- Authenticating users and enforcing access permissions.
- Maintaining audit records and application integrity.
- Creating backups and restoring household data.
- Delivering notifications and reminders where supported and configured.

Chorecast does not require household information to be transmitted to the project's maintainers for these core functions.

## 4. Data Storage

Chorecast stores its primary application data in a SQLite database located within the configured data directory.

In standard Docker deployments, this directory is mounted at `/data`.

Depending on application usage, stored information includes:

- Household configuration.
- Parent and child profiles.
- Authentication-related records.
- Chore and behavior history.
- Uploaded photographs.
- Application audit records.
- Backup archives.

Data is stored on infrastructure controlled by the Instance Administrator.

The security of the underlying storage, filesystem, hardware, operating system, and network is the responsibility of the Instance Administrator.

## 5. Authentication and Security

Chorecast provides multiple authentication and authorization mechanisms.

These include:

- Password authentication using Argon2id hashing.
- Optional time-based one-time passwords (TOTP).
- Optional OpenID Connect (OIDC) authentication.
- Browser session cookies.
- Device-pairing credentials.
- Parent and child PINs.
- Access controls based on roles and permissions.

Authentication tokens are generated as opaque random values, and token hashes are stored server-side.

The Android application uses Android Keystore-based protection for stored authentication credentials.

Certain sensitive information, including TOTP secrets, must remain accessible to the application to provide authentication functionality.

Administrators should protect the database and backups accordingly.

See [SECURITY.md](SECURITY.md) for vulnerability reporting and security recommendations.

## 6. Children's Privacy

Chorecast is designed for family use and may process personal information relating to minors.

Children do not need individual email addresses or traditional username/password accounts to use Chorecast.

Child access is managed through devices paired by an authorized parent or guardian.

The Instance Administrator is responsible for determining what information about children is collected, retained, and made accessible within the household.

Parents and guardians should:

- Limit personal information to what is necessary.
- Avoid entering unnecessary sensitive details.
- Review which devices can access children's profiles.
- Configure shared-device access appropriately.
- Remove information that is no longer required.
- Protect photographs and behavior records from unauthorized access.

Chorecast is not intended to independently collect personal information from children on behalf of its maintainers.

## 7. Third-Party Services and Integrations

Chorecast can operate without relying on a centrally hosted Chorecast account service.

Some features may interact with external services when enabled or configured by the Instance Administrator.

Examples include:

### Single Sign-On

Administrators may configure an external OpenID Connect identity provider.

Authentication interactions with that provider are subject to the provider's own privacy practices.

### Notifications

Notification integrations may involve external push delivery infrastructure.

Depending on the selected Android application variant and configured notification service, notification delivery may involve third-party providers.

The architecture is designed to support content-free push notifications, limiting information included in push messages.

The availability and behavior of notification integrations depend on the application version and administrator configuration.

### Application Distribution

Android application builds may be distributed through GitHub Releases or other distribution platforms.

Those platforms may collect information independently under their own privacy policies.

### Third-Party Integrations

Future or optional integrations may exchange information with external services as required for their functionality.

Administrators should review the configuration and privacy implications of any integration before enabling it.

## 8. Data Sharing

Chorecast does not require a centralized project-operated service to store or process household records.

The Chorecast maintainers do not sell household data or operate a data brokerage service.

Information may nevertheless be accessible to:

- Authorized users of the self-hosted instance.
- The Instance Administrator and other parties with infrastructure access.
- External identity providers or notification services when configured.
- Third-party services explicitly integrated with an installation.
- Parties authorized to access backups or exported data.

The Instance Administrator determines whether and how household information is shared outside the installation.

## 9. Data Retention and Deletion

Household information remains under the control of the Instance Administrator.

Retention depends on the application's functionality, administrator configuration, and any backup or external storage processes.

Removing information from the active application does not necessarily remove copies from existing backup archives.

Administrators are responsible for:

- Reviewing retained household information.
- Deleting unnecessary records where supported.
- Managing backup retention.
- Removing outdated backup archives.
- Securely decommissioning installations and storage media.

Chorecast provides scheduled backup functionality, but its existence does not establish a universal retention period for every category of information.

## 10. Backups

Chorecast supports scheduled and manually initiated backups.

Backup archives can include the application database, uploaded media, and authentication-related information.

**Backup archives contain sensitive information and must be protected.**

Instance Administrators should store backups securely, restrict access, use encryption where appropriate, and maintain copies separately from the application host.

Backup files should not be committed to source control or uploaded to publicly accessible locations.

See [Backups and restore](docs/guide/backups.md) for operational guidance.

## 11. Network Communication

Chorecast clients communicate with the configured Chorecast server to access application functionality.

Depending on configuration, additional communication may occur with:

- External identity providers.
- Push notification infrastructure.
- Application distribution services.
- Explicitly enabled integrations.

For connections over untrusted networks, administrators should configure HTTPS.

Chorecast does not guarantee the confidentiality of network traffic when deployed over an insecure connection.

## 12. Analytics and Telemetry

Chorecast is designed for self-hosted operation without requiring a centralized analytics or telemetry service.

The core application does not require a project-operated telemetry endpoint for household management functionality.

This statement does not cover monitoring, analytics, logging, or network inspection independently configured by an Instance Administrator or provided by external services.

Any future introduction of project-operated telemetry or analytics should be documented, including the information collected, its purpose, and available user controls.

## 13. Data Access and Privacy Requests

Individuals seeking access to, correction of, or deletion of information stored in a particular Chorecast installation should contact that installation's administrator.

The Chorecast maintainers generally cannot access, modify, export, or delete information stored in independently operated instances.

Privacy-related software defects may be reported through the project's GitHub repository.

Potential security vulnerabilities involving personal information should be reported privately according to [SECURITY.md](SECURITY.md).

## 14. International Use

Chorecast can be deployed in different geographic regions.

The location of household data depends on where the Instance Administrator hosts the application and any associated backups or integrations.

Administrators are responsible for understanding applicable legal requirements, including those concerning children's information and international data transfers.

Chorecast's availability as open-source software does not itself establish compliance with GDPR, COPPA, or other privacy regulations.

## 15. Changes to This Policy

This Privacy Policy may be updated as Chorecast develops and its functionality changes.

Material changes should be documented in the repository's version history.

The latest version of this policy is available at:

https://github.com/voidrot/chorecast/blob/main/PRIVACY.md

## 16. Contact

For general privacy questions, use the project's [GitHub repository](https://github.com/voidrot/chorecast).

For security vulnerabilities or privacy defects that could expose sensitive information, follow the private reporting process described in [SECURITY.md](SECURITY.md).

**Project Maintainer:** [@voidrot](https://github.com/voidrot)  
**Security Maintainer:** [@voidrot](https://github.com/voidrot)

---

Chorecast is distributed as open-source, self-hosted software. This policy describes the application's intended data handling and the responsibilities associated with its operation. It does not replace the privacy notices or legal obligations of individual Instance Administrators.
