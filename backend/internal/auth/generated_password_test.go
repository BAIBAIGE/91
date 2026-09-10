package auth

import (
	"bytes"
	"errors"
	"testing"
)

func TestGeneratedPasswordsHaveRequiredLengthAndCharacters(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		password, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(password) != 12 {
			t.Fatalf("length=%d", len(password))
		}
		var upper, lower, digit bool
		for _, ch := range password {
			switch {
			case ch >= 'A' && ch <= 'Z':
				upper = true
			case ch >= 'a' && ch <= 'z':
				lower = true
			case ch >= '0' && ch <= '9':
				digit = true
			default:
				t.Fatal("password contains a non-alphanumeric character")
			}
		}
		if !upper || !lower || !digit {
			t.Fatal("password is missing a required character class")
		}
		if seen[password] {
			t.Fatal("generator repeated a password")
		}
		seen[password] = true
	}
}

func TestPasswordGeneratorRejectsMissingCharacterClasses(t *testing.T) {
	entropy := append(bytes.Repeat([]byte{0}, 12), []byte{0, 26, 52, 1, 27, 53, 2, 28, 54, 3, 29, 55}...)
	password, err := generatePassword(bytes.NewReader(entropy))
	if err != nil {
		t.Fatal(err)
	}
	if password != "Aa0Bb1Cc2Dd3" {
		t.Fatal("generator did not reject the uppercase-only candidate")
	}
}

type passwordEntropyFailure struct{ err error }

func (r passwordEntropyFailure) Read([]byte) (int, error) { return 0, r.err }

func TestPasswordGeneratorPropagatesEntropyFailure(t *testing.T) {
	failure := errors.New("entropy unavailable")
	password, err := generatePassword(passwordEntropyFailure{failure})
	if password != "" || !errors.Is(err, failure) {
		t.Fatalf("generated=%v err=%v", password != "", err)
	}
}
