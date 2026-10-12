package main

import (
	"fmt"

	productidentity "x-science"
)

func main() {
	identity := productidentity.Current()
	fmt.Printf("%s\t%s\n", identity.MachineSlug, identity.Version)
}
