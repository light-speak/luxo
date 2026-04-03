package main

import (
	"log"
	"os"

	"github.com/light-speak/luxo/pkg/lsp"
)

func main() {
	logFile, err := os.OpenFile("/tmp/luxo-lsp.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		os.Exit(1)
	}
	defer logFile.Close()

	logger := log.New(logFile, "", log.LstdFlags)
	server := lsp.NewServer(os.Stdin, os.Stdout, logger)

	if err := server.Run(); err != nil {
		logger.Printf("server error: %v", err)
		os.Exit(1)
	}
}
