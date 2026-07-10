package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vaultdb/internal/crypto"
	"vaultdb/internal/osdisk"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: vaultdb-encrypt <command> [flags]")
		fmt.Println("Commands: init, status, generate-key, migrate, rotate-kek, rotate-dek")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "init":
		cmdInit()
	case "status":
		cmdStatus()
	case "generate-key":
		cmdGenerateKey()
	case "migrate":
		cmdMigrate()
	case "rotate-kek":
		cmdRotateKEK()
	case "rotate-dek":
		cmdRotateDEK()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func cmdInit() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := fs.String("database", "", "Path to database directory")
	fs.Parse(os.Args[2:])

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "--database is required")
		os.Exit(1)
	}

	passphrase, err := readPassphrase()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading passphrase: %v\n", err)
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

	ks := crypto.NewPassphraseKeySource(passphrase, salt)
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

func readPassphrase() (string, error) {
	// 1. Try environment variable first.
	if env := os.Getenv("VAULTDB_ENCRYPTION_PASSPHRASE"); env != "" {
		return env, nil
	}

	// 2. Read from stdin if not a terminal.
	if !isTerminal() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return strings.TrimSpace(scanner.Text()), nil
		}
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		return "", fmt.Errorf("no passphrase provided on stdin")
	}

	// 3. Interactive prompt.
	fmt.Fprint(os.Stderr, "Enter passphrase: ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	return "", fmt.Errorf("no passphrase entered")
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
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

	key, err := crypto.GenerateKey()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error generating key: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outputPath, key, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing key: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generated 256-bit key saved to: %s\n", *outputPath)
}

func cmdMigrate() {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dbPath := fs.String("database", "", "Path to database directory")
	fs.Parse(os.Args[2:])

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "--database is required")
		os.Exit(1)
	}

	passphrase := os.Getenv("VAULTDB_ENCRYPTION_PASSPHRASE")
	if passphrase == "" {
		fmt.Fprintln(os.Stderr, "VAULTDB_ENCRYPTION_PASSPHRASE required")
		os.Exit(1)
	}

	saltPath := filepath.Join(*dbPath, ".salt")
	var salt []byte
	if data, err := os.ReadFile(saltPath); err == nil {
		salt = data
	} else {
		var err error
		salt, err = crypto.GenerateSalt()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating salt: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(saltPath, salt, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving salt: %v\n", err)
			os.Exit(1)
		}
	}

	ks := crypto.NewPassphraseKeySource(passphrase, salt)
	dekMgr := crypto.NewDEKManager(*dbPath)
	em, err := dekMgr.GenerateAndStoreDEK(context.Background(), ks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer em.Zeroize()

	fmt.Println("Encryption migration initialized.")
	fmt.Println("Note: Actual page encryption requires running server.")
}

func cmdRotateKEK() {
	fs := flag.NewFlagSet("rotate-kek", flag.ExitOnError)
	dbPath := fs.String("database", "", "Path to database directory")
	fs.Parse(os.Args[2:])

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "--database is required")
		os.Exit(1)
	}

	oldPass := os.Getenv("VAULTDB_ENCRYPTION_PASSPHRASE")
	newPass := os.Getenv("VAULTDB_ENCRYPTION_PASSPHRASE_NEW")
	if oldPass == "" || newPass == "" {
		fmt.Fprintln(os.Stderr, "VAULTDB_ENCRYPTION_PASSPHRASE and VAULTDB_ENCRYPTION_PASSPHRASE_NEW required")
		os.Exit(1)
	}

	saltPath := filepath.Join(*dbPath, ".salt")
	salt, err := os.ReadFile(saltPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading salt: %v\n", err)
		os.Exit(1)
	}

	oldKS := crypto.NewPassphraseKeySource(oldPass, salt)
	newKS := crypto.NewPassphraseKeySource(newPass, salt)

	ctx := context.Background()
	oldKEK, err := oldKS.GetKEK(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deriving old KEK: %v\n", err)
		os.Exit(1)
	}
	defer crypto.ZeroizeSlice(oldKEK)

	dekPath := filepath.Join(*dbPath, ".dek.enc")
	encDEK, err := os.ReadFile(dekPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading DEK: %v\n", err)
		os.Exit(1)
	}

	dek, err := crypto.DecryptDEK(encDEK, oldKEK)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decrypting DEK: %v\n", err)
		os.Exit(1)
	}
	defer crypto.ZeroizeSlice(dek)

	newKEK, err := newKS.GetKEK(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deriving new KEK: %v\n", err)
		os.Exit(1)
	}
	defer crypto.ZeroizeSlice(newKEK)

	newEncDEK, err := crypto.EncryptDEK(dek, newKEK)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encrypting DEK: %v\n", err)
		os.Exit(1)
	}

	tmpPath := dekPath + ".tmp"
	if err := os.WriteFile(tmpPath, newEncDEK, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing new DEK: %v\n", err)
		os.Exit(1)
	}
	if err := os.Rename(tmpPath, dekPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error replacing DEK: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("KEK rotation complete.")
}

func cmdRotateDEK() {
	fs := flag.NewFlagSet("rotate-dek", flag.ExitOnError)
	dbPath := fs.String("database", "", "Path to database directory")
	fs.Parse(os.Args[2:])

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "--database is required")
		os.Exit(1)
	}

	fmt.Println("DEK rotation requires running server.")
	fmt.Println("Use: vaultdb-encrypt rotate-dek --database mydb")
	fmt.Println("This will generate a new DEK and re-encrypt all pages.")
}
