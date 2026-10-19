//go:build windows

package onpath

import "testing"

// Setup drops quai-dash on Windows right after putting quai-node on PATH;
// both live in the same bin directory, so that must not take it off again.
func TestOnlyQuaiNodeTakesDirOff(t *testing.T) {
	for target, want := range map[string]bool{
		`C:\ProgramData\quai-node\bin\quai-node.exe`: true,
		`C:\ProgramData\quai-node\bin\Quai-Node.EXE`: true,
		`C:\ProgramData\quai-node\bin\quai-dash.exe`: false,
		`C:\ProgramData\quai-node\bin\go-quai.exe`:   false,
	} {
		if got := takesDirOff(target); got != want {
			t.Errorf("takesDirOff(%q) = %v, want %v", target, got, want)
		}
	}
}
