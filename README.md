# Chirpy

## Testing

The auth helpers in `internal/auth/auth.go` have unit tests covering password
hashing, password verification, JWT round trips, and common JWT failure cases.

The generated query helpers in `internal/database` also have unit tests that
verify SQL execution and row scanning for the user and chirp queries.

Run the auth tests with:

```sh
go test ./internal/auth
```

Run the database tests with:

```sh
go test ./internal/database
```
