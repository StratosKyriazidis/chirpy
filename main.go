package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
)

func main() {
	apiCfg := apiConfig{}
	serverMux := http.ServeMux{}
	serverMux.Handle("/app/", apiCfg.middlewareMetricsInc(http.StripPrefix("/app", http.FileServer(http.Dir(".")))))
	serverMux.HandleFunc("GET /api/healthz", healthHandler)
	serverMux.HandleFunc("GET /admin/metrics", apiCfg.numberOfRequestsLogger)
	serverMux.HandleFunc("POST /admin/reset", apiCfg.reset)
	serverMux.HandleFunc("POST /api/validate_chirp", bodyValidator)
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

func (cfg *apiConfig) reset(_ http.ResponseWriter, _ *http.Request) {
	cfg.fileserverHits.Store(0)
}

type body struct {
	Body string `json:"body"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func bodyValidator(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	body := body{}
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
	respondWithJSON(w, 200, struct {
		CleanedBody string `json:"cleaned_body"`
	}{CleanedBody: clean})
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
