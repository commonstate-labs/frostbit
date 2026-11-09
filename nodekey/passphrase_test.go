package nodekey

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

type failingReader struct{ owned []byte }

func (r *failingReader) Read(p []byte) (int, error) {
	r.owned = p
	copy(p, "dummy password")
	return len("dummy password"), errors.New("test read failure")
}

func TestBoundedReadAndTerminalUTF8(t *testing.T) {
	r := new(failingReader)
	if got, err := readBounded(r, MaxPassphraseBytes); err == nil || got != nil {
		t.Fatal("failed reader accepted")
	}
	if !bytes.Equal(r.owned, make([]byte, len(r.owned))) {
		t.Fatal("read error left owned buffer uncleared")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	const password = " 中文🔒 passphrase "
	if _, err := writer.Write([]byte(password + "\r\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	got, err := readTerminalLine(reader)
	if err != nil || string(got) != password {
		t.Fatalf("UTF-8 passphrase changed: %v", err)
	}
	if !bytes.Equal(got[len(got):cap(got)], make([]byte, cap(got)-len(got))) {
		t.Fatal("returned passphrase buffer has nonzero unused tail")
	}
	Wipe(got)
}

func TestPassphraseBoundaryAndWhitespace(t *testing.T) {
	for _, input := range []string{" leading and trailing ", " leading and trailing \n", " leading and trailing \r\n"} {
		got, err := normalizePassphrase([]byte(input))
		if err != nil || string(got) != " leading and trailing " {
			t.Fatalf("password whitespace changed: %q, %v", got, err)
		}
		Wipe(got)
	}
	for _, input := range []string{"", "\n", "\r\n", "a\nb", "a\rb", "a\x00b", strings.Repeat("x", MaxPassphraseBytes+1), "x\n\n"} {
		buffer := []byte(input)
		got, err := normalizePassphrase(buffer)
		if err == nil || got != nil {
			t.Fatal("invalid passphrase accepted")
		}
		if bytes.ContainsAny(buffer, "abx") {
			t.Fatal("error path left owned secret bytes uncleared")
		}
	}
	if got, err := normalizePassphrase(bytes.Repeat([]byte("x"), MaxPassphraseBytes)); err != nil || len(got) != MaxPassphraseBytes {
		t.Fatalf("maximum accepted password rejected: %v", err)
	} else {
		Wipe(got)
	}
}

func TestPassphraseFDIsExplicitBoundedAndClosed(t *testing.T) {
	for _, fd := range []int{-1, 0, 1, 2} {
		if got, err := ReadPassphraseFD(fd); err == nil || got != nil {
			t.Fatal("standard or invalid descriptor accepted")
		}
	}
	for _, input := range []string{" passphrase with spaces \r\n", strings.Repeat("x", MaxPassphraseBytes+10)} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
		writer.Close()
		got, err := ReadPassphraseFD(int(reader.Fd()))
		if len(input) > MaxPassphraseBytes+2 {
			if err == nil || got != nil {
				t.Fatal("oversized FD accepted")
			}
		} else if err != nil || string(got) != " passphrase with spaces " {
			t.Fatalf("FD passphrase changed: %v", err)
		}
		Wipe(got)
		var one [1]byte
		if _, err := reader.Read(one[:]); err == nil {
			t.Fatal("consumed FD was not closed")
		}
		reader.Close()
	}
}
