package main

import (
	"os"

	app "github.com/bsamson00/dotseek/internal/dotseek"
)

func main() {
	os.Exit(app.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
