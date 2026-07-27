package main

import (
	"encoding/json"
	"fmt"
	"os"

	"aegisrt/internal/pressure"
)

func main() {
	snapshot, err := pressure.NewReader().Sample()
	if err != nil {
		fmt.Fprintln(os.Stderr, "read PSI:", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal PSI:", err)
		os.Exit(1)
	}

	fmt.Println(string(data))
}
