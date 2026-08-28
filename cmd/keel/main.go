// Command keel is a web development studio for any stack.
// See https://github.com/coullworks/keel and docs/ for the full story.
package main

import (
	"os"

	"github.com/coullworks/keel/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
