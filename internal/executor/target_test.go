package executor

import "testing"

func TestValidTargetRejectsControlChars(t *testing.T) {
	if err := validTarget("/x/a\r\nDELE /y"); err == nil {
		t.Fatal("CR/LF target accepted")
	}
}
