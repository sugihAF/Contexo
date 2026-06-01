// Command contexo-server runs the open-source Contexo API server. Cloud builds
// live in the private contexo-backend module, which calls app.Run with extra
// route registrars; this OSS entrypoint calls it with none.
package main

import (
	"log"

	"github.com/sugihAF/contexo/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
