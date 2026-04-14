# Chirpy

## Auth Endpoints

`POST /api/login` accepts an email/password pair and returns both a 1-hour
access token and a refresh token.

`POST /api/refresh` requires `Authorization: Bearer <refresh-token>`. It
returns `401` when the refresh token is missing, unknown, expired, or revoked.
Otherwise it returns `200` with a freshly minted 1-hour access token:

```json
{
  "token": "new-access-token"
}
```

`POST /api/revoke` also requires `Authorization: Bearer <refresh-token>`. It
revokes the matching refresh-token record, updates its `updated_at` timestamp,
and returns `204 No Content`.

## Testing

The auth helpers in `internal/auth/auth.go` have unit tests covering password
hashing, password verification, JWT round trips, bearer-token parsing, refresh
token generation, and common JWT failure cases.

The generated query helpers in `internal/database` also have unit tests that
verify SQL execution and row scanning for the user, chirp, and refresh-token
queries.

Run the auth tests with:

```sh
go test ./internal/auth
```

Run the database tests with:

```sh
go test ./internal/database
```
