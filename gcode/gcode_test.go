package gcode

import "testing"

func TestAddLineAndHash(t *testing.T) {
	tests := []struct {
		lineno int
		cmd    string
		want   string
	}{
		{9, "G28 Z0 F150", "N9 G28 Z0 F150*2"},
	}
	for _, tt := range tests {
		got := AddLineAndHash(tt.lineno, tt.cmd)
		if got != tt.want {
			t.Errorf("(%d, %q), want: %q, got: %q", tt.lineno, tt.cmd, tt.want, got)
		}
	}
}
