package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	args := os.Args[1:]
	ctx := context.Background()

	if err := ssh(ctx, args); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
