package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

const stubDriverName = "chirpy-test-stub"

var (
	stubDriverCounter atomic.Int64
	stubStates        sync.Map
)

func init() {
	sql.Register(stubDriverName, stubDriver{})
}

type stubDriver struct{}

func (stubDriver) Open(name string) (driver.Conn, error) {
	stateValue, ok := stubStates.Load(name)
	if !ok {
		return nil, fmt.Errorf("missing stub state for %q", name)
	}
	return &stubConn{state: stateValue.(*stubState)}, nil
}

type stubState struct {
	execFunc    func(string, []driver.NamedValue) (driver.Result, error)
	queryFunc   func(string, []driver.NamedValue) (driver.Rows, error)
	beginTxFunc func(driver.TxOptions) (driver.Tx, error)
}

type stubConn struct {
	state *stubState
}

func (c *stubConn) Prepare(string) (driver.Stmt, error) {
	return stubStmt{}, nil
}

func (c *stubConn) Close() error {
	return nil
}

func (c *stubConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *stubConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.state.beginTxFunc != nil {
		return c.state.beginTxFunc(opts)
	}
	return stubTx{}, nil
}

func (c *stubConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.state.execFunc == nil {
		return nil, fmt.Errorf("unexpected exec: %s", query)
	}
	return c.state.execFunc(query, args)
}

func (c *stubConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.state.queryFunc == nil {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	return c.state.queryFunc(query, args)
}

type stubStmt struct{}

func (stubStmt) Close() error {
	return nil
}

func (stubStmt) NumInput() int {
	return -1
}

func (stubStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, fmt.Errorf("prepared statements are not used in these tests")
}

func (stubStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, fmt.Errorf("prepared statements are not used in these tests")
}

type stubTx struct{}

func (stubTx) Commit() error {
	return nil
}

func (stubTx) Rollback() error {
	return nil
}

type stubResult struct {
	rowsAffected int64
}

func (r stubResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r stubResult) RowsAffected() (int64, error) {
	return r.rowsAffected, nil
}

type stubRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *stubRows) Columns() []string {
	return r.columns
}

func (r *stubRows) Close() error {
	return nil
}

func (r *stubRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}

func openStubDB(t *testing.T, state *stubState) *sql.DB {
	t.Helper()

	dsn := fmt.Sprintf("stub-%d", stubDriverCounter.Add(1))
	stubStates.Store(dsn, state)

	db, err := sql.Open(stubDriverName, dsn)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}

	t.Cleanup(func() {
		stubStates.Delete(dsn)
		_ = db.Close()
	})

	return db
}

func assertNamedArgs(t *testing.T, got []driver.NamedValue, want ...any) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("argument count = %d, want %d", len(got), len(want))
	}

	for i, expected := range want {
		converted, err := driver.DefaultParameterConverter.ConvertValue(expected)
		if err != nil {
			t.Fatalf("could not convert argument %d: %v", i, err)
		}
		if !reflect.DeepEqual(got[i].Value, converted) {
			t.Fatalf("argument %d = %#v, want %#v", i, got[i].Value, converted)
		}
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	db := openStubDB(t, &stubState{})
	queries := New(db)

	if queries == nil {
		t.Fatal("New returned nil")
	}
	if queries.db != db {
		t.Fatal("New did not store the provided DB handle")
	}
}

func TestWithTx(t *testing.T) {
	t.Parallel()

	state := &stubState{
		execFunc: func(query string, args []driver.NamedValue) (driver.Result, error) {
			if query != deleteUsers {
				t.Fatalf("query = %q, want %q", query, deleteUsers)
			}
			if len(args) != 0 {
				t.Fatalf("argument count = %d, want 0", len(args))
			}
			return stubResult{rowsAffected: 1}, nil
		},
	}
	db := openStubDB(t, state)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback()
	})

	queries := New(db)
	txQueries := queries.WithTx(tx)

	if txQueries == queries {
		t.Fatal("WithTx returned the original Queries pointer")
	}
	if txQueries.db != tx {
		t.Fatal("WithTx did not store the provided transaction")
	}
	if err := txQueries.DeleteUsers(context.Background()); err != nil {
		t.Fatalf("DeleteUsers returned error: %v", err)
	}
}

