package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/StratosKyriazidis/chirpy/internal/auth"
	"github.com/StratosKyriazidis/chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	secret := os.Getenv("SECRET")
	polkaKey := os.Getenv("POLKA_KEY")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println("Error connecting to database!")
		return
	}
	dbQueries := database.New(db)
	apiCfg := apiConfig{
		database: dbQueries,
		platform: platform,
		secret:   secret,
		polkaKey: polkaKey,
	}
	serverMux := http.ServeMux{}
	serverMux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(".")))))
	serverMux.HandleFunc("GET /api/healthz", healthHandler)
	serverMux.HandleFunc("GET /admin/metrics", apiCfg.numberOfRequestsLogger)
	serverMux.HandleFunc("POST /admin/reset", apiCfg.reset)
	serverMux.HandleFunc("POST /api/chirps", apiCfg.chirpHandler)
	serverMux.HandleFunc("GET /api/chirps", apiCfg.getChirps)
	serverMux.HandleFunc("GET /api/chirps/{chirpID}", apiCfg.getChirp)
	serverMux.HandleFunc("DELETE /api/chirps/{chirpID}", apiCfg.deleteChirp)
	serverMux.HandleFunc("POST /api/users", apiCfg.createUser)
	serverMux.HandleFunc("PUT /api/users", apiCfg.updateUser)
	serverMux.HandleFunc("POST /api/login", apiCfg.login)
	serverMux.HandleFunc("POST /api/refresh", apiCfg.refresh)
	serverMux.HandleFunc("POST /api/revoke", apiCfg.revoke)
	serverMux.HandleFunc("POST /api/polka/webhooks", apiCfg.polkaWebhooks)
	server := http.Server{
		Handler: &serverMux,
		Addr:    ":8080",
	}
	server.ListenAndServe()
}

func healthHandler(resWriter http.ResponseWriter, _ *http.Request) {
	resWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	resWriter.WriteHeader(200)
	resWriter.Write([]byte("OK"))
}

type apiConfig struct {
	fileserverHits atomic.Int32
	database       *database.Queries
	platform       string
	secret         string
	polkaKey       string
}

type UserResponse struct {
	ID           uuid.UUID `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Email        string    `json:"email"`
	IsChirpyRed  bool      `json:"is_chirpy_red"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
}

type CreateUserDto struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AccessTokenResponse struct {
	Token string `json:"token"`
}

type PolkaWebhookRequest struct {
	Event string `json:"event"`
	Data  struct {
		UserID uuid.UUID `json:"user_id"`
	} `json:"data"`
}

func (cfg *apiConfig) login(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	body := CreateUserDto{}
	err := decoder.Decode(&body)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		respondWithError(w, 500, "Something went wrong")
		return
	}
	defer r.Body.Close()
	usr, err := cfg.database.GetUser(r.Context(), body.Email)
	if err != nil {
		respondWithError(w, 401, "Incorrect email or password")
		return
	}
	if match, err := auth.CheckPasswordHash(body.Password, usr.HashedPassword); err != nil || !match {
		respondWithError(w, 401, "Incorrect email or password")
		return
	} else {
		token, err := auth.MakeJWT(usr.ID, cfg.secret, time.Duration(3600)*time.Second)
		if err != nil {
			respondWithError(w, 500, "Could not generate token")
			return
		}
		refToken := auth.MakeRefreshToken()
		err = cfg.database.SaveToken(r.Context(), database.SaveTokenParams{
			Token:     refToken,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			UserID:    usr.ID,
			ExpiresAt: time.Now().Add(time.Duration(24*60) * time.Hour),
			RevokedAt: sql.NullTime{},
		})
		if err != nil {
			respondWithError(w, 500, "Could not generate ref_token")
			return
		}
		respondWithJSON(w, 200, UserResponse{
			ID:           usr.ID,
			CreatedAt:    usr.CreatedAt,
			UpdatedAt:    usr.UpdatedAt,
			Email:        usr.Email,
			IsChirpyRed:  usr.IsChirpyRed,
			Token:        token,
			RefreshToken: refToken,
		})
	}
}

