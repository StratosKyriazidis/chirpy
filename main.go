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

	"github.com/StratosKyriazidis/chirpy/internal/database"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	godotenv.Load()
	dbURL := os.Getenv("DB_URL")
	platform := os.Getenv("PLATFORM")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		fmt.Println("Error connecting to database!")
		return
	}
	dbQueries := database.New(db)
	apiCfg := apiConfig{database: dbQueries, platform: platform}
	serverMux := http.ServeMux{}
	serverMux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(".")))))
	serverMux.HandleFunc("GET /api/healthz", healthHandler)
	serverMux.HandleFunc("GET /admin/metrics", apiCfg.numberOfRequestsLogger)
	serverMux.HandleFunc("POST /admin/reset", apiCfg.reset)
	serverMux.HandleFunc("POST /api/chirps", apiCfg.chirpHandler)
	serverMux.HandleFunc("GET /api/chirps", apiCfg.getChirps)
	serverMux.HandleFunc("POST /api/users", apiCfg.createUser)
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
}

func (cfg *apiConfig) createUser(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	body := struct {
		Email string `json:"email"`
	}{}
	err := decoder.Decode(&body)
	if err != nil {
		log.Printf("Error decoding parameters: %s", err)
		respondWithError(w, 500, "Something went wrong")
		return
	}
	defer r.Body.Close()
	usr, err := cfg.database.CreateUser(r.Context(), database.CreateUserParams{ID: uuid.New(), Email: body.Email, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if err != nil {
		respondWithError(w, 500, "Something went wrong")
		return
	}
	respondWithJSON(w, 201, usr)
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
		UserID:    body.UserID,
	}
	cfg.database.CreateChirp(r.Context(), chirp)
	respondWithJSON(w, 201, chirp)
}

func (cfg *apiConfig) getChirps(w http.ResponseWriter, r *http.Request) {
	chirps, err := cfg.database.GetChirps(r.Context())
	if err != nil {
		respondWithError(w, 500, "Could not retrieve chirps")
		return
	}
	respondWithJSON(w, 200, chirps)
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
	w.WriteHeader(code)
	w.Header().Set("Content-Type", "application/json")
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
