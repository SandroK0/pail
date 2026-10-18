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

type JsonResponse struct {
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, JsonResponse{Message: "Error retrieving the file from the request"})
		return
	}
	defer file.Close()

	path := r.FormValue("path")
	dirPath := filepath.Join(config.StoragePath, path)
	err = os.MkdirAll(dirPath, os.ModePerm)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, JsonResponse{Message: "Error creating the directory"})
		return
	}

	fileName := header.Filename
	filePath := filepath.Join(dirPath, fileName)

	createdFile, err := os.Create(filePath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, JsonResponse{Message: "Error creating the file"})
		return
	}

	_, err = io.Copy(createdFile, file)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, JsonResponse{Message: "Error saving the file"})
		return
	}
	createdFile.Close()

	writeJSON(w, http.StatusOK, JsonResponse{Message: "File uploaded successfully"})
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
