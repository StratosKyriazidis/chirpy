# Chirpy

## Chirp Endpoints

`GET /api/chirps` returns chirps sorted by `created_at` in ascending order.
It also accepts an optional `author_id` query parameter to limit results to
chirps created by a specific author:

```text
GET /api/chirps?author_id=<user-uuid>
```

If `author_id` is provided but is not a valid UUID, the endpoint returns `400`.

## Auth Endpoints

`POST /api/login` accepts an email/password pair and returns both a 1-hour
access token and a refresh token. User responses now include
`is_chirpy_red`.

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

`PUT /api/users` requires `Authorization: Bearer <access-token>` plus a JSON
body with a new `email` and `password`. It updates the authenticated user's
email and hashed password, returns `200`, and responds with the updated user
resource without the password. If the access token is missing or malformed, it
returns `401`.

`DELETE /api/chirps/{chirpID}` requires `Authorization: Bearer <access-token>`.
It deletes the chirp only when the authenticated user is that chirp's author.
It returns `204` on success, `403` if the user is not the author, and `404` if
the chirp does not exist.

`POST /api/polka/webhooks` requires `Authorization: ApiKey <polka-key>`, where
the key must match `POLKA_KEY` from `.env`. Requests with a missing or invalid
API key return `401`. Events other than `user.upgraded` are ignored with
`204 No Content`. A `user.upgraded` event marks the referenced user as a
Chirpy Red member and also returns `204`. If the referenced user does not
exist, it returns `404`.

## Testing

The auth helpers in `internal/auth/auth.go` have unit tests covering password
hashing, password verification, JWT round trips, bearer-token parsing, refresh
token generation, API-key parsing, and common JWT failure cases.

The generated query helpers in `internal/database` also have unit tests that
verify SQL execution and row scanning for the user, chirp, and refresh-token
queries, including Chirpy Red upgrades.

Run the auth tests with:

```sh
go test ./internal/auth
```

Run the database tests with:

```sh
go test ./internal/database
```
