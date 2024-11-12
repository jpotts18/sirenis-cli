package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jpotts18/sirenis-cli/processing"
	"github.com/jpotts18/sirenis-cli/prompt"
	"github.com/openai/openai-go"
)

func initClient() *openai.Client {
	client := openai.NewClient() // Looks for OPENAI_API_KEY
	return client
}

func main() {
	client := initClient()
    // Define command-line flags
    importFlag := flag.String("in", "", "Path to the Markdown file to import")
    promptFlag := flag.Bool("prompt", false, "Start interactive prompt")
    flag.Parse()

    // Determine which mode to run
    if *importFlag != "" {
        // Import mode
        err := processing.ImportFile(client, *importFlag)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Error importing file: %v\n", err)
            os.Exit(1)
        }
    } else if *promptFlag {
        // Prompt mode
        err := prompt.Start(client)
        if err != nil {
            fmt.Fprintf(os.Stderr, "Error in prompt mode: %v\n", err)
            os.Exit(1)
        }
    } else {
        // No valid flags provided
        fmt.Println("Usage:")
        fmt.Println("  --import <path>   Import a Markdown file")
        fmt.Println("  --prompt          Start interactive prompt")
        os.Exit(1)
    }
}
