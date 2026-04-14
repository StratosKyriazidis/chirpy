package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/argon2id"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func HashPassword(password string) (string, error) {
	if hashed, err := argon2id.CreateHash(password, &argon2id.Params{
		Memory:      argon2id.DefaultParams.Memory,
		Iterations:  argon2id.DefaultParams.Iterations,
		Parallelism: argon2id.DefaultParams.Parallelism,
		SaltLength:  argon2id.DefaultParams.SaltLength,
		KeyLength:   argon2id.DefaultParams.KeyLength,
	}); err != nil {
		return "", err
	} else {
		return hashed, nil
	}
}

func CheckPasswordHash(password, hash string) (bool, error) {
	if match, err := argon2id.ComparePasswordAndHash(password, hash); err != nil {
		return match, err
	} else {
		return match, nil
	}
}

func MakeJWT(userID uuid.UUID, tokenSecret string, expiresIn time.Duration) (string, error) {
	token1 := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Issuer:    "chirpy-access",
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(expiresIn)),
		Subject:   userID.String(),
	})
	token2, err := token1.SignedString([]byte(tokenSecret))
	if err != nil {
		return "", err
	}
	return token2, nil
}

func ValidateJWT(tokenString, tokenSecret string) (uuid.UUID, error) {
	claims := jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(token *jwt.Token) (any, error) {
		return []byte(tokenSecret), nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	sub, err := token.Claims.GetSubject()
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(sub)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func GetBearerToken(headers http.Header) (string, error) {
	token := headers.Get("Authorization")
	if len(token) == 0 {
		return "", errors.New("no auth token")
	}
	splitted := strings.Split(token, " ")
	if len(splitted) != 2 || splitted[0] != "Bearer" || splitted[1] == "" {
		return "", errors.New("invalid bearer token")
	}
	return splitted[1], nil
}

func MakeRefreshToken() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}
	s := hex.EncodeToString(b)
	return s
}
