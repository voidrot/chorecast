# Single sign-on

Chorecast can sign parents in through an OpenID Connect (OIDC) provider, such as Authentik or PocketID. SSO is optional. Passwords keep working.

## How it works

- The server is the only OIDC client. It runs the authorization code flow with PKCE, for the web admin and for the Android app.
- Accounts are matched by the identity provider's subject. A parent must link their account once. Nobody is created automatically.
- The identity provider handles second factors. SSO sign-in skips Chorecast's own two-factor step.

## Server settings

Set all three variables in the compose file, then restart the container:

| Variable | Value |
|---|---|
| `CHORECAST_OIDC_ISSUER` | The provider's issuer URL. It must match the `iss` claim exactly, including any trailing slash. |
| `CHORECAST_OIDC_CLIENT_ID` | The client ID from the provider. |
| `CHORECAST_OIDC_CLIENT_SECRET` | The client secret from the provider. |

SSO is on only when the issuer and client ID are set. Set `CHORECAST_PUBLIC_URL` too, when browsers reach the server at an address other than the one the server sees. The redirect URI is built from it. If it's empty, the server uses the address of each request.

Discovery happens the first time someone uses SSO, not at startup. An identity provider that is down doesn't stop Chorecast from starting.

## Register the redirect URI

Register this redirect URI with your provider:

```text
https://<your server address>/api/v1/auth/oidc/callback
```

Use a confidential client with a client secret. Request the `openid` and `profile` scopes. The server doesn't use `email`.

Pick an RS256 signing key for the ID token. The server verifies ID tokens with its OIDC library's default settings, which expect RS256 (UNVERIFIED: I did not test this against a provider). A provider that signs with HS256 may fail with `sso_error=failed`.

## Link a parent account

SSO sign-in works only after a parent links their identity.

1. Sign in to the web admin with your password.
2. On the account page, choose the SSO card and link your identity provider account.
3. Sign out. The login page now has a **Sign in with SSO** option.

The server answers with one of these errors on redirect:

| Code | Meaning |
|---|---|
| `not_linked` | No parent has linked this identity yet. Link it first. |
| `already_linked` | Another parent already linked this identity. |
| `expired` | The sign-in took more than 10 minutes, or the server restarted during it. Start again. |
| `denied` | The identity provider returned an error, for example the user cancelled. |
| `failed` | Code exchange or ID token check failed. Check the issuer, the client secret and the signing key. |

Pending sign-ins are kept in memory. A restart cancels any sign-in in progress.

## Example: Authentik

1. Create an OAuth2/OpenID provider.
   - **Client type:** confidential.
   - **Redirect URIs:** `https://chores.example.com/api/v1/auth/oidc/callback`, with your server's address.
   - **Signing key:** select a certificate-key pair. Don't leave it empty, because Authentik then signs with HS256.
   - UNVERIFIED: the menu path in the Authentik admin UI.
2. Create an application that uses the provider, with the slug `chorecast`.
3. Use the default issuer mode, which gives one issuer per application slug. The issuer is then:

   ```text
   https://auth.example.com/application/o/chorecast/
   ```

   Its discovery document is at `.../application/o/chorecast/.well-known/openid-configuration`.
4. Set the compose environment:

   ```yaml
   CHORECAST_OIDC_ISSUER: https://auth.example.com/application/o/chorecast/
   CHORECAST_OIDC_CLIENT_ID: <client ID from the provider>
   CHORECAST_OIDC_CLIENT_SECRET: <client secret from the provider>
   ```

## Example: PocketID

UNVERIFIED: I could not load the PocketID documentation, so the steps below come from the server's requirements and not from PocketID. Check each one against PocketID's documentation.

1. Create an OIDC client in PocketID.
   - Use a confidential client with a client secret.
   - Set the callback URL to `https://chores.example.com/api/v1/auth/oidc/callback`.
   - Allow the `openid` and `profile` scopes.
2. Set the compose environment. The issuer is usually PocketID's base URL, with no trailing slash:

   ```yaml
   CHORECAST_OIDC_ISSUER: https://id.example.com
   CHORECAST_OIDC_CLIENT_ID: <client ID from PocketID>
   CHORECAST_OIDC_CLIENT_SECRET: <client secret from PocketID>
   ```

   UNVERIFIED: the issuer value.
3. Check that the issuer's discovery document loads: `<issuer>/.well-known/openid-configuration`. It must report an `iss` that matches `CHORECAST_OIDC_ISSUER` exactly.
