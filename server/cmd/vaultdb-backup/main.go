package main

import (
	"flag"
	"fmt"
	"log"

	"vaultdb/internal/backup"
)

func main() {
	mode := flag.String("mode", "backup", "backup or restore")
	data := flag.String("data", "./data", "data directory")
	output := flag.String("output", "", "backup file path (backup mode)")
	flag.Parse()

	switch *mode {
	case "backup":
		if *output == "" {
			log.Fatal("output path required for backup mode")
		}
		if err := backup.Backup(*data, *output); err != nil {
			log.Fatalf("backup failed: %v", err)
		}
		fmt.Println("backup completed successfully")
	case "restore":
		if *output == "" {
			log.Fatal("backup file path required for restore mode")
		}
		if err := backup.Restore(*output, *data); err != nil {
			log.Fatalf("restore failed: %v", err)
		}
		fmt.Println("restore completed successfully")
	default:
		log.Fatalf("unknown mode: %s", *mode)
	}
}