func TestCreateUser(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.UTC)
	params := CreateUserParams{
		ID:             uuid.New(),
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Minute),
		Email:          "user@example.com",
		HashedPassword: "hashed-password",
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != createUser {
				t.Fatalf("query = %q, want %q", query, createUser)
			}
			assertNamedArgs(t, args, params.ID, params.CreatedAt, params.UpdatedAt, params.Email, params.HashedPassword)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "email", "hashed_password", "is_chirpy_red"},
				rows: [][]driver.Value{{
					params.ID.String(),
					params.CreatedAt,
					params.UpdatedAt,
					params.Email,
					params.HashedPassword,
					false,
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	user, err := queries.CreateUser(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateUser returned error: %v", err)
	}

	if user.ID != params.ID {
		t.Fatalf("user ID = %s, want %s", user.ID, params.ID)
	}
	if user.CreatedAt != params.CreatedAt {
		t.Fatalf("created_at = %v, want %v", user.CreatedAt, params.CreatedAt)
	}
	if user.UpdatedAt != params.UpdatedAt {
		t.Fatalf("updated_at = %v, want %v", user.UpdatedAt, params.UpdatedAt)
	}
	if user.Email != params.Email {
		t.Fatalf("email = %q, want %q", user.Email, params.Email)
	}
	if user.HashedPassword != params.HashedPassword {
		t.Fatalf("hashed_password = %q, want %q", user.HashedPassword, params.HashedPassword)
	}
	if user.IsChirpyRed {
		t.Fatal("is_chirpy_red = true, want false")
	}
}

func TestDeleteUsers(t *testing.T) {
	t.Parallel()

	state := &stubState{
		execFunc: func(query string, args []driver.NamedValue) (driver.Result, error) {
			if query != deleteUsers {
				t.Fatalf("query = %q, want %q", query, deleteUsers)
			}
			if len(args) != 0 {
				t.Fatalf("argument count = %d, want 0", len(args))
			}
			return stubResult{rowsAffected: 2}, nil
		},
	}

	queries := New(openStubDB(t, state))
	if err := queries.DeleteUsers(context.Background()); err != nil {
		t.Fatalf("DeleteUsers returned error: %v", err)
	}
}

func TestGetUser(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 10, 13, 0, 0, 0, time.UTC)
	expected := User{
		ID:             uuid.New(),
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Minute),
		Email:          "user@example.com",
		HashedPassword: "hashed-password",
		IsChirpyRed:    false,
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != getUser {
				t.Fatalf("query = %q, want %q", query, getUser)
			}
			assertNamedArgs(t, args, expected.Email)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "email", "hashed_password", "is_chirpy_red"},
				rows: [][]driver.Value{{
					expected.ID.String(),
					expected.CreatedAt,
					expected.UpdatedAt,
					expected.Email,
					expected.HashedPassword,
					expected.IsChirpyRed,
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	user, err := queries.GetUser(context.Background(), expected.Email)
	if err != nil {
		t.Fatalf("GetUser returned error: %v", err)
	}
	if !reflect.DeepEqual(user, expected) {
		t.Fatalf("user = %#v, want %#v", user, expected)
	}
}

func TestUpdateUser(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.April, 10, 13, 0, 0, 0, time.UTC)
	params := UpdateUserParams{
		ID:             uuid.New(),
		Email:          "updated@example.com",
		HashedPassword: "updated-hash",
		UpdatedAt:      createdAt.Add(2 * time.Hour),
	}
	expected := User{
		ID:             params.ID,
		CreatedAt:      createdAt,
		UpdatedAt:      params.UpdatedAt,
		Email:          params.Email,
		HashedPassword: params.HashedPassword,
		IsChirpyRed:    false,
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != updateUser {
				t.Fatalf("query = %q, want %q", query, updateUser)
			}
			assertNamedArgs(t, args, params.ID, params.Email, params.HashedPassword, params.UpdatedAt)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "email", "hashed_password", "is_chirpy_red"},
				rows: [][]driver.Value{{
					expected.ID.String(),
					expected.CreatedAt,
					expected.UpdatedAt,
					expected.Email,
					expected.HashedPassword,
					expected.IsChirpyRed,
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	user, err := queries.UpdateUser(context.Background(), params)
	if err != nil {
		t.Fatalf("UpdateUser returned error: %v", err)
	}
	if !reflect.DeepEqual(user, expected) {
		t.Fatalf("user = %#v, want %#v", user, expected)
	}
}

func TestUpgradeUserToChirpyRed(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.April, 10, 13, 0, 0, 0, time.UTC)
	params := UpgradeUserToChirpyRedParams{
		ID:        uuid.New(),
		UpdatedAt: createdAt.Add(3 * time.Hour),
	}
	expected := User{
		ID:             params.ID,
		CreatedAt:      createdAt,
		UpdatedAt:      params.UpdatedAt,
		Email:          "red@example.com",
		HashedPassword: "hashed-password",
		IsChirpyRed:    true,
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != upgradeUserToChirpyRed {
				t.Fatalf("query = %q, want %q", query, upgradeUserToChirpyRed)
			}
			assertNamedArgs(t, args, params.ID, params.UpdatedAt)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "email", "hashed_password", "is_chirpy_red"},
				rows: [][]driver.Value{{
					expected.ID.String(),
					expected.CreatedAt,
					expected.UpdatedAt,
					expected.Email,
					expected.HashedPassword,
					expected.IsChirpyRed,
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	user, err := queries.UpgradeUserToChirpyRed(context.Background(), params)
	if err != nil {
		t.Fatalf("UpgradeUserToChirpyRed returned error: %v", err)
	}
	if !reflect.DeepEqual(user, expected) {
		t.Fatalf("user = %#v, want %#v", user, expected)
	}
}

func TestCreateChirp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 10, 14, 0, 0, 0, time.UTC)
	params := CreateChirpParams{
		ID:        uuid.New(),
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
		Body:      "hello world",
		UserID:    uuid.New(),
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != createChirp {
				t.Fatalf("query = %q, want %q", query, createChirp)
			}
			assertNamedArgs(t, args, params.ID, params.CreatedAt, params.UpdatedAt, params.Body, params.UserID)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "body", "user_id"},
				rows: [][]driver.Value{{
					params.ID.String(),
					params.CreatedAt,
					params.UpdatedAt,
					params.Body,
					params.UserID.String(),
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	chirp, err := queries.CreateChirp(context.Background(), params)
	if err != nil {
		t.Fatalf("CreateChirp returned error: %v", err)
	}

	if chirp.ID != params.ID {
		t.Fatalf("chirp ID = %s, want %s", chirp.ID, params.ID)
	}
	if chirp.CreatedAt != params.CreatedAt {
		t.Fatalf("created_at = %v, want %v", chirp.CreatedAt, params.CreatedAt)
	}
	if chirp.UpdatedAt != params.UpdatedAt {
		t.Fatalf("updated_at = %v, want %v", chirp.UpdatedAt, params.UpdatedAt)
	}
	if chirp.Body != params.Body {
		t.Fatalf("body = %q, want %q", chirp.Body, params.Body)
	}
	if chirp.UserID != params.UserID {
		t.Fatalf("user_id = %s, want %s", chirp.UserID, params.UserID)
	}
}

func TestDeleteChirps(t *testing.T) {
	t.Parallel()

	state := &stubState{
		execFunc: func(query string, args []driver.NamedValue) (driver.Result, error) {
			if query != deleteChirps {
				t.Fatalf("query = %q, want %q", query, deleteChirps)
			}
			if len(args) != 0 {
				t.Fatalf("argument count = %d, want 0", len(args))
			}
			return stubResult{rowsAffected: 3}, nil
		},
	}

	queries := New(openStubDB(t, state))
	if err := queries.DeleteChirps(context.Background()); err != nil {
		t.Fatalf("DeleteChirps returned error: %v", err)
	}
}

func TestDeleteChirp(t *testing.T) {
	t.Parallel()

	chirpID := uuid.New()
	state := &stubState{
		execFunc: func(query string, args []driver.NamedValue) (driver.Result, error) {
			if query != deleteChirp {
				t.Fatalf("query = %q, want %q", query, deleteChirp)
			}
			assertNamedArgs(t, args, chirpID)
			return stubResult{rowsAffected: 1}, nil
		},
	}

	queries := New(openStubDB(t, state))
	if err := queries.DeleteChirp(context.Background(), chirpID); err != nil {
		t.Fatalf("DeleteChirp returned error: %v", err)
	}
}

func TestGetChirp(t *testing.T) {
	t.Parallel()

	expected := Chirp{
		ID:        uuid.New(),
		CreatedAt: time.Date(2026, time.April, 10, 15, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, time.April, 10, 15, 1, 0, 0, time.UTC),
		Body:      "one chirp",
		UserID:    uuid.New(),
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != getChirp {
				t.Fatalf("query = %q, want %q", query, getChirp)
			}
			assertNamedArgs(t, args, expected.ID)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "body", "user_id"},
				rows: [][]driver.Value{{
					expected.ID.String(),
					expected.CreatedAt,
					expected.UpdatedAt,
					expected.Body,
					expected.UserID.String(),
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	chirp, err := queries.GetChirp(context.Background(), expected.ID)
	if err != nil {
		t.Fatalf("GetChirp returned error: %v", err)
	}
	if !reflect.DeepEqual(chirp, expected) {
		t.Fatalf("chirp = %#v, want %#v", chirp, expected)
	}
}

func TestGetChirps(t *testing.T) {
	t.Parallel()

	expected := []Chirp{
		{
			ID:        uuid.New(),
			CreatedAt: time.Date(2026, time.April, 10, 16, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, time.April, 10, 16, 1, 0, 0, time.UTC),
			Body:      "first",
			UserID:    uuid.New(),
		},
		{
			ID:        uuid.New(),
			CreatedAt: time.Date(2026, time.April, 10, 17, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, time.April, 10, 17, 1, 0, 0, time.UTC),
			Body:      "second",
			UserID:    uuid.New(),
		},
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != getChirps {
				t.Fatalf("query = %q, want %q", query, getChirps)
			}
			if len(args) != 0 {
				t.Fatalf("argument count = %d, want 0", len(args))
			}
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "body", "user_id"},
				rows: [][]driver.Value{
					{
						expected[0].ID.String(),
						expected[0].CreatedAt,
						expected[0].UpdatedAt,
						expected[0].Body,
						expected[0].UserID.String(),
					},
					{
						expected[1].ID.String(),
						expected[1].CreatedAt,
						expected[1].UpdatedAt,
						expected[1].Body,
						expected[1].UserID.String(),
					},
				},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	chirps, err := queries.GetChirps(context.Background())
	if err != nil {
		t.Fatalf("GetChirps returned error: %v", err)
	}
	if !reflect.DeepEqual(chirps, expected) {
		t.Fatalf("chirps = %#v, want %#v", chirps, expected)
	}
}

func TestGetChirpsByAuthor(t *testing.T) {
	t.Parallel()

	authorID := uuid.New()
	expected := []Chirp{
		{
			ID:        uuid.New(),
			CreatedAt: time.Date(2026, time.April, 10, 16, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, time.April, 10, 16, 1, 0, 0, time.UTC),
			Body:      "first",
			UserID:    authorID,
		},
		{
			ID:        uuid.New(),
			CreatedAt: time.Date(2026, time.April, 10, 17, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, time.April, 10, 17, 1, 0, 0, time.UTC),
			Body:      "second",
			UserID:    authorID,
		},
	}
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != getChirpsByAuthor {
				t.Fatalf("query = %q, want %q", query, getChirpsByAuthor)
			}
			assertNamedArgs(t, args, authorID)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "body", "user_id"},
				rows: [][]driver.Value{
					{
						expected[0].ID.String(),
						expected[0].CreatedAt,
						expected[0].UpdatedAt,
						expected[0].Body,
						expected[0].UserID.String(),
					},
					{
						expected[1].ID.String(),
						expected[1].CreatedAt,
						expected[1].UpdatedAt,
						expected[1].Body,
						expected[1].UserID.String(),
					},
				},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	chirps, err := queries.GetChirpsByAuthor(context.Background(), authorID)
	if err != nil {
		t.Fatalf("GetChirpsByAuthor returned error: %v", err)
	}
	if !reflect.DeepEqual(chirps, expected) {
		t.Fatalf("chirps = %#v, want %#v", chirps, expected)
	}
}

func TestGetUserFromRefreshToken(t *testing.T) {
	t.Parallel()

	expected := User{
		ID:             uuid.New(),
		CreatedAt:      time.Date(2026, time.April, 11, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, time.April, 11, 10, 5, 0, 0, time.UTC),
		Email:          "user@example.com",
		HashedPassword: "hashed-password",
		IsChirpyRed:    true,
	}
	token := "refresh-token"
	state := &stubState{
		queryFunc: func(query string, args []driver.NamedValue) (driver.Rows, error) {
			if query != getUserFromRefreshToken {
				t.Fatalf("query = %q, want %q", query, getUserFromRefreshToken)
			}
			assertNamedArgs(t, args, token)
			return &stubRows{
				columns: []string{"id", "created_at", "updated_at", "email", "hashed_password", "is_chirpy_red"},
				rows: [][]driver.Value{{
					expected.ID.String(),
					expected.CreatedAt,
					expected.UpdatedAt,
					expected.Email,
					expected.HashedPassword,
					expected.IsChirpyRed,
				}},
			}, nil
		},
	}

	queries := New(openStubDB(t, state))
	user, err := queries.GetUserFromRefreshToken(context.Background(), token)
	if err != nil {
		t.Fatalf("GetUserFromRefreshToken returned error: %v", err)
	}
	if !reflect.DeepEqual(user, expected) {
		t.Fatalf("user = %#v, want %#v", user, expected)
	}
}

func TestRevokeRefreshToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 11, 11, 0, 0, 0, time.UTC)
	params := RevokeRefreshTokenParams{
		Token:     "refresh-token",
		RevokedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
	}
	state := &stubState{
		execFunc: func(query string, args []driver.NamedValue) (driver.Result, error) {
			if query != revokeRefreshToken {
				t.Fatalf("query = %q, want %q", query, revokeRefreshToken)
			}
			assertNamedArgs(t, args, params.Token, params.RevokedAt, params.UpdatedAt)
			return stubResult{rowsAffected: 1}, nil
		},
	}

	queries := New(openStubDB(t, state))
	if err := queries.RevokeRefreshToken(context.Background(), params); err != nil {
		t.Fatalf("RevokeRefreshToken returned error: %v", err)
	}
}
