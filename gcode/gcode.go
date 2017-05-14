package gcode

import "fmt"

// Takes a g-code command, such as "G28 Z0 F150", and transforms it
// into the defensive form that includes the desired line number
// and a hash, for example, N9 G28 Z0 F150*2
func AddLineAndHash(lineno int, gcode string) string {
	str := fmt.Sprintf("N%d %s", lineno, gcode)
	var sum byte
	for i := 0; i < len(str); i++ {
		sum ^= str[i]
	}
	return fmt.Sprintf("%s*%d", str, sum)
}