func (cfg *apiConfig) refresh(w http.ResponseWriter, r *http.Request) {
	refTokenString, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}
	refToken, err := cfg.database.GetToken(r.Context(), refTokenString)
	if err != nil {
		respondWithError(w, 401, "Token not found in database")
		return
	}
	if refToken.ExpiresAt.Before(time.Now()) || refToken.RevokedAt.Valid {
		respondWithError(w, 401, "Token has expired or been revoked")
		return
	}
	user, err := cfg.database.GetUserFromRefreshToken(r.Context(), refTokenString)
	if err != nil {
		respondWithError(w, 401, "Token not found in database")
		return
	}
	token, err := auth.MakeJWT(user.ID, cfg.secret, time.Hour)
	if err != nil {
		respondWithError(w, 500, "Could not generate token")
		return
	}
	respondWithJSON(w, 200, AccessTokenResponse{Token: token})
}

func (cfg *apiConfig) revoke(w http.ResponseWriter, r *http.Request) {
	refTokenString, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}
	now := time.Now()
	err = cfg.database.RevokeRefreshToken(r.Context(), database.RevokeRefreshTokenParams{
		Token:     refTokenString,
		RevokedAt: sql.NullTime{Time: now, Valid: true},
		UpdatedAt: now,
	})
	if err != nil {
		respondWithError(w, 500, "Could not revoke token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (cfg *apiConfig) createUser(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	body := CreateUserDto{}
	err := decoder.Decode(&body)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		respondWithError(w, 500, "Something went wrong")
		return
	}
	defer r.Body.Close()
	hashed, err := auth.HashPassword(body.Password)
	if err != nil {
		respondWithError(w, 500, "Something went wrong")
		return
	}
	usr, err := cfg.database.CreateUser(r.Context(), database.CreateUserParams{
		ID:             uuid.New(),
		Email:          body.Email,
		HashedPassword: hashed,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	})
	if err != nil {
		respondWithError(w, 500, "Something went wrong")
		return
	}
	respondWithJSON(w, 201, UserResponse{
		ID:          usr.ID,
		CreatedAt:   usr.CreatedAt,
		UpdatedAt:   usr.UpdatedAt,
		Email:       usr.Email,
		IsChirpyRed: usr.IsChirpyRed,
	})
}

func (cfg *apiConfig) updateUser(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}

	decoder := json.NewDecoder(r.Body)
	body := CreateUserDto{}
	err = decoder.Decode(&body)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		respondWithError(w, 500, "Something went wrong")
		return
	}
	defer r.Body.Close()

	hashed, err := auth.HashPassword(body.Password)
	if err != nil {
		respondWithError(w, 500, "Something went wrong")
		return
	}

	usr, err := cfg.database.UpdateUser(r.Context(), database.UpdateUserParams{
		ID:             userID,
		Email:          body.Email,
		HashedPassword: hashed,
		UpdatedAt:      time.Now(),
	})
	if err != nil {
		respondWithError(w, 500, "Something went wrong")
		return
	}

	respondWithJSON(w, 200, UserResponse{
		ID:          usr.ID,
		CreatedAt:   usr.CreatedAt,
		UpdatedAt:   usr.UpdatedAt,
		Email:       usr.Email,
		IsChirpyRed: usr.IsChirpyRed,
	})
}

func (cfg *apiConfig) polkaWebhooks(w http.ResponseWriter, r *http.Request) {
	apiKey, err := auth.GetAPIKey(r.Header)
	if err != nil || apiKey != cfg.polkaKey {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	decoder := json.NewDecoder(r.Body)
	body := PolkaWebhookRequest{}
	err = decoder.Decode(&body)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		respondWithError(w, 404, "Something went wrong")
		return
	}
	defer r.Body.Close()

	if body.Event != "user.upgraded" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	_, err = cfg.database.UpgradeUserToChirpyRed(r.Context(), database.UpgradeUserToChirpyRedParams{
		ID:        body.Data.UserID,
		UpdatedAt: time.Now(),
	})
	if err != nil {
		if err == sql.ErrNoRows {
			respondWithError(w, 404, "User not found")
			return
		}
		respondWithError(w, 404, "Something went wrong")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1)
		next.ServeHTTP(w, r)
	})
}

