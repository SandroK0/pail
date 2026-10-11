package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// StoragePath is the directory where objects are stored. Set by Load.
var StoragePath string

// Load reads .env (if present) and populates config values.
func Load() {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, using system env")
	}

	StoragePath = os.Getenv("STORAGE_PATH")
}
