package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestHashPasswordAndCheckPasswordHash(t *testing.T) {
	t.Parallel()

	password := "correct horse battery staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned an empty hash")
	}
	if hash == password {
		t.Fatal("HashPassword returned the raw password")
	}

	match, err := CheckPasswordHash(password, hash)
	if err != nil {
		t.Fatalf("CheckPasswordHash returned error for correct password: %v", err)
	}
	if !match {
		t.Fatal("CheckPasswordHash returned false for the correct password")
	}

	match, err = CheckPasswordHash("wrong password", hash)
	if err != nil {
		t.Fatalf("CheckPasswordHash returned error for wrong password: %v", err)
	}
	if match {
		t.Fatal("CheckPasswordHash returned true for the wrong password")
	}
}

func TestCheckPasswordHashInvalidHash(t *testing.T) {
	t.Parallel()

	_, err := CheckPasswordHash("password", "not-a-valid-argon2-hash")
	if err == nil {
		t.Fatal("expected CheckPasswordHash to return an error for an invalid hash")
	}
}

func TestMakeJWTAndValidateJWTRoundTrip(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	secret := "super-secret"

	tokenString, err := MakeJWT(userID, secret, time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT returned error: %v", err)
	}

	validatedID, err := ValidateJWT(tokenString, secret)
	if err != nil {
		t.Fatalf("ValidateJWT returned error: %v", err)
	}
	if validatedID != userID {
		t.Fatalf("ValidateJWT returned %s, want %s", validatedID, userID)
	}

	claims := jwt.RegisteredClaims{}
	parsedToken, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil {
		t.Fatalf("jwt.ParseWithClaims returned error: %v", err)
	}
	if !parsedToken.Valid {
		t.Fatal("parsed token is not valid")
	}
	if claims.Issuer != "chirpy-access" {
		t.Fatalf("claims issuer = %q, want %q", claims.Issuer, "chirpy-access")
	}
	if claims.Subject != userID.String() {
		t.Fatalf("claims subject = %q, want %q", claims.Subject, userID.String())
	}
	if claims.IssuedAt == nil {
		t.Fatal("claims are missing issued-at timestamp")
	}
	if claims.ExpiresAt == nil {
		t.Fatal("claims are missing expiry timestamp")
	}
	if !claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
		t.Fatal("token expiry is not after issued-at time")
	}
}

func TestValidateJWTRejectsWrongSecret(t *testing.T) {
	t.Parallel()

	tokenString, err := MakeJWT(uuid.New(), "correct-secret", time.Minute)
	if err != nil {
		t.Fatalf("MakeJWT returned error: %v", err)
	}

	_, err = ValidateJWT(tokenString, "wrong-secret")
	if err == nil {
		t.Fatal("expected ValidateJWT to reject a token signed with a different secret")
	}
}

func TestValidateJWTRejectsInvalidSubject(t *testing.T) {
	t.Parallel()

	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    "chirpy-access",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		Subject:   "not-a-uuid",
	})

	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("SignedString returned error: %v", err)
	}

	_, err = ValidateJWT(tokenString, secret)
	if err == nil {
		t.Fatal("expected ValidateJWT to reject a token with a non-UUID subject")
	}
}