func (cfg *apiConfig) numberOfRequestsLogger(resWriter http.ResponseWriter, _ *http.Request) {
	resWriter.Header().Set("Content-Type", "text/html")
	html := fmt.Sprintf(`<html>
  <body>
    <h1>Welcome, Chirpy Admin</h1>
    <p>Chirpy has been visited %d times!</p>
  </body>
</html>`, cfg.fileserverHits.Load())
	resWriter.Write([]byte(html))
}

func (cfg *apiConfig) reset(w http.ResponseWriter, r *http.Request) {
	if cfg.platform == "dev" {
		cfg.fileserverHits.Store(0)
		cfg.database.DeleteUsers(r.Context())
		cfg.database.DeleteChirps(r.Context())
		w.WriteHeader(200)
	} else {
		w.WriteHeader(403)
	}
}

func (cfg *apiConfig) chirpHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	body := chirpCreateBody{}
	err := decoder.Decode(&body)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		respondWithError(w, 500, "Something went wrong")
		return
	}
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, 401, "Invalid token 2")
		return
	}
	if len(body.Body) > 140 {
		respondWithError(w, 400, "Chirp is too long")
		return
	}
	clean := cleanBody(body.Body)
	chirp := database.CreateChirpParams{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Body:      clean,
		UserID:    userID,
	}
	cfg.database.CreateChirp(r.Context(), chirp)
	respondWithJSON(w, 201, chirp)
}

func (cfg *apiConfig) getChirps(w http.ResponseWriter, r *http.Request) {
	authorIDString := r.URL.Query().Get("author_id")

	var (
		chirps []database.Chirp
		err    error
	)
	if authorIDString == "" {
		chirps, err = cfg.database.GetChirps(r.Context())
	} else {
		authorID, parseErr := uuid.Parse(authorIDString)
		if parseErr != nil {
			respondWithError(w, 400, "Bad author_id value")
			return
		}
		chirps, err = cfg.database.GetChirpsByAuthor(r.Context(), authorID)
	}

	if err != nil {
		respondWithError(w, 500, "Could not retrieve chirps")
		return
	}
	respondWithJSON(w, 200, chirps)
}

func (cfg *apiConfig) getChirp(w http.ResponseWriter, r *http.Request) {
	idString := r.PathValue("chirpID")
	id, err := uuid.Parse(idString)
	if err != nil {
		respondWithError(w, 400, "Bad uuid value")
		return
	}
	chirp, err := cfg.database.GetChirp(r.Context(), id)
	if err != nil {
		respondWithError(w, 404, "Chirp not found")
		return
	}
	respondWithJSON(w, 200, chirp)
}

func (cfg *apiConfig) deleteChirp(w http.ResponseWriter, r *http.Request) {
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}
	userID, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		respondWithError(w, 401, "Invalid token")
		return
	}

	idString := r.PathValue("chirpID")
	id, err := uuid.Parse(idString)
	if err != nil {
		respondWithError(w, 400, "Bad uuid value")
		return
	}

	chirp, err := cfg.database.GetChirp(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			respondWithError(w, 404, "Chirp not found")
			return
		}
		respondWithError(w, 500, "Could not retrieve chirp")
		return
	}
	if chirp.UserID != userID {
		respondWithError(w, 403, "Forbidden")
		return
	}

	err = cfg.database.DeleteChirp(r.Context(), id)
	if err != nil {
		respondWithError(w, 500, "Could not delete chirp")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type chirpCreateBody struct {
	Body   string    `json:"body"`
	UserID uuid.UUID `json:"user_id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func respondWithError(w http.ResponseWriter, code int, msg string) {
	data, err := json.Marshal(errorResponse{Error: msg})
	if err != nil {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func respondWithJSON(w http.ResponseWriter, code int, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		respondWithError(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(data)
}

func cleanBody(body string) string {
	splitted := strings.Split(body, " ")
	for i, v := range splitted {
		switch strings.ToLower(v) {
		case "kerfuffle":
			splitted[i] = "****"
		case "sharbert":
			splitted[i] = "****"
		case "fornax":
			splitted[i] = "****"
		}
	}
	return strings.Join(splitted, " ")
}
