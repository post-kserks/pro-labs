package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"vaultdb/internal/crypto"
	"vaultdb/internal/osdisk"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: vaultdb-encrypt <command> [flags]")
		fmt.Println("Commands: init, status, generate-key")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cmdInit()
	case "status":
		cmdStatus()
	case "generate-key":
		cmdGenerateKey()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdInit() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := fs.String("database", "", "Path to database directory")
	passphrase := fs.String("passphrase", "", "Encryption passphrase")
	fs.Parse(os.Args[2:])

	if *dbPath == "" || *passphrase == "" {
		fmt.Fprintln(os.Stderr, "--database and --passphrase are required")
		os.Exit(1)
	}

	salt, err := crypto.GenerateSalt()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating salt: %v\n", err)
		os.Exit(1)
	}

	saltPath := filepath.Join(*dbPath, ".salt")
	if err := os.WriteFile(saltPath, salt, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving salt: %v\n", err)
		os.Exit(1)
	}

	ks := crypto.NewPassphraseKeySource(*passphrase, salt)
	dekMgr := crypto.NewDEKManager(*dbPath)

	ctx := context.Background()
	em, err := dekMgr.GenerateAndStoreDEK(ctx, ks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating DEK: %v\n", err)
		os.Exit(1)
	}
	defer em.Zeroize()

	fmt.Println("Encryption initialized successfully.")
	fmt.Printf("  Database: %s\n", *dbPath)
	fmt.Printf("  Algorithm: AES-256-GCM\n")
	fmt.Printf("  Key source: passphrase\n")
}

func cmdStatus() {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dbPath := fs.String("database", "", "Path to database directory")
	fs.Parse(os.Args[2:])

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "--database is required")
		os.Exit(1)
	}

	dekPath := filepath.Join(*dbPath, ".dek.enc")
	if _, err := os.Stat(dekPath); os.IsNotExist(err) {
		fmt.Println("Encryption: not initialized")
		return
	}

	fmt.Println("Encryption: enabled")
	fmt.Printf("  DEK file: %s\n", dekPath)

	metaPath := filepath.Join(*dbPath, ".encryption_meta.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		fmt.Printf("  Metadata: %s\n", string(data))
	}

	status, err := osdisk.DetectDiskEncryption(*dbPath)
	if err == nil {
		fmt.Printf("  OS disk encryption: %s (detected: %v)\n", status.Mechanism, status.Encrypted)
	}
}

func cmdGenerateKey() {
	fs := flag.NewFlagSet("generate-key", flag.ExitOnError)
	outputPath := fs.String("output", "key.bin", "Output file for generated key")
	fs.Parse(os.Args[2:])

	salt, err := crypto.GenerateSalt()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating key: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outputPath, salt, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated 256-bit key saved to: %s\n", *outputPath)
}
