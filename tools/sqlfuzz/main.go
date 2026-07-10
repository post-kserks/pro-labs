package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	count := flag.Int("count", 1000, "Number of queries to generate")
	seed := flag.Int64("seed", 0, "Random seed (0 = use current time)")
	schemaFile := flag.String("schema", "", "Path to JSON schema file (empty = use default schema)")
	flag.Parse()

	if *seed == 0 {
		*seed = time.Now().UnixNano()
	}

	var schema Schema
	var err error

	if *schemaFile != "" {
		schema, err = LoadSchema(*schemaFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading schema: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Loaded schema with %d tables\n", len(schema.Tables))
	} else {
		schema = DefaultSchema()
		fmt.Fprintf(os.Stderr, "Using default schema with %d tables\n", len(schema.Tables))
	}

	gen := NewGenerator(schema, *seed)

	for i := 0; i < *count; i++ {
		fmt.Println(gen.Generate())
	}
}
