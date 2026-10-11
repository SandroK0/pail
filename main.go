package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"go-objectstore/config"
)

type UploadResponse struct {
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// usage

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Error retrieving the file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileName := header.Filename
	filePath := filepath.Join(config.StoragePath, fileName)

	createdFile, err := os.Create(filePath)
	if err != nil {
		// What should we return to client in cases like this, real reason or some generic error?
		log.Printf("Could not create a file")
	}

	_, err = io.Copy(createdFile, file)
	if err != nil {
		log.Printf("Could not copy file contents")
	}

	writeJSON(w, http.StatusOK, UploadResponse{Message: "Testing"})
}

func main() {
	logFile, err := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatalf("cannot open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	config.Load()

	if err := os.MkdirAll(config.StoragePath, 0o755); err != nil {
		log.Fatalf("cannot create storage directory %q: %v", config.StoragePath, err)
	}

	http.HandleFunc("/api/upload", uploadHandler)

	fmt.Println("Server starting on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		panic(err)
	}
}
