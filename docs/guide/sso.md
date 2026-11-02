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

The server expects RS256-signed ID tokens. Choose an RS256 signing key for the ID token in your provider. An ID token signed with another algorithm, such as HS256, is rejected, and the sign-in redirect returns `sso_error=failed`.

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
   - **Signing key:** select a certificate-key pair. Without one, Authentik signs with HS256, which the server rejects.
   - For the menu path and other provider settings, see the Authentik documentation for OAuth2/OpenID providers.
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

In PocketID, create an OIDC client with the redirect URI `https://chores.example.com/api/v1/auth/oidc/callback` and the `openid` and `profile` scopes. Use a confidential client with a client secret. For the PocketID UI and client options, see the PocketID documentation.

The issuer is your PocketID base URL, with no trailing slash:

```yaml
CHORECAST_OIDC_ISSUER: https://id.example.com
CHORECAST_OIDC_CLIENT_ID: <client ID from PocketID>
CHORECAST_OIDC_CLIENT_SECRET: <client secret from PocketID>
```

Check that the issuer's discovery document loads at `<issuer>/.well-known/openid-configuration`. Its `iss` value must match `CHORECAST_OIDC_ISSUER` exactly.
