package auth

import (
	"crypto/rand"
	"io"
	"math/big"
)

const generatedPasswordLength = 12
const generatedPasswordAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func GeneratePassword() (string, error) {
	return generatePassword(rand.Reader)
}

func generatePassword(random io.Reader) (string, error) {
	limit := big.NewInt(int64(len(generatedPasswordAlphabet)))
	// Rejection sampling keeps every accepted password equally likely while
	// requiring uppercase, lowercase and digits without predictable positions.
	for {
		password := make([]byte, generatedPasswordLength)
		var upper, lower, digit bool
		for i := range password {
			n, err := rand.Int(random, limit)
			if err != nil {
				return "", err
			}
			ch := generatedPasswordAlphabet[n.Int64()]
			password[i] = ch
			upper = upper || ch >= 'A' && ch <= 'Z'
			lower = lower || ch >= 'a' && ch <= 'z'
			digit = digit || ch >= '0' && ch <= '9'
		}
		if upper && lower && digit {
			return string(password), nil
		}
	}
}
