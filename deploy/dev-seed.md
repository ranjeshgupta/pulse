# Local development accounts

Test values for the local Docker stack only. Never reuse them anywhere real.

| What | Where | Credentials |
|---|---|---|
| Demo workspace (created by `make seed`) | http://localhost:7080/login | workspace ID printed by the seed (`demo-retail`, or `demo-retail-xxxxxx` on later runs), `demo.admin@pulse.test` / `pulse-demo-pass-1` |
| Tutorial workspace (made by hand while writing tutorial 1; only in this dev database) | http://localhost:7080/login | `tutorial-co`, `tutorial.admin@pulse.test` / `pulse-tutorial-pass-1`; editor `tutorial.editor@pulse.test` / `pulse-tutorial-pass-2` |
| Keycloak SSO test user (realm `pulse`) | http://localhost:8180 | `sso.tester` / `Pulse-SSO-test-1` |
| Keycloak admin console | http://localhost:8180/admin | `admin` / `admin` |
| Platform admin token (`POST /v1/tenants`) | | `dev-admin-token` |

To try SSO against the dev Keycloak, use these values in Settings → Sign-in & keys → Single sign-on (OIDC) in the demo workspace:

- Issuer URL: `http://localhost:8180/realms/pulse`
- Discovery URL: `http://keycloak:8180/realms/pulse`
- Client ID: `pulse-app`
- Client secret: `pulse-dev-client-secret`
