// Generates gcode that takes a number of snapshots.
package main

import (
	"flag"
	"fmt"
	"time"
)

var (
	numSnapshots  = flag.Int("numSnapshots", 5, "Number of snapshots to take")
	pauseDuration = flag.Duration("pause", 1000*time.Millisecond, "How long to pause between move steps")
)

func main() {
	flag.Parse()

	fmt.Printf("; Take %d snapshots\n", *numSnapshots)
	delay := int(1000 * (*pauseDuration).Seconds())

	for i := 0; i < *numSnapshots; i++ {
		fmt.Printf("; Snapshot %d / %d\n", i, *numSnapshots)
		fmt.Printf("M7822\n")
		fmt.Printf("; host dwell\n")
		fmt.Printf("M7821 P%d\n\n", delay)
	}
}
