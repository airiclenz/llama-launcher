package main

import (
	"os"

	"github.com/airiclenz/llama-launcher/internal/launcher"
)

func main() {
	os.Exit(launcher.Run(os.Args[1:]))
}
